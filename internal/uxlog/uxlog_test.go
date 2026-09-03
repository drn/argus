package uxlog

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestInitAndLog(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test-ux.log")

	if err := Init(logPath); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer Close()

	Log("hello %s %d", "world", 42)
	Log("second line")

	// Close to flush
	Close()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "hello world 42") {
		t.Errorf("expected 'hello world 42' in log, got: %s", content)
	}
	if !strings.Contains(content, "second line") {
		t.Errorf("expected 'second line' in log, got: %s", content)
	}

	// Each line should have a timestamp prefix
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	for _, line := range lines {
		// Timestamp format: 2006/01/02 15:04:05.000
		if len(line) < 24 {
			t.Errorf("line too short for timestamp: %s", line)
		}
	}
}

func TestLogNoInit(t *testing.T) {
	// Ensure Log is a no-op when not initialized — should not panic.
	// Reset global state to simulate uninitialized.
	mu.Lock()
	old := file
	file = nil
	mu.Unlock()

	Log("this should be a no-op")

	mu.Lock()
	file = old
	mu.Unlock()
}

func TestInitIdempotent(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test-ux.log")

	if err := Init(logPath); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Second init should be a no-op (not error)
	if err := Init(logPath); err != nil {
		t.Fatalf("second Init failed: %v", err)
	}

	Close()
}

func TestPath(t *testing.T) {
	got := Path("/home/user/.argus")
	if got != "/home/user/.argus/ux.log" {
		t.Errorf("Path returned %q", got)
	}
}

// TestWriter_ReturnsDiscardWhenNotInitialized pins the safety contract that
// `Writer()` never returns nil. Callers (notably runTUI's
// `slog.SetDefault(slog.New(slog.NewTextHandler(uxlog.Writer(), nil)))`)
// would panic on a nil writer.
func TestWriter_ReturnsDiscardWhenNotInitialized(t *testing.T) {
	mu.Lock()
	old := file
	file = nil
	mu.Unlock()
	defer func() {
		mu.Lock()
		file = old
		mu.Unlock()
	}()

	w := Writer()
	if w == nil {
		t.Fatal("Writer returned nil when uxlog not initialized")
	}
	if w != io.Discard {
		t.Errorf("Writer should return io.Discard when uninitialized, got %T", w)
	}
	// Should not panic when written to.
	if _, err := w.Write([]byte("test")); err != nil {
		t.Errorf("write to discard returned error: %v", err)
	}
}

// TestWriter_ReturnsLogFileWhenInitialized pins that Writer hands out the
// same file uxlog writes to, so slog output co-located with uxlog timestamps
// for easy correlation.
func TestWriter_ReturnsLogFileWhenInitialized(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test-ux.log")
	if err := Init(logPath); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer Close()

	w := Writer()
	if w == nil {
		t.Fatal("Writer returned nil after Init")
	}
	if w == io.Discard {
		t.Fatal("Writer returned io.Discard after Init")
	}

	// Writing through Writer() and reading via the file should round-trip.
	if _, err := w.Write([]byte("via writer\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	Close()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), "via writer") {
		t.Errorf("expected 'via writer' in log, got: %s", string(data))
	}
}

// TestSlogWithUxlogWriter_DoesNotReachStderr is the regression guard for the
// "slog leaks to terminal" class of bug. CLAUDE.md hard rule 6: no code path
// reachable from runTUI may write to os.Stderr or os.Stdout once app.Run has
// taken over, because those fds ARE the user's terminal and writes corrupt
// tcell's displayed cell state. The fix is to redirect slog's default
// handler in runTUI; this test asserts that wiring `uxlog.Writer()` as the
// slog handler's destination genuinely keeps output OUT of stderr.
//
// If a future refactor breaks the wiring (e.g., removes `uxlog.Writer()` or
// changes slog.NewTextHandler's destination), this test fails before the
// regression ships.
//
// Test-isolation contract: this test mutates THREE process globals —
// `slog.Default()`, `log`'s default writer, and `os.Stderr`. All three are
// captured up front and restored via `t.Cleanup` so subsequent tests in
// the same binary run see the pre-test state. Without restoration, every
// following test in the package (or any package run in the same `go test`
// binary) would have its slog/log output redirected through this test's
// pipe — which is closed mid-body — producing silent dropped logs and
// confusing write-to-closed-fd errors. The unanimous BLOCKING finding
// from /rereview iter 1 was specifically this restore gap.
func TestSlogWithUxlogWriter_DoesNotReachStderr(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test-ux.log")
	if err := Init(logPath); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Save process-global state we're about to mutate so subsequent tests
	// see the pre-test defaults. `t.Cleanup` fires in LIFO order, after any
	// `defer` in this test body — so cleanups run even on `t.Fatalf` panic.
	origSlog := slog.Default()
	origLog := log.Writer()
	origStderr := os.Stderr
	t.Cleanup(func() {
		slog.SetDefault(origSlog)
		log.SetOutput(origLog)
		os.Stderr = origStderr
		Close()
	})

	// Capture anything that hits stderr during this test.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w

	// Mirror runTUI's wiring exactly.
	slog.SetDefault(slog.New(slog.NewTextHandler(Writer(), nil)))
	log.SetOutput(Writer())

	// Fire the kind of calls that historically bled through.
	slog.Info("slog info from TUI process")
	slog.Error("slog error from TUI process", "task", "test-task")
	log.Printf("stdlib log print from TUI process")

	// Read whatever (if anything) reached stderr.
	if cerr := w.Close(); cerr != nil {
		t.Fatalf("close pipe writer: %v", cerr)
	}
	captured, rerr := io.ReadAll(r)
	if rerr != nil {
		t.Fatalf("read captured stderr: %v", rerr)
	}
	if len(captured) != 0 {
		t.Errorf("slog/log wrote to stderr after redirect: %q", string(captured))
	}

	// Restore slog/log defaults BEFORE closing the uxlog file, so any
	// late-firing slog calls in `t.Cleanup` (e.g., from goroutines leaked
	// by earlier tests) write to the original destination, not the
	// now-closed file. `t.Cleanup` restores os.Stderr and re-Closes uxlog
	// — re-Close is safe because uxlog.Close is idempotent (nils file).
	slog.SetDefault(origSlog)
	log.SetOutput(origLog)
	Close()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	content := string(data)
	for _, want := range []string{
		"slog info from TUI process",
		"slog error from TUI process",
		"stdlib log print from TUI process",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("expected %q in uxlog, got: %s", want, content)
		}
	}
}

// TestRotate_TruncatesPreexistingOversizedFileOnInit is the RED case for
// unbounded growth: a file that is ALREADY oversized before Init is ever
// called (the real-world shape — ux.log accumulates across many separate
// process launches over months, since Init opens with O_APPEND, not
// O_TRUNC) must be capped as soon as any process opens it, not only once a
// long-running process happens to write enough new lines itself.
func TestRotate_TruncatesPreexistingOversizedFileOnInit(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test-ux.log")

	orig := maxSize
	maxSize = 1000
	t.Cleanup(func() { maxSize = orig })

	// Pre-populate a file well past the threshold with numbered lines, so we
	// can assert on which survive.
	var buf bytes.Buffer
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&buf, "2026/01/01 00:00:00.000 preexisting-line-%04d\n", i)
	}
	if err := os.WriteFile(logPath, buf.Bytes(), 0644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	preSize := int64(buf.Len())
	if preSize <= maxSize {
		t.Fatalf("test setup: seeded file (%d bytes) must exceed maxSize (%d)", preSize, maxSize)
	}

	if err := Init(logPath); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer Close()
	Close() // flush/close so we can read a stable size immediately

	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() > maxSize {
		t.Errorf("file not capped on Init: size=%d maxSize=%d", info.Size(), maxSize)
	}
	if info.Size() >= preSize {
		t.Errorf("file did not shrink: pre=%d post=%d", preSize, info.Size())
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(data), "preexisting-line-0000") {
		t.Errorf("oldest content should have been dropped, found preexisting-line-0000 in: %s", data)
	}
	if !strings.Contains(string(data), "preexisting-line-0499") {
		t.Errorf("most recent content should have survived, missing preexisting-line-0499 in: %s", data)
	}
}

// TestRotate_CapsGrowthAcrossManyWrites is the RED case for the "long
// session" shape: many Log() calls within a single process must never let
// the file grow past a bounded ceiling, and the newest content must always
// survive rotation while the oldest is dropped.
func TestRotate_CapsGrowthAcrossManyWrites(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test-ux.log")

	orig := maxSize
	maxSize = 2000
	t.Cleanup(func() { maxSize = orig })

	if err := Init(logPath); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer Close()

	const numLines = 2000
	for i := 0; i < numLines; i++ {
		Log("growth-line-%04d", i)
	}
	Close()

	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// Allow a small overshoot margin (one line's worth) above maxSize, since
	// rotation happens after a write crosses the threshold, not before.
	const lineMargin = 200
	if info.Size() > maxSize+lineMargin {
		t.Errorf("file grew unbounded: size=%d maxSize=%d", info.Size(), maxSize)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(data), "growth-line-0000 ") || strings.Contains(string(data), "growth-line-0000\n") {
		t.Errorf("oldest line should have been rotated away, found growth-line-0000 in: %s", data)
	}
	last := fmt.Sprintf("growth-line-%04d", numLines-1)
	if !strings.Contains(string(data), last) {
		t.Errorf("newest line should have survived, missing %s", last)
	}
}

// TestRotate_SafeAcrossIndependentAppendHandle pins the core assumption
// behind rotating via in-place truncation rather than rename-and-reopen: the
// daemon and the TUI each hold their OWN independent os.File handle, opened
// O_APPEND, to the same ux.log path (see gotchas/misc.md) — there is no
// shared in-process state to coordinate rotation between them. This test
// simulates that by opening a second, independent O_APPEND handle to the
// same path (standing in for "the other process"), forcing a rotation via
// the package's own handle, and then writing through the second handle —
// which must land cleanly at the new end of file, not get lost or corrupt
// the file, proving truncation is safe even with a foreign handle open.
func TestRotate_SafeAcrossIndependentAppendHandle(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test-ux.log")

	orig := maxSize
	maxSize = 500
	t.Cleanup(func() { maxSize = orig })

	if err := Init(logPath); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer Close()

	// Simulate a second, independent process holding its own O_APPEND
	// handle to the same path.
	other, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatalf("open second handle: %v", err)
	}
	defer other.Close()

	// Drive the file well past maxSize through the package's own handle so
	// a rotation fires.
	for i := 0; i < 200; i++ {
		Log("first-handle-line-%04d", i)
	}

	// Now write through the OTHER (foreign) handle. If truncation broke
	// O_APPEND's end-of-file tracking for this handle, this write would
	// either error, land at a stale offset, or leave a gap of NUL bytes.
	sentinel := "second-handle-sentinel-line\n"
	if _, err := other.WriteString(sentinel); err != nil {
		t.Fatalf("write via second handle: %v", err)
	}

	Close()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), sentinel) {
		t.Errorf("second handle's write did not land in the file: %q", data)
	}
	if bytes.Contains(data, []byte{0}) {
		t.Errorf("file contains NUL bytes, indicating a gap left by a stale append offset: %q", data)
	}
	// The sentinel must be the LAST content in the file (appended after the
	// rotation), not swallowed by it.
	if !strings.HasSuffix(string(data), sentinel) {
		t.Errorf("second handle's write should be the tail of the file, got: %q", data)
	}
}

// TestRotate_NoNewlineInTailKeptWhole covers the edge case where the rotated
// tail window contains no line boundary at all (a single line far longer
// than the keep window) — rotateIfOversizedLocked must keep the raw bytes
// rather than dropping everything for lack of a '\n' to align on.
func TestRotate_NoNewlineInTailKeptWhole(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test-ux.log")

	orig := maxSize
	maxSize = 100
	t.Cleanup(func() { maxSize = orig })

	huge := strings.Repeat("x", 500)
	if err := os.WriteFile(logPath, []byte(huge), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := Init(logPath); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer Close()
	Close()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(data) == 0 {
		t.Errorf("expected some tail content to survive rotation, got an empty file")
	}
	if strings.ContainsRune(string(data), 0) {
		t.Errorf("tail content should be raw bytes from the source line, got: %q", data)
	}
}

// TestRotate_StatErrorIsNoop covers the defensive best-effort path: a
// failure to Stat the log handle (simulated here via an already-closed fd)
// must never panic uxlog — it should just skip rotation for that call.
func TestRotate_StatErrorIsNoop(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test-ux.log")

	f, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	f.Close() // closed fd: file.Stat() now errors

	mu.Lock()
	old := file
	file = f
	rotateIfOversizedLocked() // must not panic on a closed handle
	file = old
	mu.Unlock()
}

// TestFd2RedirectViaDup2_CatchesRawSyscallWrites is the regression guard for
// the OS-level fd 2 redirect that runTUI installs as belt-and-braces for
// everything slog/log redirects can't catch — runtime panic stack dumps,
// subprocess fd 2 inheritance, third-party library writes to fd 2. The
// slog/log redirects only change the Go-level Writer that the standard
// loggers use; they do NOT change the OS-level meaning of file descriptor 2.
// Code that writes directly to fd 2 (e.g., via syscall.Write or via the Go
// runtime's internal panic-printing path) bypasses every Writer-based
// redirect.
//
// This test simulates a raw fd 2 write (the same syscall path the Go runtime
// uses for panic stack dumps) and verifies that with the Dup2 in place,
// those bytes land in the uxlog file instead of leaking to the terminal.
//
// Test-isolation: like the slog test above, this mutates the global fd 2
// and restores it via t.Cleanup. No t.Parallel so cross-test races are
// bounded to sequential package-test execution.
func TestFd2RedirectViaDup2_CatchesRawSyscallWrites(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test-ux.log")
	if err := Init(logPath); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Mirror runTUI's Dup2 + deferred restore wiring.
	f, ok := Writer().(*os.File)
	if !ok {
		t.Fatalf("Writer() returned %T, expected *os.File", Writer())
	}
	// fd values from *os.File.Fd() are guaranteed-small positive ints — the
	// uintptr → int conversion can never overflow. Silence gosec G115.
	stderrFd := int(os.Stderr.Fd()) //nolint:gosec // see comment
	uxlogFd := int(f.Fd())          //nolint:gosec // see comment

	origStderrFd, err := syscall.Dup(stderrFd)
	if err != nil {
		t.Fatalf("Dup(stderr): %v", err)
	}
	if err := syscall.Dup2(uxlogFd, stderrFd); err != nil {
		_ = syscall.Close(origStderrFd)
		t.Fatalf("Dup2(uxlog → stderr): %v", err)
	}
	t.Cleanup(func() {
		_ = syscall.Dup2(origStderrFd, stderrFd)
		_ = syscall.Close(origStderrFd)
		Close()
	})

	// Simulate the runtime's panic-printing path: write directly to fd 2
	// via the raw syscall. If the Dup2 took effect, these bytes go to the
	// uxlog file. If the redirect didn't work, they'd appear on the test's
	// stderr (which Go's test runner inherits — so you'd see them in the
	// `go test` output as garbage).
	sentinel := []byte("FD2_RAW_WRITE_SENTINEL_via_syscall_Write\n")
	if _, werr := syscall.Write(stderrFd, sentinel); werr != nil {
		t.Fatalf("syscall.Write(fd 2): %v", werr)
	}

	// Force a flush by closing and re-reading. We close uxlog (idempotent;
	// Cleanup re-closes) so the buffer is committed to disk before read.
	Close()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !bytes.Contains(data, sentinel) {
		t.Errorf("fd 2 Dup2 did not redirect raw syscall write to uxlog; "+
			"sentinel missing. logfile=%s contents=%q", logPath, string(data))
	}
}
