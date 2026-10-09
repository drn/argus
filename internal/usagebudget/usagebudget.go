package usagebudget

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	xvt "github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/sanitize"
	"github.com/drn/argus/internal/uxlog"
)

// probeTimeout bounds one probe run. A var (not const) so tests can shorten it.
var probeTimeout = 45 * time.Second

// trustKeyDelay separates the Down-arrow and Enter keystrokes that answer the
// probe directory's folder-trust dialog, so Claude Code's input handler sees
// two distinct key events rather than one ambiguous burst. A var so tests can
// shorten it.
var trustKeyDelay = 150 * time.Millisecond

// trustDownKey / trustUpKey move the trust dialog's selection cursor one
// option down / up.
var (
	trustDownKey = []byte("\x1b[B")
	trustUpKey   = []byte("\x1b[A")
)

// trustOptionLabel is the folder-trust dialog option the probe selects. It is
// located by this label, never by position, so a reordered dialog can't turn
// the answer into "No, exit" (or anything else).
const trustOptionLabel = "Yes, I trust this folder"

// trustSettle is how long the probe directory's trust dialog must sit with no
// new output before the probe answers it (see runProbeSession). A var so
// tests can shorten it.
var trustSettle = 1500 * time.Millisecond

// terminateGrace is how long Terminate waits after SIGTERM before SIGKILL.
var terminateGrace = 500 * time.Millisecond

const (
	// The /usage panel is tall: at 24 rows the weekly section is below the
	// fold, so the PTY must be big enough to render every section at once.
	ptyRows = 80
	ptyCols = 100

	// probeDirName is the dedicated, argus-owned, empty working directory the
	// probe runs in (no CLAUDE.md, so no external-imports dialog; a stable
	// path, so Claude Code's per-path folder trust is answered at most once).
	probeDirName = "usage-probe"

	// CacheMaxAge bounds how long threshold routing trusts a reading. The probe
	// cadence is currently 30 minutes, so one hour permits a missed tick without
	// letting old pressure steer spawns indefinitely.
	CacheMaxAge = time.Hour
)

// Reading is the latest parsed Claude weekly usage state.
type Reading struct {
	Percentage   float64
	ResetAt      time.Time
	LastProbedAt time.Time
}

type cacheState struct {
	mu      sync.RWMutex
	reading Reading
	ok      bool
}

var usageCache cacheState

// usageSession is one live `claude -- /usage` PTY session as driven by the
// streaming probe loop: Read yields PTY output as it arrives, Write sends
// keystrokes (used only to answer the probe directory's own folder-trust
// dialog), and Terminate stops the process (SIGTERM, then kill) without
// waiting for it to exit on its own — the interactive /usage session never
// does.
type usageSession interface {
	io.Reader
	io.Writer
	Terminate() error
}

// startUsageSession launches the probe session with its working directory set
// to dir. Test seam: tests substitute a scripted fake session.
var startUsageSession = startClaudeUsageSession

// probeDirFunc returns the dedicated probe working directory, creating it if
// needed. Test seam.
var probeDirFunc = ensureProbeDir

// ensureProbeDir creates (if missing) and returns the argus-owned, empty
// probe directory <data dir>/usage-probe. db.DataDir resolves through $HOME.
func ensureProbeDir() (string, error) {
	dir := filepath.Join(db.DataDir(), probeDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create probe dir: %w", err)
	}
	return dir, nil
}

var (
	nowFunc           = time.Now
	cacheSnapshotHook func()

	// probeMu serializes probes: the streaming loop feeds the shared render
	// emulator incrementally, so two concurrent probes would interleave.
	probeMu sync.Mutex
)

// logInfo / logWarn send a probe outcome to both uxlog (TUI-side ux.log) and
// slog. uxlog is a no-op in the daemon (only the TUI initializes it); slog is
// what actually reaches daemon.log.
func logInfo(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	uxlog.Log("%s", msg)
	slog.Info(msg)
}

func logWarn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	uxlog.Log("%s", msg)
	slog.Warn(msg)
}

// Probe runs one best-effort Claude /usage probe and updates the cache when the
// rendered screen parses. The interactive /usage session never exits on its
// own, so Probe streams its output into a terminal emulator, attempts a parse
// after every chunk, and terminates the session as soon as the weekly reading
// renders (or on timeout / a blocking dialog). Failures are logged and leave
// the existing cache untouched; the returned error is always nil.
func Probe(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return nil
	}
	probeMu.Lock()
	defer probeMu.Unlock()

	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	dir, err := probeDirFunc()
	if err != nil {
		logWarn("[usagebudget] probe failed: %v", err)
		return nil
	}
	sess, err := startUsageSession(probeCtx, dir)
	if err != nil {
		if ctx.Err() == nil {
			logWarn("[usagebudget] probe failed: %v", err)
		}
		return nil
	}
	defer sess.Terminate() //nolint:errcheck // best-effort; Terminate is idempotent

	runProbeSession(ctx, probeCtx, sess, dir)
	return nil
}

// runProbeSession drives one started session to an outcome (stored reading,
// timeout, blocking dialog, or end-of-output parse failure) and logs it.
func runProbeSession(parent, probeCtx context.Context, sess usageSession, dir string) {
	chunks := make(chan []byte, 64)
	stop := make(chan struct{})
	defer close(stop)
	go readSessionChunks(sess, chunks, stop)

	resetEmulator()
	var raw []byte
	inSync := false

	// The trust dialog is answered only once it has sat unchanged for
	// trustSettle: live, a Down-arrow sent right after the dialog's first
	// paint was undone by Claude Code's own follow-up repaint, so the
	// subsequent Enter chose "No, exit".
	trustAnswered := false
	trustOffset := 0 // option rows from the cursor to the trust option
	trustTimer := time.NewTimer(time.Hour)
	trustTimer.Stop()
	defer trustTimer.Stop()
	var trustTimerC <-chan time.Time

	for {
		select {
		case <-probeCtx.Done():
			_ = sess.Terminate()
			if parent.Err() == nil {
				logWarn("[usagebudget] probe timed out after %s", probeTimeout)
			}
			return

		case <-trustTimerC:
			trustTimerC = nil
			trustAnswered = true
			if err := answerTrustDialog(probeCtx, sess, trustOffset); err != nil {
				_ = sess.Terminate()
				if parent.Err() == nil {
					logWarn("[usagebudget] probe failed: answer trust dialog: %v", err)
				}
				return
			}

		case chunk, ok := <-chunks:
			if !ok {
				// The session ended on its own before the weekly section
				// rendered. Last attempt over everything it printed.
				finishWithoutSession(raw)
				return
			}
			raw = append(raw, chunk...)
			screen := feedEmulator(chunk)
			inSync = syncUpdateOpen(raw, len(chunk), inSync)
			if trustTimerC != nil {
				// New output while waiting to answer: restart the settle wait.
				trustTimer.Reset(trustSettle)
			}
			if inSync {
				// Mid-frame: the screen is half old frame, half new.
				continue
			}
			// /usage first paints Claude Code's locally cached figures with a
			// "Refreshing…" marker, then repaints with the fetched ones; only
			// the settled screen is authoritative.
			if !usageRefreshing(screen) && storeIfParsed(screen) {
				_ = sess.Terminate()
				return
			}
			d := detectDialog(screen)
			switch {
			case d == nil:
				if trustTimerC != nil {
					// The dialog went away on its own; nothing to answer.
					trustTimer.Stop()
					trustTimerC = nil
				}
			case d.trust && trustPathMatches(d.workspace, dir) && !d.trustOptionFound:
				_ = sess.Terminate()
				logWarn("[usagebudget] probe blocked by dialog: %s (no %q option)", d.title, trustOptionLabel)
				return
			case d.trust && trustPathMatches(d.workspace, dir):
				// Our own probe directory: answer it (once) after it settles,
				// from the latest frame's cursor position. Once answered, the
				// dialog lingers until Claude repaints.
				trustOffset = d.trustOffset
				if !trustAnswered && trustTimerC == nil {
					trustTimer.Reset(trustSettle)
					trustTimerC = trustTimer.C
				}
			default:
				_ = sess.Terminate()
				logWarn("[usagebudget] probe blocked by dialog: %s", d.title)
				return
			}
		}
	}
}

// readSessionChunks copies session output into chunks until Read errors
// (io.EOF on exit / Terminate) or the probe loop stops listening.
func readSessionChunks(sess usageSession, chunks chan<- []byte, stop <-chan struct{}) {
	defer close(chunks)
	for {
		buf := make([]byte, 4096)
		n, err := sess.Read(buf)
		if n > 0 {
			select {
			case chunks <- buf[:n]:
			case <-stop:
				return
			}
		}
		if err != nil {
			return
		}
	}
}

var (
	syncBegin = []byte("\x1b[?2026h")
	syncEnd   = []byte("\x1b[?2026l")
)

// syncUpdateOpen reports whether the output so far ends inside a synchronized
// update (DEC mode 2026, which Claude Code wraps every frame in). The screen
// is only evaluated between frames: mid-frame it mixes old and new content
// (e.g. a stale percentage beside an already-cleared "Refreshing…" marker).
// Only the newest chunk (plus a marker-length overlap, for markers split
// across chunks) is scanned; prev carries the state when it has no marker.
func syncUpdateOpen(raw []byte, chunkLen int, prev bool) bool {
	start := max(0, len(raw)-chunkLen-len(syncBegin)+1)
	window := raw[start:]
	b, e := bytes.LastIndex(window, syncBegin), bytes.LastIndex(window, syncEnd)
	if b < 0 && e < 0 {
		return prev
	}
	return b > e
}

// usageRefreshing reports whether the /usage panel is still showing cached
// figures while it fetches fresh ones (captured live: the first frames read
// 50% under "Refreshing…", the settled frame 51%).
func usageRefreshing(screen string) bool {
	return strings.Contains(screen, "Refreshing…") || strings.Contains(screen, "Refreshing...")
}

func finishWithoutSession(raw []byte) {
	if storeIfParsed(renderUsageOutput(raw)) {
		return
	}
	_, err := parseUsageOutput(renderUsageOutput(raw), nowFunc())
	logWarn("[usagebudget] parse failed: session ended before the weekly reading rendered: %v", err)
}

// storeIfParsed stores the reading and logs success when screen parses.
func storeIfParsed(screen string) bool {
	probedAt := nowFunc()
	reading, err := parseUsageOutput(screen, probedAt)
	if err != nil {
		return false
	}
	reading.LastProbedAt = probedAt
	storeReading(reading)
	logInfo("[usagebudget] probe updated: percentage=%.2f reset_at=%s", reading.Percentage, reading.ResetAt.Format(time.RFC3339))
	return true
}

// answerTrustDialog moves the selection cursor offset option rows (down when
// positive, up when negative) onto "Yes, I trust this folder", pausing after
// each arrow so Claude Code sees distinct key events, then presses Enter.
func answerTrustDialog(ctx context.Context, sess usageSession, offset int) error {
	key := trustDownKey
	if offset < 0 {
		key, offset = trustUpKey, -offset
	}
	for range offset {
		if _, err := sess.Write(key); err != nil {
			return err
		}
		select {
		case <-time.After(trustKeyDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	_, err := sess.Write([]byte("\r"))
	return err
}

// CachedClaudePct returns the most recently probed Claude weekly usage
// percentage and whether that reading is present and not stale. It never
// triggers a live probe, so it is safe to call synchronously from
// internal/backendtier's tier-list resolver.
func CachedClaudePct() (float64, bool) {
	reading, ok := snapshot()
	if !ok {
		return 0, false
	}
	return reading.Percentage, true
}

// CachedReading returns the full most-recently-probed Reading (percentage,
// reset time, and when it was probed) and whether it is present and not
// stale, for callers that need staleness information beyond the bare
// percentage (e.g. the daemon's BootInfo relay, surfaced in the TUI status
// bar). Never triggers a live probe.
func CachedReading() (Reading, bool) {
	return snapshot()
}

func storeReading(reading Reading) {
	usageCache.mu.Lock()
	defer usageCache.mu.Unlock()
	usageCache.reading = reading
	usageCache.ok = true
}

func snapshot() (Reading, bool) {
	if cacheSnapshotHook != nil {
		cacheSnapshotHook()
	}
	usageCache.mu.RLock()
	defer usageCache.mu.RUnlock()
	if !usageCache.ok {
		return Reading{}, false
	}
	reading := usageCache.reading
	if nowFunc().Sub(reading.LastProbedAt) > CacheMaxAge {
		return Reading{}, false
	}
	return reading, true
}

// --- real PTY session ------------------------------------------------------

type ptyUsageSession struct {
	cmd      *exec.Cmd
	ptmx     *os.File
	waitDone chan struct{}
	termOnce sync.Once
}

// startClaudeUsageSession runs `claude -- /usage` in a PTY with cmd.Dir = dir.
// Default-account only: tasks on other Claude accounts are not probed, so an
// inherited CLAUDE_CONFIG_DIR is dropped rather than probing some other dir.
func startClaudeUsageSession(ctx context.Context, dir string) (usageSession, error) {
	s, err := startPTYSession(ctx, dir, "claude", "--", "/usage")
	if err != nil {
		return nil, err // never a typed-nil *ptyUsageSession in the interface
	}
	return s, nil
}

// startPTYSession runs name+args in a ptyRows x ptyCols PTY with cmd.Dir =
// dir and the probe environment. Split from startClaudeUsageSession so tests
// can exercise the real PTY/terminate path with a stand-in command.
func startPTYSession(ctx context.Context, dir, name string, args ...string) (*ptyUsageSession, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	env := make([]string, 0, len(os.Environ())+2)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") {
			env = append(env, kv)
		}
	}
	cmd.Env = append(env,
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
	)

	// pty.Start puts the child in its own session (Setsid), so its pid is
	// also its process-group id: Terminate signals the whole group, reaching
	// hook / MCP-server children too.
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: ptyRows, Cols: ptyCols})
	if err != nil {
		return nil, err
	}
	s := &ptyUsageSession{cmd: cmd, ptmx: ptmx, waitDone: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(s.waitDone)
	}()
	return s, nil
}

func (s *ptyUsageSession) Read(p []byte) (int, error)  { return s.ptmx.Read(p) }
func (s *ptyUsageSession) Write(p []byte) (int, error) { return s.ptmx.Write(p) }

// Terminate sends SIGTERM to the session's process group, escalates to
// SIGKILL after terminateGrace, and closes the PTY. Once the leader is gone
// (or was killed) the whole group is SIGKILLed too, so children that ignore
// SIGTERM (MCP servers, hooks) never outlive the probe. A session that had
// already exited on its own is not group-signalled at all: with its leader
// reaped, the pgid may since have been reused by an unrelated process group.
// Idempotent.
func (s *ptyUsageSession) Terminate() error {
	s.termOnce.Do(func() {
		defer s.ptmx.Close() //nolint:errcheck // best-effort
		select {
		case <-s.waitDone:
			return // exited on its own before Terminate
		default:
		}
		pid := s.cmd.Process.Pid
		_ = syscall.Kill(-pid, syscall.SIGTERM)
		select {
		case <-s.waitDone:
		case <-time.After(terminateGrace):
			killGroup(pid)
			select {
			case <-s.waitDone:
			case <-time.After(terminateGrace):
			}
		}
		killGroup(pid)
	})
	return nil
}

// killGroup SIGKILLs process group pgid; ESRCH (already empty) is expected.
func killGroup(pgid int) {
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		uxlog.Log("[usagebudget] kill probe process group %d: %v", pgid, err)
	}
}

// --- rendering -------------------------------------------------------------

var renderState struct {
	mu   sync.Mutex
	emu  *xvt.SafeEmulator
	cols int
	rows int
}

// renderUsageOutput renders a complete raw capture in one go (rendered screen
// plus an ANSI-stripped fallback). Used for the end-of-session last attempt.
func renderUsageOutput(raw []byte) string {
	stripped := normalizeText(sanitize.StripANSI(string(raw)))
	rendered := renderPTY(raw)
	if rendered == "" {
		return stripped
	}
	if stripped == "" {
		return rendered
	}
	return rendered + "\n" + stripped
}

func renderPTY(raw []byte) string {
	resetEmulator()
	return feedEmulator(raw)
}

// resetEmulator returns the shared emulator to a blank screen. One emulator is
// reused (RIS-reset) for the process lifetime rather than recreated per probe:
// a SafeEmulator's response-drain goroutine can't be stopped safely, so
// discarding emulators leaks them (see gotchas/pty-terminal.md).
func resetEmulator() {
	defer recoverEmulator()
	renderState.mu.Lock()
	defer renderState.mu.Unlock()

	if renderState.emu == nil {
		renderState.emu = xvt.NewSafeEmulator(ptyCols, ptyRows)
		renderState.cols = ptyCols
		renderState.rows = ptyRows
		go io.Copy(io.Discard, renderState.emu) //nolint:errcheck
		return
	}
	if _, err := renderState.emu.Write([]byte("\x1bc")); err != nil {
		uxlog.Log("[usagebudget] emulator reset failed: %v", err)
	}
	if renderState.cols != ptyCols || renderState.rows != ptyRows {
		renderState.emu.Resize(ptyCols, ptyRows)
		renderState.cols = ptyCols
		renderState.rows = ptyRows
	}
}

// feedEmulator writes chunk into the shared emulator (on top of whatever it
// already holds) and returns the rendered screen text.
func feedEmulator(chunk []byte) (out string) {
	defer func() {
		if rec := recover(); rec != nil {
			uxlog.Log("[usagebudget] recovered from emulator panic: %v", rec)
			out = ""
		}
	}()
	renderState.mu.Lock()
	defer renderState.mu.Unlock()
	if renderState.emu == nil {
		return ""
	}
	if _, err := renderState.emu.Write(chunk); err != nil {
		uxlog.Log("[usagebudget] emulator write failed: %v", err)
		return ""
	}
	return normalizeText(renderState.emu.String())
}

func recoverEmulator() {
	if rec := recover(); rec != nil {
		uxlog.Log("[usagebudget] recovered from emulator panic: %v", rec)
	}
}

func normalizeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\x00", " ")
	s = strings.ReplaceAll(s, " ", " ")
	return s
}

// --- dialog detection ------------------------------------------------------

// dialog is a blocking selection dialog found on the rendered screen.
type dialog struct {
	title     string // first non-border line, for logging
	trust     bool   // Claude Code's folder-trust dialog
	workspace string // trust dialog only: the (wrap-joined) workspace path

	// trust dialog only: whether the trustOptionLabel option row was found,
	// and how many option rows it sits below (negative: above) the cursor.
	trustOptionFound bool
	trustOffset      int
}

const selectionCursor = "❯"

// detectDialog recognizes a blocking selection dialog by its UI shape: a row
// led by the ❯ selection cursor with at least one further option row below
// it, plus a confirm/cancel footer. (Claude's input prompt also uses ❯, but is
// never paired with the confirm footer.) It returns nil when none is showing.
// The folder-trust dialog is then identified by its "Accessing workspace:"
// anchor, which is the only reliable way to read the path it names.
func detectDialog(screen string) *dialog {
	lines := strings.Split(screen, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}

	cursorRow, footerRow := -1, -1
	for i, l := range lines {
		if cursorRow < 0 && strings.HasPrefix(l, selectionCursor) && len(l) > len(selectionCursor) {
			cursorRow = i
			continue
		}
		if cursorRow >= 0 && strings.Contains(l, "Enter to confirm") {
			footerRow = i
			break
		}
	}
	if cursorRow < 0 || footerRow < 0 {
		return nil
	}
	options := 0
	for _, l := range lines[cursorRow+1 : footerRow] {
		if l != "" {
			options++
		}
	}
	if options == 0 {
		return nil
	}

	d := &dialog{}
	for _, l := range lines {
		if l != "" && !isBorderLine(l) {
			d.title = l
			break
		}
	}
	for i, l := range lines {
		if !strings.HasPrefix(l, "Accessing workspace:") {
			continue
		}
		d.trust = true
		d.workspace = strings.TrimSpace(strings.TrimPrefix(l, "Accessing workspace:"))
		j := i + 1
		for j < len(lines) && lines[j] == "" && d.workspace == "" {
			j++
		}
		// The path wraps at the screen edge; join its consecutive rows.
		for ; j < len(lines) && lines[j] != ""; j++ {
			d.workspace += lines[j]
		}
		break
	}
	if d.trust {
		d.trustOffset, d.trustOptionFound = trustOptionOffset(lines, cursorRow, footerRow)
	}
	return d
}

// trustOptionOffset locates the trustOptionLabel row within the dialog's
// option block — the rows from the cursor down to the footer, plus the
// contiguous non-blank rows directly above the cursor — and returns how many
// option rows it lies below (positive) or above (negative) the cursor row.
// Blank rows are not options and are not counted.
func trustOptionOffset(lines []string, cursorRow, footerRow int) (int, bool) {
	isTrust := func(l string) bool {
		return strings.Contains(strings.TrimPrefix(l, selectionCursor), trustOptionLabel)
	}
	if isTrust(lines[cursorRow]) {
		return 0, true
	}
	n := 0
	for _, l := range lines[cursorRow+1 : footerRow] {
		if l == "" {
			continue
		}
		n++
		if isTrust(l) {
			return n, true
		}
	}
	n = 0
	for i := cursorRow - 1; i >= 0 && lines[i] != ""; i-- {
		n++
		if isTrust(lines[i]) {
			return -n, true
		}
	}
	return 0, false
}

func isBorderLine(l string) bool {
	for _, r := range l {
		if !strings.ContainsRune("─━═▔▁╭╮╰╯│┃", r) {
			return false
		}
	}
	return true
}

// trustPathMatches reports whether the trust dialog's workspace path is the
// probe directory (comparing symlink-resolved forms too, since Claude Code
// shows the real path — e.g. /private/tmp for /tmp on macOS).
func trustPathMatches(workspace, dir string) bool {
	if workspace == "" || dir == "" {
		return false
	}
	candidates := []string{filepath.Clean(dir)}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		candidates = append(candidates, filepath.Clean(resolved))
	}
	ws := filepath.Clean(workspace)
	for _, c := range candidates {
		if ws == c {
			return true
		}
	}
	return false
}

// --- parsing ---------------------------------------------------------------

var percentageRe = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)\s*%`)

var resetRe = regexp.MustCompile(`(?i)\bResets\s+(?:on\s+)?(.+?)\s+at\s+([0-9]{1,2}(?::[0-9]{2})?\s*(?:AM|PM)?)\s*\(([^)]+)\)`)

var meridiemRe = regexp.MustCompile(`(?i)([0-9])\s*(AM|PM)\b`)

// parseUsageOutput extracts the "Current week (all models)" reading. The
// percentage may sit on the header line itself or on a following line before
// the next section header (the real /usage layout puts it on the bar line
// after the header); the "Resets ..." line is taken from the same section.
// Other sections (Current session, per-model weeks, Usage credits) are never
// read.
func parseUsageOutput(output string, base time.Time) (Reading, error) {
	lines := strings.Split(output, "\n")
	header := -1
	for i, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "current week") && strings.Contains(lower, "all models") {
			header = i
			break
		}
	}
	if header < 0 {
		return Reading{}, errors.New("weekly section not found")
	}

	end := len(lines)
	for i := header + 1; i < len(lines); i++ {
		if isSectionHeader(lines[i]) {
			end = i
			break
		}
	}
	section := lines[header:end]

	var pct float64
	foundPct := false
	for _, line := range section {
		match := percentageRe.FindStringSubmatch(line)
		if len(match) < 2 {
			continue
		}
		parsed, err := strconv.ParseFloat(match[1], 64)
		if err != nil {
			return Reading{}, fmt.Errorf("parse percentage %q: %w", match[1], err)
		}
		pct = parsed
		foundPct = true
		break
	}
	if !foundPct {
		return Reading{}, errors.New("weekly percentage not found")
	}

	match := resetRe.FindStringSubmatch(strings.Join(section, "\n"))
	if len(match) < 4 {
		return Reading{}, errors.New("reset timestamp not found")
	}
	resetAt, err := parseResetTimestamp(match[1], match[2], match[3], base)
	if err != nil {
		return Reading{}, err
	}
	return Reading{Percentage: pct, ResetAt: resetAt}, nil
}

// isSectionHeader reports whether line starts a new /usage section: a line
// with words but no percentage that isn't a "Resets ..." line. Bar-only rows
// (block characters, no letters) and blank rows are not headers.
func isSectionHeader(line string) bool {
	trimmed := strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "│┃|"))
	if trimmed == "" || percentageRe.MatchString(trimmed) {
		return false
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "resets") {
		return false
	}
	for _, r := range trimmed {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return true
		}
	}
	return false
}

var (
	ordinalDayRe = regexp.MustCompile(`\b([0-9]{1,2})(?:st|nd|rd|th)\b`)
	yearRe       = regexp.MustCompile(`\b[0-9]{4}\b`)
)

func parseResetTimestamp(datePart, timePart, zonePart string, base time.Time) (time.Time, error) {
	datePart = cleanDatePart(datePart)
	timePart = meridiemRe.ReplaceAllString(timePart, "$1 $2") // "1am" -> "1 am"
	timePart = strings.ToUpper(strings.Join(strings.Fields(timePart), " "))
	loc := locationForZone(zonePart)
	hasYear := yearRe.MatchString(datePart)

	input := datePart + " " + timePart
	if !hasYear {
		input = fmt.Sprintf("%s %d %s", datePart, base.In(loc).Year(), timePart)
	}

	layouts := []string{
		"Jan 2 2006 3:04 PM",
		"January 2 2006 3:04 PM",
		"Jan 2 2006 3 PM",
		"January 2 2006 3 PM",
		"Jan 2 2006 15:04",
		"January 2 2006 15:04",
		"2006-01-02 3:04 PM",
		"2006-01-02 3 PM",
		"2006-01-02 15:04",
	}
	var lastErr error
	for _, layout := range layouts {
		parsed, err := time.ParseInLocation(layout, input, loc)
		if err == nil {
			if !hasYear {
				baseInLoc := base.In(loc)
				if parsed.Before(baseInLoc.Add(-24 * time.Hour)) {
					parsed = parsed.AddDate(1, 0, 0)
				}
			}
			return parsed, nil
		}
		lastErr = err
	}
	return time.Time{}, fmt.Errorf("parse reset timestamp %q (%s): %w", input, zonePart, lastErr)
}

func cleanDatePart(s string) string {
	s = sanitize.StripANSI(s)
	s = strings.ReplaceAll(s, ",", " ")
	s = ordinalDayRe.ReplaceAllString(s, "$1")
	s = strings.Join(strings.Fields(s), " ")
	parts := strings.Fields(s)
	if len(parts) > 1 && isWeekday(parts[0]) {
		s = strings.Join(parts[1:], " ")
	}
	return s
}

func isWeekday(s string) bool {
	switch strings.ToLower(strings.TrimSuffix(s, ".")) {
	case "mon", "monday", "tue", "tues", "tuesday", "wed", "wednesday",
		"thu", "thur", "thurs", "thursday", "fri", "friday",
		"sat", "saturday", "sun", "sunday":
		return true
	default:
		return false
	}
}

// locationForZone maps the zone in a "Resets ... (zone)" line to a location:
// IANA names (e.g. "America/Los_Angeles", what current Claude Code prints)
// load directly; common US abbreviations map to their IANA zone.
func locationForZone(zone string) *time.Location {
	zone = strings.TrimSpace(zone)
	switch strings.ToUpper(zone) {
	case "UTC", "GMT":
		return time.UTC
	case "PST", "PDT":
		zone = "America/Los_Angeles"
	case "MST", "MDT":
		zone = "America/Denver"
	case "CST", "CDT":
		zone = "America/Chicago"
	case "EST", "EDT":
		zone = "America/New_York"
	}
	if strings.Contains(zone, "/") {
		if loc, err := time.LoadLocation(zone); err == nil {
			return loc
		}
	}
	return time.Local
}
