package terminal

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"image/color"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	xvt "github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/app/agentview"
	"github.com/drn/argus/internal/gitutil"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/drn/argus/internal/uxlog"
)

// replayScrollbackSize is the scrollback buffer for replay emulators used
// during scrollback browsing. 50K lines (~2500 screens at 20 rows) allows
// deep scrolling in long sessions without constant rebuilds. The live emulator
// uses x/vt's default (10K lines) since only the current viewport matters.
const replayScrollbackSize = 50_000

// NewDrainedEmulator creates an x/vt SafeEmulator with a goroutine that drains
// the response pipe. x/vt uses io.Pipe() internally — when the emulator
// processes terminal query sequences (DA1, DA2, DSR, etc.), it writes responses
// to pw which blocks until pr is read. Without draining, Write() hangs
// indefinitely on any input containing these sequences. The drain goroutine
// exits when the emulator is closed or garbage collected.
func NewDrainedEmulator(cols, rows int) *xvt.SafeEmulator {
	emu := xvt.NewSafeEmulator(cols, rows)
	go io.Copy(io.Discard, emu) //nolint:errcheck
	return emu
}

// newDrainedReplayEmulator creates an emulator with a large scrollback buffer
// for scrollback browsing. This avoids frequent rebuilds when scrolling up
// through long session output.
func newDrainedReplayEmulator(cols, rows int) *xvt.SafeEmulator {
	emu := xvt.NewSafeEmulator(cols, rows)
	emu.Emulator.SetScrollbackSize(replayScrollbackSize)
	go io.Copy(io.Discard, emu) //nolint:errcheck
	return emu
}

// SafeEmuWrite writes data to an x/vt emulator, recovering from panics caused
// by upstream bugs (e.g., InsertLineArea index-out-of-range when replay data
// contains cursor positions or scroll regions from a larger terminal).
func SafeEmuWrite(emu *xvt.SafeEmulator, data []byte) (n int, err error) {
	defer func() {
		if r := recover(); r != nil {
			uxlog.Log("[vt] recovered from emulator panic: %v\n%s", r, debug.Stack())
			err = fmt.Errorf("emulator panic: %v", r)
		}
	}()
	return emu.Write(data)
}

// Cursor colors — high-contrast, theme-independent.
var (
	cursorFG = tcell.PaletteColor(17)  // dark blue
	cursorBG = tcell.PaletteColor(153) // light blue
)

// cachedCell stores a single cell's screen position and content for fast replay.
type cachedCell struct {
	x, y  int
	ch    rune
	style tcell.Style
}

// TerminalPane renders PTY output natively to a tcell screen via x/vt.
// No ANSI string intermediary — x/vt cells map directly to tcell cells.
// No activeInputBG or findInputRow — the native surface shows upstream
// PTY output without Argus-injected highlights.
type TerminalPane struct {
	*tview.Box
	mu      sync.Mutex
	session agentview.TerminalAdapter
	taskID  string
	focused bool

	// Persistent x/vt emulator for live incremental rendering.
	emu           *xvt.SafeEmulator
	emuFedTotal   uint64
	emuCols       int
	emuRows       int
	cursorVisible bool

	// Cached PTY size — set from Draw() (main goroutine), read by sync goroutine.
	ptyCols int
	ptyRows int

	// Scrollback.
	scrollOffset int

	// Anchor-lock: track total lines so scrollOffset stays pinned when new output arrives.
	anchorTotalLines int // total lines when scrollOffset was last set

	// Scroll acceleration: tracks recent scroll events for keyboard acceleration.
	lastScrollTime time.Time // when last keyboard scroll happened
	scrollAccel    int       // current acceleration multiplier (1-based)

	// Replay emulator cache: reuse when only scroll changes (no new bytes).
	replayEmu              *xvt.SafeEmulator
	replayEmuBytes         uint64 // TotalWritten when replayEmu was built
	replayEmuCols          int
	replayEmuRows          int
	replayEmuLogSize       int64 // log file size when replayEmu was built (for log-backed scroll)
	replayEmuMaxScroll     int   // max scrollOffset the replay emulator was built for
	replayEmuCursorVisible bool  // cached cursor visibility from replay emulator
	// replayEmuFirstByte is the file offset of the first byte fed into
	// the current replayEmu (0 means "from the start of the file"). Rebuilds
	// triggered by scroll-past-maxScroll on an alive session must read from
	// no later than this offset, otherwise the user-visible scrollback
	// window jumps forward as new agent output pushes the 8MB read window
	// past the bytes the previous build had cached (defect 5).
	replayEmuFirstByte int64

	// Paint cache: stores the last paintEmu output so keystroke-triggered
	// redraws (no new bytes) can replay SetContent calls without touching
	// the emulator (no mutex, no allocations, no style conversion).
	paintCacheCells  []cachedCell
	paintCacheX      int // screen origin used when cache was built
	paintCacheY      int
	paintCacheW      int // viewport dimensions
	paintCacheH      int
	paintCacheValid  bool // true when cache can be replayed
	paintCacheScroll int  // scrollOffset when cache was built

	// Replay data for finished sessions (loaded from session log file).
	replayData []byte

	// Diff mode.
	diffMode         bool
	diffParsed       gitutil.ParsedDiff
	diffUnifiedLines []widget.RenderedDiffLine
	diffSplitLines   []widget.RenderedDiffLine
	diffSplitWidth   int // width used to build split lines (invalidate on resize)
	diffSplit        bool
	diffScroll       int
	diffFile         string

	// pendingResize is set by Draw() when panel dimensions differ from PTY.
	// The tick goroutine checks this and performs the resize RPC.
	pendingResizeRows uint16
	pendingResizeCols uint16

	// forceResync makes the next Draw() unconditionally repost a resize even
	// when panel dimensions appear unchanged. Used on agent-view entry to
	// recover from a stuck PTY size (e.g. a dropped SIGWINCH at session start).
	forceResync bool

	// Async replay rebuild: when Draw() hits a cache miss on the slow path,
	// it kicks off a background goroutine instead of blocking the main goroutine.
	// The goroutine builds the emulator and stores it, then calls OnNeedRedraw.
	replayBuilding       bool // true while a background rebuild is in flight
	replayRebuildPending bool // set by async rebuild to signal anchor/cache reset needed
	// replayNoDataUntil throttles re-kicks after asyncReplayRebuild bailed
	// out with empty inputs (no log, no ring, no replayData). Without this,
	// every subsequent Draw immediately spawns another goroutine that
	// immediately bails the same way, churning CPU at the tick rate while
	// the screen shows "Waiting for output..." (defect 12). Cleared as
	// soon as real input is detected on the next entry.
	replayNoDataUntil time.Time

	// pending is true when a task is being prepared (worktree creation) and
	// the session hasn't started yet. Draw() shows a launch banner instead of
	// "No active session".
	pending bool

	// OnClick is called when the user clicks on the terminal pane.
	// The app wires this to switch agentFocus back to the terminal.
	OnClick func()

	// OnNeedRedraw is called from a background goroutine when an async
	// replay rebuild completes. The app wires this to tapp.QueueUpdateDraw.
	OnNeedRedraw func()

	// OnBranchChange fires when Draw() will paint a different rendering
	// branch than the previous frame: SetSession (live↔nil↔replay),
	// SetTaskID (different task → potentially different replay log content,
	// or replayData clear → "Session not running" placeholder), SetPending
	// (banner↔normal), EnterDiffMode/ExitDiffMode/ToggleDiffSplit (PTY↔
	// unified diff↔split diff), scroll-mode 0↔nonzero (live↔replay
	// emulator), and async replay rebuild completion (fallback emulator →
	// fresh 50K-line emulator with potentially different content at the
	// same cell positions). Each branch paints different cells in the same
	// rect; tcell's diff-based Show() can leave stale cells from the
	// previous branch on screen. The app wires this to forceRedraw so
	// afterDraw runs Sync. See gotchas/ui-threading.md.
	//
	// Safe to fire from any goroutine: the callback is set once in
	// buildUI and never reassigned, and forceRedraw uses an atomic flag.
	OnBranchChange func()
}

// mouseScrollStep is the number of lines scrolled per mouse wheel tick.
const mouseScrollStep = 3

// scrollIndicatorCells is the rendered width of the "   [SCROLL]   " badge
// painted at the top row in scroll mode. Pre-reserved in paintCacheCells so
// the slice doesn't reallocate on every scroll frame (defect 2).
const scrollIndicatorCells = 14

// replayNoDataCooldown is how long the slow-path replay rebuild waits after
// bailing out with no input bytes before it's willing to re-spawn another
// goroutine. Defect 12 — without a cooldown, every Draw immediately kicks
// off a new goroutine that immediately bails the same way, burning CPU at
// the tick rate while the user sees the "Waiting for output..." placeholder.
const replayNoDataCooldown = 250 * time.Millisecond

// NewTerminalPane creates a native terminal rendering pane.
func NewTerminalPane() *TerminalPane {
	return &TerminalPane{
		Box: tview.NewBox(),
	}
}

// notifyBranchChange fires OnBranchChange if set. Call AFTER releasing tp.mu
// — the callback may invoke app-level code that takes other locks. Safe to
// call when OnBranchChange is nil (no-op).
func (tp *TerminalPane) notifyBranchChange() {
	if tp.OnBranchChange != nil {
		tp.OnBranchChange()
	}
}

// SetSession attaches a live session. Resets emulator state only when the
// session pointer actually changes — the tick calls this every second with
// the same session, and resetting the emulator each time would destroy
// incremental rendering state.
func (tp *TerminalPane) SetSession(sess agentview.TerminalAdapter) {
	tp.mu.Lock()
	if tp.session == sess {
		tp.mu.Unlock()
		return // same session, skip reset
	}
	if sess != nil {
		uxlog.Log("[terminalpane] SetSession: sess=%p totalWritten=%d", sess, sess.TotalWritten())
	} else {
		uxlog.Log("[terminalpane] SetSession: nil")
	}
	tp.session = sess
	tp.pending = false
	tp.emu = nil
	tp.emuFedTotal = 0
	tp.scrollOffset = 0
	tp.paintCacheValid = false
	// Seed PTY size from panel dimensions — Draw() will refine on first render.
	// Do NOT fall back to 80x24 when GetInnerRect returns zero (before first
	// Draw); leave ptyCols/ptyRows at 0 so Draw() sets them to match the
	// actual panel width. Falling back to 80 creates a mismatch with the PTY
	// (which was started at the correct width), causing the emulator to wrap
	// text at 80 cols even though the agent output is formatted wider.
	if sess != nil {
		_, _, w, h := tp.GetInnerRect()
		if w > 0 && h > 0 {
			tp.ptyCols = max(w, 20)
			tp.ptyRows = max(h, 5)
		}
	}
	tp.mu.Unlock()
	// Branch change: nil↔live↔replay paint different cells in the same rect.
	// Fire AFTER releasing the lock — the app-side handler may take other locks.
	tp.notifyBranchChange()
}

// Session returns the current session (thread-safe).
func (tp *TerminalPane) Session() agentview.TerminalAdapter {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	return tp.session
}

// SetTaskID sets the current task ID and loads session log if no live session.
//
// Branch change: clearing replayData and (potentially) loading a different
// session log can flip Draw between "replay finished session" content and
// "Session not running" text, or between two replay logs of different sizes.
// Today this is always paired with SetSession (which fires its own notify),
// but firing here too makes the contract explicit instead of relying on the
// implicit pairing — defense against a future caller that uses SetTaskID
// in isolation.
func (tp *TerminalPane) SetTaskID(id string) {
	prevID := tp.taskID
	hadReplay := len(tp.replayData) > 0
	tp.taskID = id
	tp.replayData = nil
	if id != "" && tp.Session() == nil {
		tp.loadSessionLog(id)
	}
	if prevID != id || hadReplay {
		tp.notifyBranchChange()
	}
}

// loadSessionLog reads up to 8MB from the tail of the session log file for
// finished-session replay. Bounded to liveRebuildHistorySize so a huge log
// (50MB+ for long-lived sessions) does not pin that much memory in tp.replayData
// for the lifetime of the agent view (defect 8). asyncReplayRebuild reads the
// full 8MB tail itself when it builds the replay emulator, so the cached
// tp.replayData only needs to back HasContent() and the rare path where the
// async rebuild can't reach the log (e.g., file was deleted underneath us).
func (tp *TerminalPane) loadSessionLog(taskID string) {
	data, fileSize := readLogTailForTask(taskID, liveRebuildHistorySize)
	if len(data) == 0 {
		return
	}
	uxlog.Log("[tui] loaded session log tail for %s (%d / %d bytes)", taskID, len(data), fileSize)
	tp.replayData = data
}

// SetPending sets the pending state. When true, Draw() shows a launch banner
// instead of "No active session". Cleared automatically by SetSession.
func (tp *TerminalPane) SetPending(v bool) {
	tp.mu.Lock()
	changed := tp.pending != v
	tp.pending = v
	tp.mu.Unlock()
	if changed {
		// Branch change: pending banner ↔ "No active session" message.
		tp.notifyBranchChange()
	}
}

// ForceResyncPTY schedules a one-shot unconditional resize on the next Draw().
// Call this on agent-view entry so a session whose PTY is stuck at a stale
// width gets reconciled to the current panel dimensions even when the delta
// check would miss.
func (tp *TerminalPane) ForceResyncPTY() {
	tp.mu.Lock()
	tp.forceResync = true
	tp.mu.Unlock()
}

// SyncPTYSize performs a pending PTY resize (RPC). Called from the tick
// goroutine — safe to block here. Draw() sets pendingResize* when panel
// dimensions change; this method consumes them and issues the resize RPC.
func (tp *TerminalPane) SyncPTYSize() {
	tp.mu.Lock()
	sess := tp.session
	rows := tp.pendingResizeRows
	cols := tp.pendingResizeCols
	tp.pendingResizeRows = 0
	tp.pendingResizeCols = 0
	tp.mu.Unlock()

	if sess == nil || !sess.Alive() || rows == 0 || cols == 0 {
		return
	}
	sess.Resize(rows, cols)
}

// SetFocused sets the focus state for border rendering.
func (tp *TerminalPane) SetFocused(f bool) {
	tp.focused = f
}

// EagerReplayBuild kicks off an async replay rebuild from the session log
// without waiting for Draw(). Called on session exit to pre-populate the
// replay emulator so the first Draw() hits the cache instead of showing a
// brief "Waiting for output..." flash.
//
// Skips when ptyCols/ptyRows are still zero (no Draw has happened yet) —
// substituting 80x24 defaults produces a cache that Draw() immediately
// invalidates as soon as the real panel dimensions arrive, wasting the
// pre-build (defect 13).
func (tp *TerminalPane) EagerReplayBuild() {
	tp.mu.Lock()
	if tp.replayBuilding || tp.taskID == "" {
		tp.mu.Unlock()
		return
	}
	cols := tp.ptyCols
	rows := tp.ptyRows
	if cols < 20 || rows < 5 {
		// Dimensions unknown — defer until Draw() resolves the panel size.
		tp.mu.Unlock()
		return
	}
	tp.replayBuilding = true
	taskID := tp.taskID
	onDone := tp.OnNeedRedraw
	tp.mu.Unlock()

	go tp.asyncReplayRebuild(taskID, 0, rows, cols, rows, nil, nil, 0, onDone)
}

// ResetVT clears all terminal state (on resize or task switch).
func (tp *TerminalPane) ResetVT() {
	tp.mu.Lock()
	tp.emu = nil
	tp.emuFedTotal = 0
	tp.scrollOffset = 0
	tp.anchorTotalLines = 0
	tp.replayEmu = nil
	tp.replayEmuBytes = 0
	tp.replayEmuLogSize = 0
	tp.replayEmuMaxScroll = 0
	tp.replayEmuFirstByte = 0
	tp.replayBuilding = false
	tp.replayRebuildPending = false
	tp.replayNoDataUntil = time.Time{}
	tp.replayData = nil
	tp.paintCacheValid = false
	tp.mu.Unlock()
	tp.ExitDiffMode()
}

// HasContent returns true if there is something to render.
func (tp *TerminalPane) HasContent() bool {
	tp.mu.Lock()
	sess := tp.session
	tp.mu.Unlock()
	if sess != nil {
		return sess.TotalWritten() > 0
	}
	return len(tp.replayData) > 0
}

// --- Scrollback ---

// scrollAccelWindow is the time window for key repeat acceleration.
// If a scroll event arrives within this window of the previous one,
// the acceleration multiplier increases.
const scrollAccelWindow = 120 * time.Millisecond

// scrollAccelMax caps the acceleration multiplier.
const scrollAccelMax = 12

func (tp *TerminalPane) ScrollUp(n int) {
	wasZero := tp.scrollOffset == 0
	if wasZero {
		tp.invalidateReplayCache()
	}
	tp.scrollOffset += n
	tp.paintCacheValid = false
	if wasZero {
		// Branch change 0→nonzero: live (renderLive) → replay emulator path.
		// The two paths fetch from different sources (ring buffer vs replay
		// emu) and can paint subtly different content (e.g. [SCROLL] indicator
		// only appears in replay).
		tp.notifyBranchChange()
	}
}
func (tp *TerminalPane) ScrollOffset() int { return tp.scrollOffset }
func (tp *TerminalPane) ResetScroll() {
	wasNonZero := tp.scrollOffset != 0
	tp.scrollOffset = 0
	tp.anchorTotalLines = 0
	tp.scrollAccel = 0
	tp.paintCacheValid = false
	if wasNonZero {
		// Branch change nonzero→0: replay → live.
		tp.notifyBranchChange()
	}
}

// AccelScrollUp performs an accelerated scroll up for keyboard key-repeat.
// Returns the actual number of lines scrolled.
func (tp *TerminalPane) AccelScrollUp() int {
	n := tp.nextAccelStep()
	wasZero := tp.scrollOffset == 0
	if wasZero {
		tp.invalidateReplayCache()
	}
	tp.scrollOffset += n
	tp.paintCacheValid = false
	if wasZero {
		// Branch change: live → replay (see ScrollUp).
		tp.notifyBranchChange()
	}
	return n
}

// invalidateReplayCache clears stale replay emulator state when entering
// scroll mode from live mode. The cached replay emu may be from a previous
// scroll (agent has written more since then), and anchorTotalLines from
// renderLive would cause anchor-lock to misfire.
func (tp *TerminalPane) invalidateReplayCache() {
	tp.mu.Lock()
	tp.replayEmu = nil
	tp.replayEmuBytes = 0
	tp.replayEmuLogSize = 0
	tp.replayEmuMaxScroll = 0
	tp.replayEmuFirstByte = 0
	tp.anchorTotalLines = 0
	tp.replayBuilding = false // allow new async rebuild after invalidation
	// Clear the no-data cooldown too — the cooldown was scoped to the
	// previous state's empty inputs; after invalidation we want the next
	// Draw to kick a fresh rebuild without waiting for the old timestamp.
	tp.replayNoDataUntil = time.Time{}
	tp.mu.Unlock()
}

// AccelScrollDown performs an accelerated scroll down for keyboard key-repeat.
func (tp *TerminalPane) AccelScrollDown() int {
	n := tp.nextAccelStep()
	wasNonZero := tp.scrollOffset != 0
	tp.scrollOffset -= n
	tp.paintCacheValid = false
	if tp.scrollOffset <= 0 {
		tp.scrollOffset = 0
		tp.anchorTotalLines = 0
		if wasNonZero {
			// Branch change: replay → live.
			tp.notifyBranchChange()
		}
	}
	return n
}

// nextAccelStep computes the scroll step based on acceleration state.
// Rapid key repeats within scrollAccelWindow ramp up the step from 1 to scrollAccelMax.
func (tp *TerminalPane) nextAccelStep() int {
	now := time.Now()
	if !tp.lastScrollTime.IsZero() && now.Sub(tp.lastScrollTime) < scrollAccelWindow {
		tp.scrollAccel++
		if tp.scrollAccel > scrollAccelMax {
			tp.scrollAccel = scrollAccelMax
		}
	} else {
		tp.scrollAccel = 1
	}
	tp.lastScrollTime = now
	return tp.scrollAccel
}

func (tp *TerminalPane) ScrollDown(n int) {
	wasNonZero := tp.scrollOffset != 0
	tp.scrollOffset -= n
	tp.paintCacheValid = false
	if tp.scrollOffset <= 0 {
		tp.scrollOffset = 0
		tp.anchorTotalLines = 0
		if wasNonZero {
			// Branch change: replay → live.
			tp.notifyBranchChange()
		}
	}
}

// statLogSize returns the current size of the session log file without reading it.
// Returns 0 if the file doesn't exist or can't be stat'd. This is a cheap syscall
// (~1μs) used to check replay emulator cache validity without the cost of a full read.
func (tp *TerminalPane) statLogSize() int64 {
	logPath := agent.SessionLogPath(tp.taskID)
	fi, err := os.Stat(logPath)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// readLogTailForTask reads the last `size` bytes from a session log file.
// Safe to call from any goroutine — uses only the passed taskID, no shared state.
//
// Returns (bytes, fileSize) where fileSize is the on-disk size when the read
// was issued. The returned slice may start at an arbitrary byte position in
// the stream and is NOT escape-sequence aligned — pass through
// alignToEscBoundary before feeding into a fresh terminal emulator.
func readLogTailForTask(taskID string, size int64) ([]byte, int64) {
	logPath := agent.SessionLogPath(taskID)
	f, err := os.Open(logPath)
	if err != nil {
		return nil, 0
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil || fi.Size() == 0 {
		return nil, 0
	}

	fileSize := fi.Size()
	readSize := size
	if readSize > fileSize {
		readSize = fileSize
	}

	offset := fileSize - readSize
	buf := make([]byte, readSize)
	n, err := f.ReadAt(buf, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, 0
	}
	return buf[:n], fileSize
}

// replayRebuildReadSize computes how many bytes asyncReplayRebuild should
// pull from the session log. Base size = (scrollOffset+viewport) * cols * 3,
// floored at 8MB to populate the 50K-line replay scrollback buffer. When
// `prevFirstByte > 0`, the size grows so the new firstByteOffset is ≤
// prevFirstByte — keeps the user's deep-scroll content from jumping out
// from under the cursor as the log grows (defect 5). Capped at
// replayRebuildMaxBytes (64MB) to bound a single synchronous read.
func replayRebuildReadSize(taskID string, scrollOffset, viewportHeight, ptyCols int, prevFirstByte int64) int64 {
	needed := int64(scrollOffset+viewportHeight) * int64(ptyCols) * 3
	if needed < 8*1024*1024 {
		needed = 8 * 1024 * 1024
	}
	if prevFirstByte > 0 {
		// readLogTailForTask returns bytes [fileSize - readSize, fileSize),
		// so readSize >= fileSize - prevFirstByte forces the start back to
		// the previous build's first byte.
		if fi, err := os.Stat(agent.SessionLogPath(taskID)); err == nil {
			if fi.Size() > prevFirstByte {
				needForMono := fi.Size() - prevFirstByte
				if needForMono > needed {
					needed = needForMono
				}
			}
		}
	}
	if needed > replayRebuildMaxBytes {
		needed = replayRebuildMaxBytes
	}
	return needed
}

// alignToEscBoundary returns a slice of `raw` starting at the first ESC
// (0x1B) byte. Tails of session logs and ring buffers can begin in the
// middle of a CSI parameter list (e.g. "5;3H" with the leading "ESC ["
// missing) — x/vt parses those orphan bytes as garbage text and renders
// them as a smudge of digits/punctuation at the top of the emulator.
// Skipping to the first ESC guarantees the parser sees a complete
// sequence from byte 0. If `raw` has no ESC at all, return it unchanged
// (likely plain text — safe to feed).
func alignToEscBoundary(raw []byte) []byte {
	if i := bytes.IndexByte(raw, 0x1B); i > 0 {
		return raw[i:]
	}
	return raw
}

// liveRebuildHistorySize bounds the on-disk session log read when the
// live emulator is rebuilt (dimension change). 8MB is wide enough to
// populate the emulator's 10K-line default scrollback for any realistic
// session while keeping the synchronous read cost on the main goroutine
// well under a frame budget (~20ms on a warm OS cache).
const liveRebuildHistorySize = 8 * 1024 * 1024

// readLiveRebuildHistory assembles bytes for rebuilding the live emulator
// after a dimension change. Reads up to 8MB from the on-disk session log
// (which holds the full history), then atomically pairs it with the ring
// buffer tail to cover any bytes written between the log read and our
// snapshot. Returns the merged bytes plus the monotonic `total` the caller
// should record as emuFedTotal after feeding (modulo the rare ring-lag
// gap noted below).
//
// Merge boundary: log covers [logSize-8MB, logSize); ring tail covers
// [ringTotal-256KB, ringTotal). Bytes [logSize, ringTotal) — the bytes
// readLoop wrote to the ring but hadn't yet flushed to the log — come
// from the ring tail's overflow region. If the log lags the ring by more
// than 256KB (rare; readLoop flushes synchronously per chunk), the gap
// is unrecoverable from local state; caller accepts and continues, the
// next incremental feed picks up from ringTotal.
//
// The returned bytes are NOT ESC-boundary aligned. Callers should pass
// through alignToEscBoundary before feeding x/vt.
func readLiveRebuildHistory(sess agentview.TerminalAdapter, taskID string) (raw []byte, total uint64) {
	if sess == nil {
		return nil, 0
	}
	var logRaw []byte
	var logSize int64
	if taskID != "" {
		logRaw, logSize = readLogTailForTask(taskID, liveRebuildHistorySize)
	}
	if len(logRaw) == 0 {
		// No log — fall back to ring buffer only. RecentOutputTailWithTotal
		// snapshots (bytes, total) atomically; using RecentOutput() + a
		// separate TotalWritten() call leaves a window where readLoop
		// advances total past the bytes we sampled.
		ring, ringTotal := sess.RecentOutputTailWithTotal(256 * 1024)
		return ring, ringTotal
	}
	ringTail, ringTotal := sess.RecentOutputTailWithTotal(256 * 1024)
	// ringTotal is monotonic and bounded by realistic session sizes (an
	// agent producing 8EiB of output before we attach is not a real case);
	// gosec G115 flags the conversion but the cast is safe and we want
	// signed arithmetic for the overflow subtraction below.
	overflow := int64(ringTotal) - logSize //nolint:gosec // see comment
	if overflow <= 0 {
		// Log already covers up to ringTotal (or further — readLoop wrote
		// to the log between our two reads). Either way, the log slice
		// alone gives the emulator a consistent view of bytes [start,
		// ringTotal]; record emuFedTotal = ringTotal so the next
		// incremental feed picks up new bytes only.
		return logRaw, ringTotal
	}
	overflowInt := int(overflow)
	if overflowInt > len(ringTail) {
		// Log lags ring by more than the ring's capacity — should not
		// happen under normal operation (readLoop flushes log writes
		// chunk-by-chunk). Best effort: concat both. The bytes
		// [logSize, ringTotal-len(ringTail)) are unrecoverable; emulator
		// will be missing those, but the next incremental feed brings
		// it back to live.
		overflowInt = len(ringTail)
	}
	extra := ringTail[len(ringTail)-overflowInt:]
	out := make([]byte, 0, len(logRaw)+len(extra))
	out = append(out, logRaw...)
	out = append(out, extra...)
	return out, ringTotal
}

// MouseHandler handles mouse clicks (focus switching) and scroll wheel.
func (tp *TerminalPane) MouseHandler() func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
	return tp.WrapMouseHandler(func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
		switch action {
		case tview.MouseLeftDown, tview.MouseLeftClick:
			setFocus(tp)
			if tp.OnClick != nil {
				tp.OnClick()
			}
			return true, nil
		case tview.MouseScrollUp:
			if tp.diffMode {
				tp.DiffScrollUp(mouseScrollStep)
			} else {
				tp.ScrollUp(mouseScrollStep)
			}
			return true, nil
		case tview.MouseScrollDown:
			if tp.diffMode {
				tp.DiffScrollDown(mouseScrollStep)
			} else {
				tp.ScrollDown(mouseScrollStep)
			}
			return true, nil
		}
		return false, nil
	})
}

// PasteHandler handles bracketed paste events, writing the entire pasted text
// to the PTY in a single call instead of character-by-character.
func (tp *TerminalPane) PasteHandler() func(pastedText string, setFocus func(p tview.Primitive)) {
	return tp.WrapPasteHandler(func(pastedText string, setFocus func(p tview.Primitive)) {
		tp.mu.Lock()
		sess := tp.session
		tp.mu.Unlock()
		if sess != nil && sess.Alive() {
			// Write the entire paste as a single PTY write, wrapped in
			// bracket paste sequences so the agent's readline sees it as
			// a paste (no per-character echo/processing).
			data := "\x1b[200~" + pastedText + "\x1b[201~"
			sess.WriteInput([]byte(data))
		}
	})
}

// --- Diff mode ---

// EnterDiffMode activates diff display in the center panel.
func (tp *TerminalPane) EnterDiffMode(diff, fileName string) {
	wasDiff := tp.diffMode
	tp.diffMode = true
	tp.diffScroll = 0
	tp.diffFile = fileName
	tp.diffParsed = gitutil.ParseUnifiedDiff(diff)
	tp.diffUnifiedLines = widget.BuildUnifiedDiffLines(tp.diffParsed, fileName)
	tp.diffSplitLines = nil
	tp.diffSplitWidth = 0
	if !wasDiff {
		// Branch change: PTY → diff (different rendering path, different
		// cell set). Skip when already in diff mode — diff → diff (different
		// file) keeps the same Draw branch and overwrites the same cell
		// positions cleanly, so no Sync needed.
		tp.notifyBranchChange()
	}
}

// ExitDiffMode returns to terminal display.
func (tp *TerminalPane) ExitDiffMode() {
	wasDiff := tp.diffMode
	tp.diffMode = false
	tp.diffParsed = gitutil.ParsedDiff{}
	tp.diffUnifiedLines = nil
	tp.diffSplitLines = nil
	tp.diffSplitWidth = 0
	tp.diffScroll = 0
	tp.diffFile = ""
	tp.paintCacheValid = false
	if wasDiff {
		// Branch change: diff → PTY. Cells from the diff render must be wiped
		// before the live emulator paints over them.
		tp.notifyBranchChange()
	}
}

// InDiffMode returns true if viewing a diff.
func (tp *TerminalPane) InDiffMode() bool { return tp.diffMode }

// ToggleDiffSplit switches between side-by-side and unified views.
func (tp *TerminalPane) ToggleDiffSplit() {
	tp.diffSplit = !tp.diffSplit
	tp.diffScroll = 0
	// Branch change: unified diff ↔ split diff paint completely different
	// columns/cells in the same rect. Toggle always changes state — no
	// idempotency guard needed (unlike setFocus / setSelectedPR / etc.).
	tp.notifyBranchChange()
}

// DiffScrollUp scrolls the diff view up.
func (tp *TerminalPane) DiffScrollUp(n int) {
	tp.diffScroll -= n
	if tp.diffScroll < 0 {
		tp.diffScroll = 0
	}
}

// DiffScrollDown scrolls the diff view down.
func (tp *TerminalPane) DiffScrollDown(n int) {
	tp.diffScroll += n
}

// --- Draw ---

func (tp *TerminalPane) Draw(screen tcell.Screen) {
	defer func() {
		if r := recover(); r != nil {
			uxlog.Log("[terminalpane] PANIC in Draw: %v\n%s", r, debug.Stack())
		}
	}()

	tp.Box.DrawForSubclass(screen, tp)
	x, y, width, height := tp.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	borderStyle := theme.StyleBorder
	if tp.focused {
		borderStyle = theme.StyleFocusedBorder
	}
	inner := widget.DrawBorderedPanel(screen, x, y, width, height, " Agent ", borderStyle)
	x, y, width, height = inner.X, inner.Y, inner.W, inner.H
	if width <= 0 || height <= 0 {
		return
	}

	if tp.diffMode {
		tp.renderDiff(screen, x, y, width, height)
		return
	}

	tp.mu.Lock()
	sess := tp.session
	pending := tp.pending
	// Compute PTY size from panel dimensions (main goroutine — safe to call GetInnerRect).
	wantCols := max(width, 20)
	wantRows := max(height, 5)
	if sess != nil && sess.Alive() {
		// Live session — resize PTY to match panel. The forceResync flag
		// reposts a resize even when dimensions match our tracked ptyCols,
		// which recovers from a PTY that's stuck at a stale width.
		sizeChanged := tp.ptyCols != wantCols || tp.ptyRows != wantRows
		if sizeChanged || tp.forceResync {
			if tp.forceResync {
				if sizeChanged {
					uxlog.Log("[terminalpane] force resync corrected %dx%d -> %dx%d", tp.ptyCols, tp.ptyRows, wantCols, wantRows)
				} else {
					uxlog.Log("[terminalpane] force resync at %dx%d (no delta)", wantCols, wantRows)
				}
			}
			tp.ptyCols = wantCols
			tp.ptyRows = wantRows
			tp.pendingResizeRows = uint16(wantRows)
			tp.pendingResizeCols = uint16(wantCols)
		}
		// Only clear when a live session actually consumed the flag.
		// Dead/nil-session Draws must leave it armed for the next live session.
		tp.forceResync = false
	}
	ptyCols := tp.ptyCols
	ptyRows := tp.ptyRows
	// For dead/replay sessions, always use current panel dimensions so
	// content auto-resizes with the window instead of staying at the
	// stale PTY size from when the session was alive.
	if sess == nil || !sess.Alive() {
		ptyCols = wantCols
		ptyRows = wantRows
	}
	tp.mu.Unlock()

	if sess == nil && !tp.HasContent() {
		if pending {
			// Show launch banner while worktree is being created.
			bannerH := widget.PendingBannerHeight()
			bannerY := y + (height-bannerH)/2
			if bannerY < y {
				bannerY = y
			}
			widget.DrawPendingBanner(screen, x, bannerY, width)
			return
		}
		msg := "No active session"
		if tp.taskID != "" {
			msg = "Session not running - press Enter to start"
		}
		midY := y + height/2
		midX := x + (width-len(msg))/2
		if midX < x {
			midX = x
		}
		widget.DrawText(screen, midX, midY, width, msg, theme.StyleDimmed)
		return
	}

	alive := false
	if sess != nil {
		alive = sess.Alive()
	}

	if ptyCols < 20 {
		ptyCols = width
	}
	if ptyRows < 5 {
		ptyRows = height
	}

	if tp.scrollOffset > 0 || !alive {
		// Replay/scroll path. The replay emulator fields are shared with
		// asyncReplayRebuild (background goroutine), so access under tp.mu.
		tp.mu.Lock()

		// Consume pending reset from async rebuild — these fields are owned
		// by the main goroutine (written by paintEmu without lock), so only
		// the main goroutine may write them.
		if tp.replayRebuildPending {
			tp.replayRebuildPending = false
			tp.anchorTotalLines = 0
			tp.paintCacheValid = false
		}

		// Fast path: if we have a cached replay emulator with matching
		// dimensions and the log file hasn't grown, skip file I/O entirely
		// and just repaint from the cache with the new scroll offset.
		cacheHit := false
		if tp.replayEmu != nil && tp.replayEmuCols == ptyCols && tp.replayEmuRows == ptyRows {
			cacheValid := false
			if alive && tp.scrollOffset > 0 {
				if tp.scrollOffset <= tp.replayEmuMaxScroll {
					cacheValid = true
				}
			} else if tp.taskID != "" {
				logSize := tp.statLogSize()
				if logSize > 0 && logSize == tp.replayEmuLogSize {
					cacheValid = true
				}
			} else if tp.replayEmuLogSize == 0 {
				if sess != nil {
					cacheValid = tp.replayEmuBytes == sess.TotalWritten()
				} else {
					cacheValid = tp.replayEmuBytes == uint64(len(tp.replayData))
				}
			}
			cacheHit = cacheValid
		}

		if cacheHit {
			// Snapshot replay emu fields under lock, then release before painting.
			emu := tp.replayEmu
			curVis := tp.replayEmuCursorVisible
			tp.mu.Unlock()

			if tp.paintCacheValid && tp.paintCacheX == x && tp.paintCacheY == y &&
				tp.paintCacheW == width && tp.paintCacheH == height &&
				tp.paintCacheScroll == tp.scrollOffset {
				tp.replayPaintCache(screen)
				return
			}
			tp.paintEmu(screen, x, y, width, height, emu, ptyCols, ptyRows, tp.scrollOffset == 0, curVis)
			return
		}

		// Slow path: cache miss — kick off async rebuild to avoid blocking
		// the main goroutine with file I/O (up to 8MB read) and VT emulation.
		// Skip the kick when we recently bailed out empty (defect 12) so we
		// don't churn goroutines while the placeholder is on screen.
		if !tp.replayBuilding && time.Now().After(tp.replayNoDataUntil) {
			tp.replayBuilding = true
			taskID := tp.taskID
			scrollOffset := tp.scrollOffset
			// Pass the previous build's first-byte offset so the next read
			// won't start LATER in the file when the agent has appended
			// new output between builds. Without this monotonic clamp, a
			// growing log forces the 8MB read window forward and the user's
			// deepest-cached scrollback jumps out from under their cursor.
			prevFirstByte := tp.replayEmuFirstByte
			var replayDataCopy []byte
			if len(tp.replayData) > 0 {
				replayDataCopy = tp.replayData
			}
			tp.mu.Unlock()

			// Grab ring buffer snapshot (may block briefly on session mutex,
			// but this is a memcpy of ≤256KB — acceptable on the main goroutine).
			var ringBuf []byte
			if sess != nil {
				ringBuf = sess.RecentOutput()
			}
			onDone := tp.OnNeedRedraw
			go tp.asyncReplayRebuild(taskID, scrollOffset, height, ptyCols, ptyRows, ringBuf, replayDataCopy, prevFirstByte, onDone)

			tp.mu.Lock()
		}

		// While rebuild is in flight, paint stale replay emulator if available.
		staleEmu := tp.replayEmu
		staleCurVis := tp.replayEmuCursorVisible
		tp.mu.Unlock()

		// Pick the best available emulator to show while the replay builds.
		// Prefer a stale replay emulator (from a previous scroll), but fall
		// back to the live emulator which has 10K lines of scrollback — enough
		// for instant scroll-up response while the full 50K replay builds.
		fallbackEmu := staleEmu
		fallbackCurVis := staleCurVis
		if fallbackEmu == nil && tp.emu != nil { // tp.emu is main-goroutine-owned; no lock needed
			fallbackEmu = tp.emu
			fallbackCurVis = tp.cursorVisible
		}

		if fallbackEmu != nil {
			// Save scrollOffset — the fallback emulator may clamp it to its
			// (smaller) maxScroll, which would cause a position jump when the
			// fresh emulator arrives with more scrollback.
			savedScroll := tp.scrollOffset
			savedAnchor := tp.anchorTotalLines
			tp.paintEmu(screen, x, y, width, height, fallbackEmu, ptyCols, ptyRows, tp.scrollOffset == 0, fallbackCurVis)
			tp.scrollOffset = savedScroll
			tp.anchorTotalLines = savedAnchor
			return
		}
		if sess != nil {
			msg := "Waiting for output..."
			widget.DrawText(screen, x+(width-len(msg))/2, y+height/2, width, msg, theme.StyleDimmed)
		}
		return
	} else {
		// Live follow-tail mode: incremental feed.
		// renderLive fetches the buffer internally, only when new bytes
		// have arrived — avoids a 256KB ring buffer copy on every draw.
		tp.renderLive(screen, x, y, width, height, ptyCols, ptyRows)
	}
}

// replayRebuildMaxBytes caps the on-disk read for a single replay rebuild.
// Defect 5 monotonic-firstByteOffset clamp can keep growing the read window
// as the agent writes more output; without an upper bound, an agent that
// runs for hours would force a multi-hundred-MB synchronous read on each
// scroll-past-maxScroll. 64MB covers roughly 200K lines of dense output at
// typical widths — far beyond what a user scrolls through interactively.
const replayRebuildMaxBytes = 64 * 1024 * 1024

// asyncReplayRebuild performs the heavy replay emulator build (file I/O + VT emulation)
// on a background goroutine. When done, it stores the result and triggers a redraw.
// This prevents the main goroutine from blocking on multi-MB file reads and VT processing,
// which was the primary cause of UI freezes with large session logs (20MB+).
//
// `prevFirstByte` is the file offset of the previous build's first byte (0
// if no prior build). When non-zero, the read window grows to start at or
// before that offset so the user-visible scrollback doesn't slide forward
// as the agent appends new bytes to the log between builds (defect 5).
func (tp *TerminalPane) asyncReplayRebuild(taskID string, scrollOffset, viewportHeight, ptyCols, ptyRows int, ringBuf, replayDataCopy []byte, prevFirstByte int64, onDone func()) {
	defer func() {
		if r := recover(); r != nil {
			uxlog.Log("[terminalpane] PANIC in asyncReplayRebuild: %v\n%s", r, debug.Stack())
		}
	}()

	var raw []byte
	var logSize int64

	if taskID != "" {
		needed := replayRebuildReadSize(taskID, scrollOffset, viewportHeight, ptyCols, prevFirstByte)
		// Use taskID parameter (not tp.taskID) to avoid data race with SetTaskID.
		raw, logSize = readLogTailForTask(taskID, needed)
	}
	if len(raw) == 0 {
		// Fallback: ring buffer snapshot or cached replay data.
		if len(ringBuf) > 0 {
			raw = ringBuf
		} else if len(replayDataCopy) > 0 {
			raw = replayDataCopy
		}
	}

	if len(raw) == 0 {
		// Nothing to build. Set a short cooldown so the next Draw doesn't
		// immediately re-kick another goroutine (defect 12) — the slow path
		// gates on (now > replayNoDataUntil). Cooldown is brief so a session
		// that's about to start producing output isn't blocked for long.
		// Skip onDone: there's nothing new to repaint, and calling it would
		// schedule another Draw that just re-enters this no-op path.
		tp.mu.Lock()
		tp.replayBuilding = false
		tp.replayNoDataUntil = time.Now().Add(replayNoDataCooldown)
		tp.mu.Unlock()
		return
	}

	// Build the replay emulator (the expensive part — VT emulation of up to 8MB).
	cursorVisible := true
	emu := tp.newTrackedReplayEmulatorWithCallback(ptyCols, ptyRows, func(visible bool) {
		cursorVisible = visible
	})
	// Tail slices begin at arbitrary byte positions — see alignToEscBoundary
	// (defect 4). Without this, partial CSI prefixes show up as orphan
	// digits/punctuation at the top of the scrollback emulator.
	_, _ = SafeEmuWrite(emu, alignToEscBoundary(raw))

	// Compute max scroll from emulator's scrollback capacity.
	sbLen := emu.ScrollbackLen()
	lastRow := FindLastContentRowEmu(emu, ptyCols, ptyRows)
	if cursorVisible {
		cur := emu.CursorPosition()
		if cur.Y > lastRow {
			lastRow = cur.Y
		}
	}
	var totalLines int
	if sbLen > 0 {
		totalLines = sbLen + lastRow + 1
	} else {
		firstRow := FindFirstContentRowEmu(emu, ptyCols, lastRow)
		totalLines = lastRow - firstRow + 1
	}
	maxScroll := totalLines - ptyRows
	if maxScroll < 0 {
		maxScroll = 0
	}

	// Store results — accessed from the main goroutine during Draw().
	// Use tp.mu to safely swap in the new emulator.
	// anchorTotalLines and paintCacheValid are owned by the main goroutine
	// (written by paintEmu without lock), so we use replayRebuildPending
	// to signal that the main goroutine should reset them.
	tp.mu.Lock()
	tp.replayEmu = emu
	tp.replayEmuCols = ptyCols
	tp.replayEmuRows = ptyRows
	tp.replayEmuLogSize = logSize
	tp.replayEmuBytes = uint64(len(raw))
	tp.replayEmuCursorVisible = cursorVisible
	tp.replayEmuMaxScroll = maxScroll
	// firstByteOffset = logSize - bytes-fed. When we fed from the log,
	// that's the position of the first byte in `raw`; when we fell back
	// to the ring buffer (logSize=0), no monotonic clamp is possible and
	// we leave the field at 0 (any prior log-backed build is invalidated
	// by the dimension or session changes that drove this path).
	if logSize > 0 {
		tp.replayEmuFirstByte = logSize - int64(len(raw))
	} else {
		tp.replayEmuFirstByte = 0
	}
	tp.replayRebuildPending = true
	tp.replayBuilding = false
	tp.mu.Unlock()

	// Branch change: the fallback emulator (or "Waiting for output..." text)
	// painted on prior frames is being replaced by a fresh 50K-line replay
	// emulator. Different content fills the same rect — fire OnBranchChange
	// so afterDraw runs Sync, wiping any stale fallback cells. notify is
	// safe to call from this background goroutine: the callback is set once
	// in buildUI and never reassigned (no race), and forceRedraw uses an
	// atomic flag (no cross-goroutine ordering concerns).
	tp.notifyBranchChange()

	// Trigger a redraw so the next Draw() picks up the new emulator.
	if onDone != nil {
		onDone()
	}
}

// renderLive feeds only new bytes to persistent x/vt emulator and paints cells to tcell.
// It fetches the ring buffer only when new output has arrived, avoiding a 256KB copy
// on redraws triggered by keystrokes or timer ticks with no new output.
func (tp *TerminalPane) renderLive(screen tcell.Screen, x, y, w, h int, ptyCols, ptyRows int) {
	tp.mu.Lock()
	sess := tp.session
	tp.mu.Unlock()

	totalWritten := uint64(0)
	if sess != nil {
		totalWritten = sess.TotalWritten()
	}

	needRebuild := tp.emu == nil || tp.emuCols != ptyCols || tp.emuRows != ptyRows
	if needRebuild {
		tp.emu = tp.newTrackedEmulator(ptyCols, ptyRows)
		tp.emuFedTotal = 0
		tp.emuCols = ptyCols
		tp.emuRows = ptyRows
		// Branch change: dropping the old emulator means tcell's per-cell
		// diff cannot vouch for cells the new emu hasn't drawn over yet.
		// Invalidate the paint cache so the next paintEmu rebuilds from
		// scratch instead of replaying stale SetContent calls.
		tp.paintCacheValid = false
	}

	newBytes := totalWritten - tp.emuFedTotal

	if newBytes > 0 || needRebuild {
		var raw []byte
		if sess != nil {
			raw = sess.RecentOutput()
		}
		// "Full replay" is required when the emulator was just rebuilt
		// (dimension change) OR the ring wrapped past our last cursor —
		// in either case the incremental tail in `raw` no longer aligns
		// with what the emulator already parsed. Use the on-disk session
		// log (up to 8MB) to give the new emu meaningful history instead
		// of the ring's last 256KB sliver; without this, earlier status
		// bars and framing the old emu had absorbed get re-emitted by
		// the agent, producing stacked-status-bar artifacts at the
		// bottom of the pane (defect 3).
		fullReplay := needRebuild || newBytes > uint64(len(raw))
		if fullReplay {
			if !needRebuild {
				tp.emu = tp.newTrackedEmulator(ptyCols, ptyRows)
				tp.paintCacheValid = false
			}
			history, finalTotal := readLiveRebuildHistory(sess, tp.taskID)
			if len(history) == 0 {
				if tp.emuFedTotal == 0 {
					msg := "Waiting for output..."
					widget.DrawText(screen, x+(w-len(msg))/2, y+h/2, w, msg, theme.StyleDimmed)
					return
				}
				// Emulator already has content — repaint below without
				// advancing emuFedTotal (no bytes were fed).
			} else {
				// alignToEscBoundary skips any partial CSI/OSC prefix
				// the log/ring tail may have started mid-sequence at,
				// which x/vt would otherwise render as a smudge of
				// orphan parameter bytes at the top of the screen
				// (defect 4).
				_, _ = SafeEmuWrite(tp.emu, alignToEscBoundary(history))
				tp.emuFedTotal = finalTotal
			}
		} else if len(raw) > 0 {
			// Incremental feed: the delta is contiguous with what the
			// emulator has already parsed, so no ESC realignment is
			// needed (parser state is already at the right boundary).
			_, _ = SafeEmuWrite(tp.emu, raw[len(raw)-int(newBytes):])
			tp.emuFedTotal = totalWritten
		} else if tp.emuFedTotal == 0 {
			msg := "Waiting for output..."
			widget.DrawText(screen, x+(w-len(msg))/2, y+h/2, w, msg, theme.StyleDimmed)
			return
		}
	} else if tp.emuFedTotal == 0 {
		// No data has ever arrived.
		msg := "Waiting for output..."
		widget.DrawText(screen, x+(w-len(msg))/2, y+h/2, w, msg, theme.StyleDimmed)
		return
	} else if tp.paintCacheValid && tp.paintCacheX == x && tp.paintCacheY == y &&
		tp.paintCacheW == w && tp.paintCacheH == h {
		// Fast path: no new bytes, no rebuild, viewport unchanged.
		// Replay cached SetContent calls — skips all emulator access,
		// mutex ops, allocations, and style conversion.
		tp.replayPaintCache(screen)
		return
	}
	// Cache miss or stale — fall through to full paintEmu.

	tp.paintEmu(screen, x, y, w, h, tp.emu, ptyCols, ptyRows, true, tp.cursorVisible)
}

// paintEmu renders x/vt emulator cells to the tcell screen with content trimming and scrollback.
func (tp *TerminalPane) paintEmu(screen tcell.Screen, x, y, w, h int, emu *xvt.SafeEmulator, emuCols, emuRows int, showCursor, cursorVisible bool) {
	cur := emu.CursorPosition()
	sbLen := emu.ScrollbackLen()
	// Find content bounds in the main screen area.
	lastContentRow := FindLastContentRowEmu(emu, emuCols, emuRows)
	// Only extend content area to include cursor when it's visible.
	// Without this guard, a hidden cursor at (0, bottom) inflates the
	// content region with empty rows, causing a phantom cursor artifact.
	if cursorVisible && cur.Y > lastContentRow {
		lastContentRow = cur.Y
	}

	// Total addressable lines = scrollback + visible content.
	totalLines := sbLen + lastContentRow + 1
	firstContentRow := 0
	if sbLen == 0 {
		firstContentRow = FindFirstContentRowEmu(emu, emuCols, lastContentRow)
		totalLines = lastContentRow - firstContentRow + 1
	}

	if totalLines <= 0 {
		return
	}

	// Anchor-lock: when scrolled up and new lines arrive, bump scrollOffset
	// so the viewed content stays pinned (tmux-style).
	if tp.scrollOffset > 0 && tp.anchorTotalLines > 0 && totalLines > tp.anchorTotalLines {
		delta := totalLines - tp.anchorTotalLines
		tp.scrollOffset += delta
	}
	tp.anchorTotalLines = totalLines

	// Clamp scroll offset.
	maxScroll := totalLines - h
	if maxScroll < 0 {
		maxScroll = 0
	}
	if tp.scrollOffset > maxScroll {
		tp.scrollOffset = maxScroll
	}

	renderCols := min(emuCols, w)

	// Pre-allocate cache for this paint. Reuse backing array when possible.
	// `+ scrollIndicatorCells` reserves room for the [SCROLL] indicator the
	// scroll-mode branch appends after the row loop (defect 2 — otherwise
	// every scroll-mode frame triggers a heap realloc past the h*renderCols
	// preallocation).
	cacheSize := h*renderCols + scrollIndicatorCells
	if cap(tp.paintCacheCells) >= cacheSize {
		tp.paintCacheCells = tp.paintCacheCells[:0]
	} else {
		tp.paintCacheCells = make([]cachedCell, 0, cacheSize)
	}

	// Render visible rows. Row index is in "unified" space:
	// rows 0..sbLen-1 are scrollback, rows sbLen..sbLen+emuRows-1 are main screen.
	endLine := totalLines - 1 - tp.scrollOffset
	startLine := endLine - h + 1
	if startLine < 0 {
		startLine = 0
	}

	contentRows := 0
	for screenRow := 0; screenRow < h; screenRow++ {
		lineIdx := startLine + screenRow
		if lineIdx > endLine {
			break
		}
		contentRows = screenRow + 1

		for col := 0; col < renderCols; col++ {
			var cell *uv.Cell
			isMainScreen := false
			mainRow := 0

			if sbLen > 0 && lineIdx < sbLen {
				// Scrollback region.
				cell = emu.ScrollbackCellAt(col, lineIdx)
			} else {
				// Main screen region.
				if sbLen > 0 {
					mainRow = lineIdx - sbLen
				} else {
					mainRow = firstContentRow + lineIdx
				}
				isMainScreen = true
				cell = emu.CellAt(col, mainRow)
			}

			ch := ' '
			style := tcell.StyleDefault
			if cell != nil {
				if cell.Content != "" {
					runes := []rune(cell.Content)
					if len(runes) > 0 {
						ch = runes[0]
					}
				}
				style = UvCellToTcellStyle(cell)
			}

			// Match the emulator's cursor visibility instead of forcing an Argus-owned cursor.
			if showCursor && cursorVisible && isMainScreen && mainRow == cur.Y && col == cur.X {
				style = tcell.StyleDefault.Foreground(cursorFG).Background(cursorBG)
			}

			sx, sy := x+col, y+screenRow
			screen.SetContent(sx, sy, ch, nil, style)
			tp.paintCacheCells = append(tp.paintCacheCells, cachedCell{x: sx, y: sy, ch: ch, style: style})
		}
	}

	// Blank any viewport rows below the last content row. paintEmu may be
	// re-entered on a frame whose content is shorter than the previous
	// frame's (e.g., agent cleared rows, scrollback shrank, dimension
	// change reduced content height); without an explicit blank, tcell's
	// per-cell diff retains the stale cells from the prior paint and the
	// paint cache replay path serves them on the next keystroke redraw —
	// the user sees ghost lines at the bottom of the pane (defect 6).
	for screenRow := contentRows; screenRow < h; screenRow++ {
		for col := 0; col < renderCols; col++ {
			sx, sy := x+col, y+screenRow
			screen.SetContent(sx, sy, ' ', nil, tcell.StyleDefault)
			tp.paintCacheCells = append(tp.paintCacheCells, cachedCell{x: sx, y: sy, ch: ' ', style: tcell.StyleDefault})
		}
	}

	// Scroll indicator.
	if tp.scrollOffset > 0 {
		indicator := "   [SCROLL]   "
		style := tcell.StyleDefault.Foreground(tcell.PaletteColor(214)).Bold(true)
		midX := x + (w-len(indicator))/2
		if midX < x {
			midX = x
		}
		for i, r := range indicator {
			if midX+i < x+w {
				sx, sy := midX+i, y
				screen.SetContent(sx, sy, r, nil, style)
				tp.paintCacheCells = append(tp.paintCacheCells, cachedCell{x: sx, y: sy, ch: r, style: style})
			}
		}
	}

	// Mark cache as valid for this viewport.
	tp.paintCacheX = x
	tp.paintCacheY = y
	tp.paintCacheW = w
	tp.paintCacheH = h
	tp.paintCacheScroll = tp.scrollOffset
	tp.paintCacheValid = true
}

// replayPaintCache writes the cached cells to the screen without touching
// the emulator. This is the fast path for keystroke-triggered redraws where
// no new PTY output has arrived.
func (tp *TerminalPane) replayPaintCache(screen tcell.Screen) {
	for _, c := range tp.paintCacheCells {
		screen.SetContent(c.x, c.y, c.ch, nil, c.style)
	}
}

func (tp *TerminalPane) newTrackedEmulator(cols, rows int) *xvt.SafeEmulator {
	return tp.newTrackedEmulatorWithCallback(cols, rows, func(visible bool) {
		tp.cursorVisible = visible
	})
}

func (tp *TerminalPane) newTrackedEmulatorWithCallback(cols, rows int, onCursorVisible func(bool)) *xvt.SafeEmulator {
	emu := NewDrainedEmulator(cols, rows)
	if onCursorVisible != nil {
		emu.Emulator.SetCallbacks(xvt.Callbacks{
			CursorVisibility: onCursorVisible,
		})
	}
	// Default cursor to hidden — agents (Claude Code, Codex) hide the hardware
	// cursor via \e[?25l. When the ring buffer wraps or the emulator is rebuilt,
	// the hide sequence may no longer be in the replay data. Defaulting to false
	// prevents a stale cursor from appearing (typically bottom-left) until the
	// emulator processes an explicit \e[?25h show-cursor sequence.
	if onCursorVisible != nil {
		onCursorVisible(false)
	}
	return emu
}

// newTrackedReplayEmulatorWithCallback creates a replay emulator with a large
// scrollback buffer (50K lines) for scrollback browsing in long sessions.
func (tp *TerminalPane) newTrackedReplayEmulatorWithCallback(cols, rows int, onCursorVisible func(bool)) *xvt.SafeEmulator {
	emu := newDrainedReplayEmulator(cols, rows)
	if onCursorVisible != nil {
		emu.Emulator.SetCallbacks(xvt.Callbacks{
			CursorVisibility: onCursorVisible,
		})
		// Default cursor to hidden — same rationale as newTrackedEmulatorWithCallback.
		onCursorVisible(false)
	}
	return emu
}

// --- Diff rendering ---

func (tp *TerminalPane) renderDiff(screen tcell.Screen, x, y, w, h int) {
	var lines []widget.RenderedDiffLine
	if tp.diffSplit {
		// Rebuild side-by-side lines if width changed.
		if tp.diffSplitWidth != w || tp.diffSplitLines == nil {
			tp.diffSplitLines = widget.BuildSideBySideDiffLines(tp.diffParsed, tp.diffFile, w)
			tp.diffSplitWidth = w
		}
		lines = tp.diffSplitLines
	} else {
		lines = tp.diffUnifiedLines
	}

	if len(lines) == 0 {
		msg := "No diff available"
		widget.DrawText(screen, x+(w-len(msg))/2, y+h/2, w, msg, theme.StyleDimmed)
		return
	}

	// Header
	mode := "unified"
	if tp.diffSplit {
		mode = "split"
	}
	headerText := " " + tp.diffFile + "  [" + mode + "]"
	headerStyle := tcell.StyleDefault.Foreground(theme.ColorTitle).Bold(true)
	for i, r := range headerText {
		if i >= w {
			break
		}
		screen.SetContent(x+i, y, r, nil, headerStyle)
	}

	visibleH := h - 1
	maxScroll := len(lines) - visibleH
	if maxScroll < 0 {
		maxScroll = 0
	}
	if tp.diffScroll > maxScroll {
		tp.diffScroll = maxScroll
	}

	for i := range visibleH {
		lineIdx := tp.diffScroll + i
		if lineIdx >= len(lines) {
			break
		}
		widget.DrawStyledLine(screen, x, y+1+i, w, lines[lineIdx].Cells)
	}
}

// --- x/vt → tcell helpers ---

// uvColorToTcell converts an image/color.Color (as used by x/vt) to a tcell.Color.
// nil → tcell.ColorDefault (inherits terminal theme).
// ansi.BasicColor/IndexedColor → tcell.PaletteColor for exact palette match.
// Everything else → tcell.FromImageColor for RGB conversion.
func uvColorToTcell(c color.Color) tcell.Color {
	if c == nil {
		return tcell.ColorDefault
	}
	switch v := c.(type) {
	case ansi.BasicColor:
		return tcell.PaletteColor(int(v))
	case ansi.IndexedColor:
		return tcell.PaletteColor(int(v))
	default:
		return tcell.FromImageColor(c)
	}
}

// UvCellToTcellStyle converts a *uv.Cell to a tcell.Style.
func UvCellToTcellStyle(cell *uv.Cell) tcell.Style {
	if cell == nil {
		return tcell.StyleDefault
	}
	style := tcell.StyleDefault.
		Foreground(uvColorToTcell(cell.Style.Fg)).
		Background(uvColorToTcell(cell.Style.Bg))

	attrs := cell.Style.Attrs
	if attrs&uv.AttrBold != 0 {
		style = style.Bold(true)
	}
	if attrs&uv.AttrFaint != 0 {
		style = style.Dim(true)
	}
	if attrs&uv.AttrItalic != 0 {
		style = style.Italic(true)
	}
	if attrs&uv.AttrBlink != 0 {
		style = style.Blink(true)
	}
	if attrs&uv.AttrReverse != 0 {
		style = style.Reverse(true)
	}
	if attrs&uv.AttrStrikethrough != 0 {
		style = style.StrikeThrough(true)
	}
	// Underline styles + color.
	if ul := cell.Style.Underline; ul != 0 {
		var ulStyle tcell.UnderlineStyle
		switch ul {
		case ansi.UnderlineSingle:
			ulStyle = tcell.UnderlineStyleSolid
		case ansi.UnderlineDouble:
			ulStyle = tcell.UnderlineStyleDouble
		case ansi.UnderlineCurly:
			ulStyle = tcell.UnderlineStyleCurly
		case ansi.UnderlineDotted:
			ulStyle = tcell.UnderlineStyleDotted
		case ansi.UnderlineDashed:
			ulStyle = tcell.UnderlineStyleDashed
		default:
			ulStyle = tcell.UnderlineStyleSolid
		}
		if cell.Style.UnderlineColor != nil {
			style = style.Underline(ulStyle, uvColorToTcell(cell.Style.UnderlineColor))
		} else {
			style = style.Underline(ulStyle)
		}
	}
	// Hyperlinks (OSC 8).
	if cell.Link.URL != "" {
		style = style.Url(cell.Link.URL)
	}
	return style
}

// FindLastContentRowEmu scans backwards to find the last row with visible content.
func FindLastContentRowEmu(emu *xvt.SafeEmulator, cols, rows int) int {
	for row := rows - 1; row >= 0; row-- {
		if rowHasContentEmu(emu, row, cols) {
			return row
		}
	}
	return -1
}

// FindFirstContentRowEmu scans forward to find the first row with content.
func FindFirstContentRowEmu(emu *xvt.SafeEmulator, cols, maxRow int) int {
	for row := 0; row <= maxRow; row++ {
		if rowHasContentEmu(emu, row, cols) {
			return row
		}
	}
	return 0
}

// cellHasContent returns true if a single cell has visible content or styling.
func cellHasContent(cell *uv.Cell) bool {
	if cell == nil {
		return false
	}
	if cell.Content != "" && cell.Content != " " {
		return true
	}
	return cell.Style.Fg != nil || cell.Style.Bg != nil || cell.Style.Attrs != 0
}

// rowHasContentEmu returns true if any cell in the row has visible content.
// Checks column 0 first (most terminal output starts there) for a fast exit.
func rowHasContentEmu(emu *xvt.SafeEmulator, row, cols int) bool {
	// Fast check: column 0 has content for ~90% of non-empty rows.
	if cellHasContent(emu.CellAt(0, row)) {
		return true
	}
	for x := 1; x < cols; x++ {
		if cellHasContent(emu.CellAt(x, row)) {
			return true
		}
	}
	return false
}
