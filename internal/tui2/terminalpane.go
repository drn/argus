package tui2

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sync"

	"image/color"

	"github.com/charmbracelet/x/ansi"
	xvt "github.com/charmbracelet/x/vt"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/app/agentview"
	"github.com/drn/argus/internal/gitutil"
	"github.com/drn/argus/internal/uxlog"
)

// newDrainedEmulator creates an x/vt SafeEmulator with a goroutine that drains
// the response pipe. x/vt uses io.Pipe() internally — when the emulator
// processes terminal query sequences (DA1, DA2, DSR, etc.), it writes responses
// to pw which blocks until pr is read. Without draining, Write() hangs
// indefinitely on any input containing these sequences. The drain goroutine
// exits when the emulator is closed or garbage collected.
func newDrainedEmulator(cols, rows int) *xvt.SafeEmulator {
	emu := xvt.NewSafeEmulator(cols, rows)
	go io.Copy(io.Discard, emu) //nolint:errcheck
	return emu
}

// safeEmuWrite writes data to an x/vt emulator, recovering from panics caused
// by upstream bugs (e.g., InsertLineArea index-out-of-range when replay data
// contains cursor positions or scroll regions from a larger terminal).
func safeEmuWrite(emu *xvt.SafeEmulator, data []byte) (n int, err error) {
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

// TerminalPane renders PTY output natively to a tcell screen via x/vt.
// No ANSI string intermediary — x/vt cells map directly to tcell cells.
// No activeInputBG or findInputRow — the native surface shows upstream
// PTY output without Argus-injected highlights.
type TerminalPane struct {
	*tview.Box
	mu      sync.Mutex
	session agentview.TerminalAdapter
	taskID  string
	taskPR  string
	focused bool

	// Persistent x/vt emulator for live incremental rendering.
	emu         *xvt.SafeEmulator
	emuFedTotal uint64
	emuCols     int
	emuRows     int
	cursorVisible bool

	// Cached PTY size — set from Draw() (main goroutine), read by sync goroutine.
	ptyCols int
	ptyRows int

	// Scrollback.
	scrollOffset int

	// Anchor-lock: track total lines so scrollOffset stays pinned when new output arrives.
	anchorTotalLines int // total lines when scrollOffset was last set

	// Replay emulator cache: reuse when only scroll changes (no new bytes).
	replayEmu          *xvt.SafeEmulator
	replayEmuBytes     uint64 // TotalWritten when replayEmu was built
	replayEmuCols      int
	replayEmuRows      int
	replayEmuLogSize       int64 // log file size when replayEmu was built (for log-backed scroll)
	replayEmuCursorVisible bool  // cached cursor visibility from replay emulator

	// Replay data for finished sessions (loaded from session log file).
	replayData []byte

	// Diff mode.
	diffMode         bool
	diffParsed       gitutil.ParsedDiff
	diffUnifiedLines []renderedDiffLine
	diffSplitLines   []renderedDiffLine
	diffSplitWidth   int // width used to build split lines (invalidate on resize)
	diffSplit        bool
	diffScroll       int
	diffFile         string

	// pendingResize is set by Draw() when panel dimensions differ from PTY.
	// The tick goroutine checks this and performs the resize RPC.
	pendingResizeRows uint16
	pendingResizeCols uint16

	// OnClick is called when the user clicks on the terminal pane.
	// The app wires this to switch agentFocus back to the terminal.
	OnClick func()
}

// mouseScrollStep is the number of lines scrolled per mouse wheel tick.
const mouseScrollStep = 3

// NewTerminalPane creates a native terminal rendering pane.
func NewTerminalPane() *TerminalPane {
	return &TerminalPane{
		Box: tview.NewBox(),
	}
}

// SetSession attaches a live session. Resets emulator state only when the
// session pointer actually changes — the tick calls this every second with
// the same session, and resetting the emulator each time would destroy
// incremental rendering state.
func (tp *TerminalPane) SetSession(sess agentview.TerminalAdapter) {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	if tp.session == sess {
		return // same session, skip reset
	}
	if sess != nil {
		uxlog.Log("[terminalpane] SetSession: sess=%p totalWritten=%d", sess, sess.TotalWritten())
	} else {
		uxlog.Log("[terminalpane] SetSession: nil")
	}
	tp.session = sess
	tp.emu = nil
	tp.emuFedTotal = 0
	tp.scrollOffset = 0
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
}

// Session returns the current session (thread-safe).
func (tp *TerminalPane) Session() agentview.TerminalAdapter {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	return tp.session
}

// SetTaskID sets the current task ID and loads session log if no live session.
func (tp *TerminalPane) SetTaskID(id string) {
	tp.taskID = id
	tp.replayData = nil
	if id != "" && tp.Session() == nil {
		tp.loadSessionLog(id)
	}
}

// loadSessionLog reads the session log file for finished-session replay.
func (tp *TerminalPane) loadSessionLog(taskID string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	logPath := filepath.Join(home, ".argus", "sessions", taskID+".log")
	data, err := os.ReadFile(logPath)
	if err != nil || len(data) == 0 {
		return
	}
	uxlog.Log("[tui2] loaded session log for %s (%d bytes)", taskID, len(data))
	tp.replayData = data
}

// SetPRURL sets the PR URL for the current task.
func (tp *TerminalPane) SetPRURL(url string) {
	tp.taskPR = url
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

// ResetVT clears all terminal state (on resize or task switch).
func (tp *TerminalPane) ResetVT() {
	tp.emu = nil
	tp.emuFedTotal = 0
	tp.scrollOffset = 0
	tp.anchorTotalLines = 0
	tp.replayEmu = nil
	tp.replayEmuBytes = 0
	tp.replayEmuLogSize = 0
	tp.replayData = nil
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

func (tp *TerminalPane) ScrollUp(n int)    { tp.scrollOffset += n }
func (tp *TerminalPane) ScrollOffset() int  { return tp.scrollOffset }
func (tp *TerminalPane) ResetScroll()       { tp.scrollOffset = 0; tp.anchorTotalLines = 0 }

func (tp *TerminalPane) ScrollDown(n int) {
	tp.scrollOffset -= n
	if tp.scrollOffset < 0 {
		tp.scrollOffset = 0
		tp.anchorTotalLines = 0
	}
}

// readLogTail reads the last `size` bytes from the session log file.
// Returns nil if the file doesn't exist or is empty. The log file is
// concurrently appended by readLoop; using Open+Seek+Read (not ReadFile)
// avoids reading partial writes at EOF. vt10x handles truncated escape
// sequences gracefully.
func (tp *TerminalPane) readLogTail(size int64) ([]byte, int64) {
	logPath := agent.SessionLogPath(tp.taskID)
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
	if err != nil && err != io.EOF {
		return nil, 0
	}
	return buf[:n], fileSize
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

// --- Diff mode ---

// EnterDiffMode activates diff display in the center panel.
func (tp *TerminalPane) EnterDiffMode(diff, fileName string) {
	tp.diffMode = true
	tp.diffScroll = 0
	tp.diffFile = fileName
	tp.diffParsed = gitutil.ParseUnifiedDiff(diff)
	tp.diffUnifiedLines = buildUnifiedDiffLines(tp.diffParsed, fileName)
	tp.diffSplitLines = nil
	tp.diffSplitWidth = 0
}

// ExitDiffMode returns to terminal display.
func (tp *TerminalPane) ExitDiffMode() {
	tp.diffMode = false
	tp.diffParsed = gitutil.ParsedDiff{}
	tp.diffUnifiedLines = nil
	tp.diffSplitLines = nil
	tp.diffSplitWidth = 0
	tp.diffScroll = 0
	tp.diffFile = ""
}

// InDiffMode returns true if viewing a diff.
func (tp *TerminalPane) InDiffMode() bool { return tp.diffMode }

// ToggleDiffSplit switches between side-by-side and unified views.
func (tp *TerminalPane) ToggleDiffSplit() {
	tp.diffSplit = !tp.diffSplit
	tp.diffScroll = 0
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

// --- PR ---

func (tp *TerminalPane) OpenPR() {
	if tp.taskPR == "" {
		return
	}
	exec.Command("open", tp.taskPR).Start() //nolint:errcheck
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

	borderStyle := StyleBorder
	if tp.focused {
		borderStyle = StyleFocusedBorder
	}
	inner := drawBorderedPanel(screen, x, y, width, height, " Agent ", borderStyle)
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
	// Compute PTY size from panel dimensions (main goroutine — safe to call GetInnerRect).
	wantCols := max(width, 20)
	wantRows := max(height, 5)
	if sess != nil && sess.Alive() {
		// Live session — resize PTY to match panel.
		if tp.ptyCols != wantCols || tp.ptyRows != wantRows {
			tp.ptyCols = wantCols
			tp.ptyRows = wantRows
			tp.pendingResizeRows = uint16(wantRows)
			tp.pendingResizeCols = uint16(wantCols)
		}
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
		msg := "No active session"
		if tp.taskID != "" {
			msg = "Session not running - press Enter to start"
		}
		midY := y + height/2
		midX := x + (width-len(msg))/2
		if midX < x {
			midX = x
		}
		drawText(screen, midX, midY, width, msg, StyleDimmed)
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
		// Scrollback or finished session: use log file for full history,
		// falling back to ring buffer or cached replay data.
		var raw []byte
		var logSize int64

		if tp.taskID != "" {
			// Estimate bytes needed: (scrollOffset + viewport) * cols * 3 for ANSI overhead.
			// Minimum 1MB to avoid re-reads on small scrolls.
			needed := int64(tp.scrollOffset+height) * int64(ptyCols) * 3
			if needed < 1024*1024 {
				needed = 1024 * 1024
			}
			raw, logSize = tp.readLogTail(needed)
		}
		if len(raw) == 0 {
			// Fallback: ring buffer or cached replay data.
			if sess != nil {
				raw = sess.RecentOutput()
			} else if len(tp.replayData) > 0 {
				raw = tp.replayData
			}
		}
		if len(raw) == 0 {
			if sess != nil {
				msg := "Waiting for output..."
				drawText(screen, x+(width-len(msg))/2, y+height/2, width, msg, StyleDimmed)
			}
			return
		}
		tp.renderReplay(screen, x, y, width, height, raw, logSize, ptyCols, ptyRows)
	} else {
		// Live follow-tail mode: incremental feed.
		var raw []byte
		if sess != nil {
			raw = sess.RecentOutput()
		}
		if len(raw) == 0 {
			msg := "Waiting for output..."
			drawText(screen, x+(width-len(msg))/2, y+height/2, width, msg, StyleDimmed)
			return
		}
		tp.renderLive(screen, x, y, width, height, raw, ptyCols, ptyRows)
	}
}

// renderLive feeds only new bytes to persistent x/vt emulator and paints cells to tcell.
func (tp *TerminalPane) renderLive(screen tcell.Screen, x, y, w, h int, raw []byte, ptyCols, ptyRows int) {
	tp.mu.Lock()
	sess := tp.session
	tp.mu.Unlock()

	totalWritten := uint64(0)
	if sess != nil {
		totalWritten = sess.TotalWritten()
	}

	if tp.emu == nil || tp.emuCols != ptyCols || tp.emuRows != ptyRows {
		tp.emu = tp.newTrackedEmulator(ptyCols, ptyRows)
		tp.emuFedTotal = 0
		tp.emuCols = ptyCols
		tp.emuRows = ptyRows
	}

	newBytes := totalWritten - tp.emuFedTotal
	if newBytes > uint64(len(raw)) {
		// Ring buffer wrapped — full reset and replay.
		tp.emu = tp.newTrackedEmulator(ptyCols, ptyRows)
		safeEmuWrite(tp.emu, raw)
	} else if newBytes > 0 {
		safeEmuWrite(tp.emu, raw[len(raw)-int(newBytes):])
	}
	tp.emuFedTotal = totalWritten

	tp.paintEmu(screen, x, y, w, h, tp.emu, ptyCols, ptyRows, true, tp.cursorVisible)
}

// renderReplay uses x/vt scrollback for finished sessions and scroll mode.
// Caches the emulator when input hasn't changed (same log size or same
// TotalWritten). Supports anchor-lock: when scrolled up and new output
// arrives, bumps scrollOffset so the viewed content stays pinned.
func (tp *TerminalPane) renderReplay(screen tcell.Screen, x, y, w, h int, raw []byte, logSize int64, ptyCols, ptyRows int) {
	// Check if we can reuse the cached replay emulator.
	needRebuild := tp.replayEmu == nil ||
		tp.replayEmuCols != ptyCols ||
		tp.replayEmuRows != ptyRows ||
		(logSize > 0 && tp.replayEmuLogSize != logSize) ||
		(logSize == 0 && tp.replayEmuBytes != uint64(len(raw)))

	if needRebuild {
		cursorVisible := true
		tp.replayEmu = tp.newTrackedEmulatorWithCallback(ptyCols, ptyRows, func(visible bool) {
			cursorVisible = visible
		})
		safeEmuWrite(tp.replayEmu, raw)
		tp.replayEmuCols = ptyCols
		tp.replayEmuRows = ptyRows
		tp.replayEmuLogSize = logSize
		tp.replayEmuBytes = uint64(len(raw))
		tp.replayEmuCursorVisible = cursorVisible
	}

	tp.paintEmu(screen, x, y, w, h, tp.replayEmu, ptyCols, ptyRows, tp.scrollOffset == 0, tp.replayEmuCursorVisible)
}

// paintEmu renders x/vt emulator cells to the tcell screen with content trimming and scrollback.
func (tp *TerminalPane) paintEmu(screen tcell.Screen, x, y, w, h int, emu *xvt.SafeEmulator, emuCols, emuRows int, showCursor, cursorVisible bool) {
	cur := emu.CursorPosition()
	sbLen := emu.ScrollbackLen()
	// Find content bounds in the main screen area.
	lastContentRow := findLastContentRowEmu(emu, emuCols, emuRows)
	if cur.Y > lastContentRow {
		lastContentRow = cur.Y
	}

	// Total addressable lines = scrollback + visible content.
	totalLines := sbLen + lastContentRow + 1
	firstContentRow := 0
	if sbLen == 0 {
		firstContentRow = findFirstContentRowEmu(emu, emuCols, lastContentRow)
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

	// Render visible rows. Row index is in "unified" space:
	// rows 0..sbLen-1 are scrollback, rows sbLen..sbLen+emuRows-1 are main screen.
	endLine := totalLines - 1 - tp.scrollOffset
	startLine := endLine - h + 1
	if startLine < 0 {
		startLine = 0
	}

	for screenRow := 0; screenRow < h; screenRow++ {
		lineIdx := startLine + screenRow
		if lineIdx > endLine {
			break
		}

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
				style = uvCellToTcellStyle(cell)
			}

			// Match the emulator's cursor visibility instead of forcing an Argus-owned cursor.
			if showCursor && cursorVisible && isMainScreen && mainRow == cur.Y && col == cur.X {
				style = tcell.StyleDefault.Foreground(cursorFG).Background(cursorBG)
			}

			screen.SetContent(x+col, y+screenRow, ch, nil, style)
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
				screen.SetContent(midX+i, y, r, nil, style)
			}
		}
	}
}

func (tp *TerminalPane) newTrackedEmulator(cols, rows int) *xvt.SafeEmulator {
	return tp.newTrackedEmulatorWithCallback(cols, rows, func(visible bool) {
		tp.cursorVisible = visible
	})
}

func (tp *TerminalPane) newTrackedEmulatorWithCallback(cols, rows int, onCursorVisible func(bool)) *xvt.SafeEmulator {
	emu := newDrainedEmulator(cols, rows)
	if onCursorVisible != nil {
		emu.Emulator.SetCallbacks(xvt.Callbacks{
			CursorVisibility: onCursorVisible,
		})
	}
	if onCursorVisible != nil {
		onCursorVisible(true)
	}
	return emu
}

// --- Diff rendering ---

func (tp *TerminalPane) renderDiff(screen tcell.Screen, x, y, w, h int) {
	var lines []renderedDiffLine
	if tp.diffSplit {
		// Rebuild side-by-side lines if width changed.
		if tp.diffSplitWidth != w || tp.diffSplitLines == nil {
			tp.diffSplitLines = buildSideBySideDiffLines(tp.diffParsed, tp.diffFile, w)
			tp.diffSplitWidth = w
		}
		lines = tp.diffSplitLines
	} else {
		lines = tp.diffUnifiedLines
	}

	if len(lines) == 0 {
		msg := "No diff available"
		drawText(screen, x+(w-len(msg))/2, y+h/2, w, msg, StyleDimmed)
		return
	}

	// Header
	mode := "unified"
	if tp.diffSplit {
		mode = "split"
	}
	headerText := " " + tp.diffFile + "  [" + mode + "]"
	headerStyle := tcell.StyleDefault.Foreground(ColorTitle).Bold(true)
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
		drawStyledLine(screen, x, y+1+i, w, lines[lineIdx].cells)
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

// uvCellToTcellStyle converts a *uv.Cell to a tcell.Style.
func uvCellToTcellStyle(cell *uv.Cell) tcell.Style {
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
	if attrs&uv.AttrItalic != 0 {
		style = style.Italic(true)
	}
	if attrs&uv.AttrReverse != 0 {
		style = style.Reverse(true)
	}
	if attrs&uv.AttrStrikethrough != 0 {
		style = style.StrikeThrough(true)
	}
	// Underline styles.
	ul := cell.Style.Underline
	if ul != 0 {
		style = style.Underline(true)
	}
	return style
}

// findLastContentRowEmu scans backwards to find the last row with visible content.
func findLastContentRowEmu(emu *xvt.SafeEmulator, cols, rows int) int {
	for row := rows - 1; row >= 0; row-- {
		if rowHasContentEmu(emu, row, cols) {
			return row
		}
	}
	return -1
}

// findFirstContentRowEmu scans forward to find the first row with content.
func findFirstContentRowEmu(emu *xvt.SafeEmulator, cols, maxRow int) int {
	for row := 0; row <= maxRow; row++ {
		if rowHasContentEmu(emu, row, cols) {
			return row
		}
	}
	return 0
}

// rowHasContentEmu returns true if any cell in the row has visible content.
func rowHasContentEmu(emu *xvt.SafeEmulator, row, cols int) bool {
	for x := 0; x < cols; x++ {
		cell := emu.CellAt(x, row)
		if cell == nil {
			continue
		}
		if cell.Content != "" && cell.Content != " " {
			return true
		}
		if cell.Style.Fg != nil || cell.Style.Bg != nil || cell.Style.Attrs != 0 {
			return true
		}
	}
	return false
}
