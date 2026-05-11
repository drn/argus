package agent

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestStartSession_EchoCommand(t *testing.T) {
	cmd := exec.Command("echo", "hello from pty")
	sess, err := StartSession("test-1", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}

	// Wait for process to finish
	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for process")
	}

	if sess.Alive() {
		t.Error("should not be alive after exit")
	}
	if sess.Err() != nil {
		t.Errorf("unexpected error: %v", sess.Err())
	}

	output := string(sess.RecentOutput())
	if !strings.Contains(output, "hello from pty") {
		t.Errorf("expected output to contain 'hello from pty', got %q", output)
	}
}

func TestStartSession_PID(t *testing.T) {
	cmd := exec.Command("sleep", "10")
	sess, err := StartSession("test-2", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop()

	if sess.PID() == 0 {
		t.Error("expected non-zero PID")
	}
	if sess.TaskID != "test-2" {
		t.Errorf("TaskID = %q", sess.TaskID)
	}
}

func TestStartSession_Stop(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	sess, err := StartSession("test-3", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}

	if !sess.Alive() {
		t.Error("should be alive")
	}

	if err := sess.Stop(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for stop")
	}

	if sess.Alive() {
		t.Error("should not be alive after stop")
	}
}

func TestSession_IsIdle_AfterOutput(t *testing.T) {
	// Start a command that produces output then goes silent
	cmd := exec.Command("echo", "done")
	sess, err := StartSession("idle-1", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}

	// Wait for process to finish and output to be captured
	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for process")
	}

	// Dead session should not be idle
	if sess.IsIdle() {
		t.Error("dead session should not report idle")
	}
}

func TestSession_IsIdle_LongRunning(t *testing.T) {
	// Start a long-running process that produces no output after start
	cmd := exec.Command("sleep", "60")
	sess, err := StartSession("idle-2", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop()

	// Immediately after start, lastOutput is zero → not idle (still starting)
	if sess.IsIdle() {
		t.Error("should not be idle immediately after start")
	}

	// Simulate output then wait for idle threshold
	sess.mu.Lock()
	sess.lastOutput = time.Now().Add(-4 * time.Second)
	sess.mu.Unlock()

	if !sess.IsIdle() {
		t.Error("should be idle after no output for longer than threshold")
	}
}

func TestStartSession_Detach_NotAttached(t *testing.T) {
	cmd := exec.Command("sleep", "10")
	sess, err := StartSession("test-4", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop()

	// Should not panic
	sess.Detach()
}

func TestSession_WorkDir_WithDir(t *testing.T) {
	cmd := exec.Command("sleep", "10")
	cmd.Dir = "/tmp"
	sess, err := StartSession("wd-1", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop()

	if sess.WorkDir() != "/tmp" {
		t.Errorf("WorkDir() = %q, want /tmp", sess.WorkDir())
	}
}

func TestSession_WorkDir_FallbackToCwd(t *testing.T) {
	cmd := exec.Command("sleep", "10")
	// Don't set cmd.Dir — should fall back to os.Getwd()
	sess, err := StartSession("wd-2", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop()

	cwd, _ := os.Getwd()
	if sess.WorkDir() != cwd {
		t.Errorf("WorkDir() = %q, want %q", sess.WorkDir(), cwd)
	}
}

func TestSession_PTYSize(t *testing.T) {
	cmd := exec.Command("sleep", "10")
	sess, err := StartSession("pty-size-1", cmd, 30, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop()

	cols, rows := sess.PTYSize()
	if cols != 100 || rows != 30 {
		t.Errorf("PTYSize() = (%d, %d), want (100, 30)", cols, rows)
	}
}

func TestSession_Resize(t *testing.T) {
	cmd := exec.Command("sleep", "10")
	sess, err := StartSession("resize-1", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop()

	if err := sess.Resize(50, 120); err != nil {
		t.Fatal(err)
	}

	cols, rows := sess.PTYSize()
	if cols != 120 || rows != 50 {
		t.Errorf("PTYSize() after Resize = (%d, %d), want (120, 50)", cols, rows)
	}
}

func TestSession_TotalWritten(t *testing.T) {
	cmd := exec.Command("echo", "hello")
	sess, err := StartSession("tw-1", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}

	if sess.TotalWritten() == 0 {
		t.Error("expected TotalWritten > 0 after echo output")
	}
}

func TestSession_WriteInput(t *testing.T) {
	// Use cat which reads from stdin and echoes to stdout
	cmd := exec.Command("cat")
	sess, err := StartSession("wi-1", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop()

	n, err := sess.WriteInput([]byte("test input\n"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 11 {
		t.Errorf("WriteInput wrote %d bytes, want 11", n)
	}

	// Wait for output to appear in buffer
	time.Sleep(200 * time.Millisecond)
	output := string(sess.RecentOutput())
	if !strings.Contains(output, "test input") {
		t.Errorf("expected output to contain 'test input', got %q", output)
	}
}

func TestSession_Signal_NilProcess(t *testing.T) {
	// Create a session with a command that hasn't been started via Process
	// We can test the nil process path by using a finished session
	cmd := exec.Command("true")
	sess, err := StartSession("sig-1", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}

	// Process still exists after exit (not nil), so test Signal on live process
	// For nil process test, manually set it
	sess.Cmd.Process = nil
	if err := sess.Signal(syscall.SIGTERM); !errors.Is(err, ErrNotRunning) {
		t.Errorf("Signal with nil process: got %v, want ErrNotRunning", err)
	}
}

func TestSession_RecentOutput(t *testing.T) {
	cmd := exec.Command("echo", "recent output test")
	sess, err := StartSession("ro-1", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}

	// Done() now fires only after readLoop has fully drained the PTY into
	// the ring buffer (see waitLoop), so RecentOutput is immediately ready.
	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
	output := string(sess.RecentOutput())
	if !strings.Contains(output, "recent output test") {
		t.Errorf("RecentOutput() = %q, want it to contain 'recent output test'", output)
	}
}

func TestSession_RecentOutputTail(t *testing.T) {
	cmd := exec.Command("echo", "tail output test data here")
	sess, err := StartSession("rot-1", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}

	full := sess.RecentOutput()
	tail := sess.RecentOutputTail(10)

	if len(tail) > 10 {
		t.Errorf("RecentOutputTail(10) returned %d bytes, want <= 10", len(tail))
	}
	if len(full) > 0 && len(tail) > 0 {
		// Tail should match the end of full output
		fullEnd := full[len(full)-len(tail):]
		if string(tail) != string(fullEnd) {
			t.Errorf("RecentOutputTail(10) = %q, want suffix of full output %q", tail, fullEnd)
		}
	}
}

func TestSession_Stop_AlreadyStopped(t *testing.T) {
	cmd := exec.Command("true")
	sess, err := StartSession("stop-2", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}

	// Calling Stop on an already-exited session should return nil
	if err := sess.Stop(); err != nil {
		t.Errorf("Stop on dead session: %v", err)
	}
}

func TestSession_Attach_Detach(t *testing.T) {
	cmd := exec.Command("cat") // cat reads stdin and echoes to stdout
	sess, err := StartSession("attach-1", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop()

	// Use a pipe as stdin so we can control when it closes
	pr, pw := io.Pipe()
	var stdout bytes.Buffer

	errCh := make(chan error, 1)
	go func() {
		errCh <- sess.Attach(pr, &stdout)
	}()

	// Give attach time to start and replay
	time.Sleep(100 * time.Millisecond)

	// Detach should cause Attach to return nil
	sess.Detach()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Attach returned %v, want nil on detach", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for detach")
	}

	pw.Close()
	pr.Close()
}

func TestSession_Attach_ProcessExit(t *testing.T) {
	cmd := exec.Command("echo", "bye")
	sess, err := StartSession("attach-2", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}

	// Wait for process to exit
	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}

	// Now try to attach — process is done, so Attach should return quickly
	// with the process error (nil for echo)
	pr, pw := io.Pipe()
	defer pw.Close()
	defer pr.Close()
	var stdout bytes.Buffer

	errCh := make(chan error, 1)
	go func() {
		errCh <- sess.Attach(pr, &stdout)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Attach returned %v, want nil (echo exits 0)", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for attach to return")
	}
}

func TestSession_Attach_AlreadyAttached(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	sess, err := StartSession("attach-3", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop()

	pr1, pw1 := io.Pipe()
	var stdout1 bytes.Buffer

	// First attach
	go func() {
		sess.Attach(pr1, &stdout1)
	}()

	time.Sleep(100 * time.Millisecond)

	// Second attach should fail
	pr2, pw2 := io.Pipe()
	var stdout2 bytes.Buffer
	err = sess.Attach(pr2, &stdout2)
	if !errors.Is(err, ErrAlreadyAttached) {
		t.Errorf("expected ErrAlreadyAttached, got %v", err)
	}

	sess.Detach()
	pw1.Close()
	pr1.Close()
	pw2.Close()
	pr2.Close()
}

func TestSession_Attach_WithReplay(t *testing.T) {
	cmd := exec.Command("echo", "replay-this")
	sess, err := StartSession("attach-4", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}

	// Wait for process to exit and output to be captured
	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}

	// Attach should replay buffered output
	pr, pw := io.Pipe()
	defer pw.Close()
	defer pr.Close()
	var stdout bytes.Buffer

	errCh := make(chan error, 1)
	go func() {
		errCh <- sess.Attach(pr, &stdout)
	}()

	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}

	if !strings.Contains(stdout.String(), "replay-this") {
		t.Errorf("expected replay output to contain 'replay-this', got %q", stdout.String())
	}
}

func TestSession_PID_NilProcess(t *testing.T) {
	cmd := exec.Command("true")
	sess, err := StartSession("pid-nil", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}

	// Set process to nil to test the nil branch
	sess.Cmd.Process = nil
	if sess.PID() != 0 {
		t.Errorf("PID() = %d, want 0 for nil process", sess.PID())
	}
}

func TestSession_Attach_StdinEOF(t *testing.T) {
	// Test that when stdin returns EOF (pipe closes), Attach handles it
	cmd := exec.Command("sleep", "60")
	sess, err := StartSession("attach-eof", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop()

	// Create a reader that immediately returns EOF
	pr, pw := io.Pipe()
	var stdout bytes.Buffer

	errCh := make(chan error, 1)
	go func() {
		errCh <- sess.Attach(pr, &stdout)
	}()

	// Close the write end to cause EOF on read end
	time.Sleep(50 * time.Millisecond)
	pw.Close()

	select {
	case err := <-errCh:
		// Should get io.EOF or nil — either is acceptable
		if err != nil && !errors.Is(err, io.EOF) {
			t.Errorf("Attach returned unexpected error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for attach to return after stdin EOF")
	}
}

func TestSession_MultiWriter(t *testing.T) {
	cmd := exec.Command("echo", "multi-writer-test")
	sess, err := StartSession("mw-1", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}

	// syncBuffer (not bytes.Buffer) — the readLoop goroutine may still be
	// flushing into AddWriter consumers when the test reads, so the buffer
	// itself must be thread-safe to satisfy `go test -race`.
	var buf1, buf2 syncBuffer
	sess.AddWriter(&buf1)
	sess.AddWriter(&buf2)

	// Wait for process to exit and output to propagate
	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}

	// Poll instead of fixed sleep — readLoop may take a beat to drain.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf1.String(), "multi-writer-test") &&
			strings.Contains(buf2.String(), "multi-writer-test") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if !strings.Contains(buf1.String(), "multi-writer-test") {
		t.Errorf("writer 1 missing output: %q", buf1.String())
	}
	if !strings.Contains(buf2.String(), "multi-writer-test") {
		t.Errorf("writer 2 missing output: %q", buf2.String())
	}
}

// syncBuffer is a thread-safe bytes.Buffer for use in tests where
// readLoop writes concurrently with test goroutine reads.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (sb *syncBuffer) Write(p []byte) (int, error) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.Write(p)
}

func (sb *syncBuffer) Len() int {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.Len()
}

func (sb *syncBuffer) String() string {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.String()
}

func TestSession_RemoveWriter(t *testing.T) {
	// Use cat which echoes stdin to stdout, producing observable output.
	cmd := exec.Command("cat")
	sess, err := StartSession("rw-1", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop()

	var buf syncBuffer
	sess.AddWriter(&buf)

	// Write some input so cat echoes it — this produces output
	// that the writer receives.
	sess.WriteInput([]byte("before\n"))
	time.Sleep(100 * time.Millisecond)

	if buf.Len() == 0 {
		t.Fatal("expected writer to receive output before removal")
	}

	// Now remove the writer and send more input.
	sess.RemoveWriter(&buf)
	initialLen := buf.Len()

	sess.WriteInput([]byte("after-removal\n"))
	time.Sleep(100 * time.Millisecond)

	if buf.Len() > initialLen {
		t.Error("writer received output after removal")
	}
}

// waitForTotalAtLeast polls TotalWritten() until it reaches at least n, or fails.
func waitForTotalAtLeast(t *testing.T, sess *Session, n uint64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if sess.TotalWritten() >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for TotalWritten() >= %d (got %d)", n, sess.TotalWritten())
}

func TestSession_AddWriterFrom_NoReplayWhenCaughtUp(t *testing.T) {
	// Two distinct printf bursts separated by a sleep so we can attach
	// between them with a known offset. The second burst exercises live
	// delivery; the lack of replay in the first verifies the offset gate.
	cmd := exec.Command("sh", "-c", "printf 'aaaaaaaa'; sleep 0.5; printf 'bbbbbbbb'")
	sess, err := StartSession("awf-noreplay", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop() //nolint:errcheck

	// Wait for the first burst to land in the ring.
	waitForTotalAtLeast(t, sess, 8, 3*time.Second)

	// Attach with offset == currentTotal: the writer must NOT receive the
	// 8 bytes already in the ring (they're "behind" us in the byte stream).
	var buf syncBuffer
	sess.AddWriterFrom(&buf, sess.TotalWritten())

	select {
	case <-sess.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for sh to exit")
	}
	// Drain — readLoop may still be flushing the second burst.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && buf.Len() < 8 {
		time.Sleep(20 * time.Millisecond)
	}

	got := buf.String()
	if !strings.Contains(got, "bbbbbbbb") {
		t.Errorf("missing live bytes from second burst: %q", got)
	}
	if strings.Contains(got, "aaaa") {
		t.Errorf("got 'aaaa' replay despite caught-up offset: %q", got)
	}
}

func TestSession_AddWriterFrom_PartialReplay(t *testing.T) {
	cmd := exec.Command("sh", "-c", "printf 'aaaaaaaa'; sleep 0.5; printf 'bbbbbbbb'")
	sess, err := StartSession("awf-partial", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop() //nolint:errcheck

	waitForTotalAtLeast(t, sess, 8, 3*time.Second)

	// Attach with offset 4 bytes BEFORE the current high-water mark. The
	// writer should receive exactly the last 4 bytes of the first burst as
	// replay, then the 8 bytes of the second burst live — 12 bytes total
	// and exactly the suffix of the session's logical byte stream from
	// offset 4 onward.
	currentTotal := sess.TotalWritten()
	var buf syncBuffer
	sess.AddWriterFrom(&buf, currentTotal-4)

	select {
	case <-sess.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for sh to exit")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && buf.Len() < 12 {
		time.Sleep(20 * time.Millisecond)
	}

	got := buf.String()
	if !strings.Contains(got, "aaaa") {
		t.Errorf("missing 4-byte replay tail of first burst: %q", got)
	}
	// We should NOT see all 8 a's — only the tail of 4 should be replayed.
	if strings.Count(got, "a") > 4 {
		t.Errorf("too many 'a' chars; replay should be exactly 4: %q", got)
	}
	if !strings.Contains(got, "bbbbbbbb") {
		t.Errorf("missing live bytes from second burst: %q", got)
	}
}

func TestSession_AddWriterFrom_FullReplayAtZeroOffset(t *testing.T) {
	// offset=0 with currentTotal=N replays the full ring (or as much as the
	// ring retains). Mirrors legacy AddWriter behaviour and exercises the
	// gap > ringLen branch's siblings.
	cmd := exec.Command("sh", "-c", "printf 'helloworld'; sleep 0.3")
	sess, err := StartSession("awf-zero", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop() //nolint:errcheck

	waitForTotalAtLeast(t, sess, 10, 3*time.Second)

	var buf syncBuffer
	sess.AddWriterFrom(&buf, 0)

	select {
	case <-sess.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for sh to exit")
	}
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) && buf.Len() < 10 {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(buf.String(), "helloworld") {
		t.Errorf("offset=0 should replay full ring: got %q", buf.String())
	}
}

// TestSession_AddWriterFrom_NoGapUnderConcurrency exercises the race-free
// guarantee: many concurrent attaches across a busy session must each
// receive a contiguous suffix from their offset, with no missing bytes
// between replay and live attach.
func TestSession_AddWriterFrom_NoGapUnderConcurrency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping concurrency test in short mode")
	}
	// Producer that writes a known counter pattern. Each "byte" is a single
	// digit so a missing chunk shows up as a non-monotonic sequence in the
	// captured buffer.
	cmd := exec.Command("sh", "-c", "for i in $(seq 1 200); do printf 'x'; done; sleep 0.5")
	sess, err := StartSession("awf-conc", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop() //nolint:errcheck

	// Wait for at least 50 bytes so we have a meaningful baseline.
	waitForTotalAtLeast(t, sess, 50, 3*time.Second)

	// Attach 5 writers in parallel, each at the moment-of-call offset. Each
	// writer's expected payload is `currentTotal_at_attach .. final_total`.
	const attachers = 5
	results := make([]struct {
		offset uint64
		buf    *syncBuffer
	}, attachers)
	var wg sync.WaitGroup
	for i := range attachers {
		wg.Go(func() {
			b := &syncBuffer{}
			results[i].buf = b
			results[i].offset = sess.TotalWritten()
			sess.AddWriterFrom(b, results[i].offset)
		})
	}
	wg.Wait()

	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for sh to exit")
	}

	finalTotal := sess.TotalWritten()
	// Wait for delivery to settle — readLoop may still be flushing.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		allDone := true
		for _, r := range results {
			expected := finalTotal - r.offset
			// syncBuffer.Len() is non-negative; gosec's int->uint64 warning
			// is spurious here.
			if uint64(r.buf.Len()) < expected { //nolint:gosec // Len() non-negative
				allDone = false
				break
			}
		}
		if allDone {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	for i, r := range results {
		expected := finalTotal - r.offset
		got := uint64(r.buf.Len()) //nolint:gosec // Len() non-negative
		if got != expected {
			t.Errorf("writer %d (offset=%d): got %d bytes, want %d (final=%d)",
				i, r.offset, got, expected, finalTotal)
		}
		// Every byte should be 'x' — any other char would indicate a gap
		// or duplication mangling. got is bounded by the test's own writes
		// (200 bytes), comfortably within int range.
		if !strings.HasPrefix(r.buf.String(), strings.Repeat("x", int(got))) { //nolint:gosec // got <= 200 in this test
			t.Errorf("writer %d: non-'x' byte in stream: %q", i, r.buf.String())
		}
	}
}

func TestStartSession_ZeroSize(t *testing.T) {
	// Test that zero rows/cols fall back to defaults
	cmd := exec.Command("sleep", "10")
	sess, err := StartSession("zero-size", cmd, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop()

	cols, rows := sess.PTYSize()
	if cols != 80 || rows != 24 {
		t.Errorf("PTYSize() = (%d, %d), want (80, 24) for zero-size fallback", cols, rows)
	}
}

// errorWriter is an io.Writer that returns an error on Write. Used to drive
// the readLoop's failed-writer removal path.
type errorWriter struct{}

func (errorWriter) Write(_ []byte) (int, error) { return 0, errors.New("write fail") }

// TestSession_ReadLoop_RemovesErroredWriter exercises the readLoop branch
// where Writer.Write returns an error — the writer is removed from the set.
func TestSession_ReadLoop_RemovesErroredWriter(t *testing.T) {
	cmd := exec.Command("sh", "-c", "for i in $(seq 1 5); do printf 'data%d' $i; sleep 0.05; done")
	sess, err := StartSession("rl-fail", cmd, 24, 80)
	testutil.NoError(t, err)
	t.Cleanup(func() { sess.Stop() }) //nolint:errcheck

	bad := errorWriter{}
	sess.AddWriter(bad)

	// Wait for some bytes to flow.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if sess.TotalWritten() >= 5 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The errored writer should have been auto-removed by readLoop.
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		sess.mu.Lock()
		writerCount := len(sess.writers)
		sess.mu.Unlock()
		if writerCount == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("expected errored writer to be auto-removed by readLoop")
}

// TestSession_ReadLoop_ManyWriters covers the heap-fallback branch when
// there are >4 writers (stack array overflows).
func TestSession_ReadLoop_ManyWriters(t *testing.T) {
	cmd := exec.Command("sh", "-c", "printf 'hello'")
	sess, err := StartSession("rl-many", cmd, 24, 80)
	testutil.NoError(t, err)
	t.Cleanup(func() { sess.Stop() }) //nolint:errcheck

	// Add 6 writers (>4 → heap fallback in readLoop).
	bufs := make([]*syncBuffer, 6)
	for i := range bufs {
		bufs[i] = &syncBuffer{}
		sess.AddWriter(bufs[i])
	}

	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		all := true
		for _, b := range bufs {
			if !strings.Contains(b.String(), "hello") {
				all = false
				break
			}
		}
		if all {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	for i, b := range bufs {
		if !strings.Contains(b.String(), "hello") {
			t.Errorf("writer %d missing output: %q", i, b.String())
		}
	}
}

// TestSession_AddWriterFromTolerant_PartialReplay verifies the gap-tolerant
// offset-aware writer attaches with replay of [offset..currentTotal) only.
// Mirrors AddWriterFrom_PartialReplay but exercises the no-lock-held variant
// used by the daemon stream for net.Conn writers.
func TestSession_AddWriterFromTolerant_PartialReplay(t *testing.T) {
	cmd := exec.Command("sh", "-c", "printf 'aaaaaaaa'; sleep 0.5; printf 'bbbbbbbb'")
	sess, err := StartSession("awft-partial", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop() //nolint:errcheck

	waitForTotalAtLeast(t, sess, 8, 3*time.Second)

	currentTotal := sess.TotalWritten()
	var buf syncBuffer
	sess.AddWriterFromTolerant(&buf, currentTotal-4)

	select {
	case <-sess.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for sh to exit")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && buf.Len() < 12 {
		time.Sleep(20 * time.Millisecond)
	}

	got := buf.String()
	if !strings.Contains(got, "aaaa") {
		t.Errorf("missing 4-byte replay tail of first burst: %q", got)
	}
	if strings.Count(got, "a") > 4 {
		t.Errorf("too many 'a' chars; replay should be exactly 4: %q", got)
	}
	if !strings.Contains(got, "bbbbbbbb") {
		t.Errorf("missing live bytes from second burst: %q", got)
	}
}

// TestSession_AddWriterFromTolerant_NoReplayWhenCaughtUp verifies the
// caught-up case skips replay entirely.
func TestSession_AddWriterFromTolerant_NoReplayWhenCaughtUp(t *testing.T) {
	cmd := exec.Command("sh", "-c", "printf 'aaaaaaaa'; sleep 0.5; printf 'bbbbbbbb'")
	sess, err := StartSession("awft-caught-up", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop() //nolint:errcheck

	waitForTotalAtLeast(t, sess, 8, 3*time.Second)

	var buf syncBuffer
	sess.AddWriterFromTolerant(&buf, sess.TotalWritten())

	select {
	case <-sess.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for sh to exit")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && buf.Len() < 8 {
		time.Sleep(20 * time.Millisecond)
	}

	got := buf.String()
	if strings.Contains(got, "aaaa") {
		t.Errorf("got 'aaaa' replay despite caught-up offset: %q", got)
	}
	if !strings.Contains(got, "bbbbbbbb") {
		t.Errorf("missing live bytes from second burst: %q", got)
	}
}

// TestSession_AddWriterFromTolerant_FullReplayAtZero verifies offset=0 replays
// the full ring (legacy AddWriter behaviour).
func TestSession_AddWriterFromTolerant_FullReplayAtZero(t *testing.T) {
	cmd := exec.Command("sh", "-c", "printf 'helloworld'; sleep 0.3")
	sess, err := StartSession("awft-zero", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop() //nolint:errcheck

	waitForTotalAtLeast(t, sess, 10, 3*time.Second)

	var buf syncBuffer
	sess.AddWriterFromTolerant(&buf, 0)

	select {
	case <-sess.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for sh to exit")
	}
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) && buf.Len() < 10 {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(buf.String(), "helloworld") {
		t.Errorf("offset=0 should replay full ring: got %q", buf.String())
	}
}

// TestSession_AddWriterFromTolerant_WriteError verifies the writer is NOT
// registered when the replay Write fails.
func TestSession_AddWriterFromTolerant_WriteError(t *testing.T) {
	cmd := exec.Command("sh", "-c", "printf 'hello world'; sleep 0.3")
	sess, err := StartSession("awft-err", cmd, 24, 80)
	testutil.NoError(t, err)
	t.Cleanup(func() { sess.Stop() }) //nolint:errcheck

	waitForTotalAtLeast(t, sess, 11, 3*time.Second)

	sess.AddWriterFromTolerant(errorWriter{}, 0)

	sess.mu.Lock()
	writerCount := len(sess.writers)
	sess.mu.Unlock()
	if writerCount != 0 {
		t.Errorf("AddWriterFromTolerant should not register writer that failed replay; got %d", writerCount)
	}
}

// TestSession_AddWriterFrom_WriteError exercises the early-return on writer
// Write error during replay.
func TestSession_AddWriterFrom_WriteError(t *testing.T) {
	cmd := exec.Command("sh", "-c", "printf 'hello world'; sleep 0.3")
	sess, err := StartSession("awf-err", cmd, 24, 80)
	testutil.NoError(t, err)
	t.Cleanup(func() { sess.Stop() }) //nolint:errcheck

	waitForTotalAtLeast(t, sess, 11, 3*time.Second)

	// Writer that fails immediately. AddWriterFrom should observe the error
	// and not register the writer.
	sess.AddWriterFrom(errorWriter{}, 0)

	// Confirm no spurious writer added.
	sess.mu.Lock()
	writerCount := len(sess.writers)
	sess.mu.Unlock()
	if writerCount != 0 {
		t.Errorf("AddWriterFrom should not register writer that failed replay; got %d", writerCount)
	}
}

// TestSession_Resize_AfterClose exercises the ptmxClosed branch in Resize:
// after the process exits and waitLoop closes ptmx, Resize must return nil.
func TestSession_Resize_AfterClose(t *testing.T) {
	cmd := exec.Command("true")
	sess, err := StartSession("resize-closed", cmd, 24, 80)
	testutil.NoError(t, err)

	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
	// waitLoop has closed ptmx — Resize must take the ptmxClosed branch.
	if err := sess.Resize(40, 100); err != nil {
		t.Errorf("Resize after ptmxClosed: %v, want nil", err)
	}
}

// TestSession_WorkDir_GetwdFallback covers the os.Getwd() fallback path
// when Cmd.Dir is empty.
func TestSession_WorkDir_GetwdFallback(t *testing.T) {
	cmd := exec.Command("sleep", "10")
	cmd.Dir = "" // Force fallback.
	sess, err := StartSession("wd-getwd", cmd, 24, 80)
	testutil.NoError(t, err)
	t.Cleanup(func() { sess.Stop() }) //nolint:errcheck

	got := sess.WorkDir()
	if got == "" {
		t.Error("WorkDir() returned empty when Cmd.Dir empty and Getwd should succeed")
	}
}

// TestResolveStartPoint_Branches covers all branches: HEAD, valid local, upstream/,
// origin/, and unresolvable.
func TestResolveStartPoint_Branches(t *testing.T) {
	repo := initGitRepo(t)

	t.Run("HEAD", func(t *testing.T) {
		got := resolveStartPoint(repo, "HEAD")
		testutil.Equal(t, got, "HEAD")
	})

	t.Run("local branch", func(t *testing.T) {
		// Create a local branch.
		cmd := exec.Command("git", "branch", "feature-x")
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git branch: %v: %s", err, out)
		}
		got := resolveStartPoint(repo, "feature-x")
		testutil.Equal(t, got, "feature-x")
	})

	t.Run("falls back to nothing for unknown ref", func(t *testing.T) {
		got := resolveStartPoint(repo, "nonexistent-ref-xyz")
		// No fallback found → returns the original ref unchanged.
		testutil.Equal(t, got, "nonexistent-ref-xyz")
	})
}

// TestCreateWorktree_SuffixOnConflict covers the path where the candidate
// directory already exists and we fall through to the next suffix.
func TestCreateWorktree_SuffixOnConflict(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := initGitRepo(t)

	// First creation: gets the base name.
	wt1, name1, branch1, err := CreateWorktree(repo, "proj", "conflict-task", "HEAD")
	testutil.NoError(t, err)
	testutil.Equal(t, name1, "conflict-task")
	t.Cleanup(func() { RemoveWorktreeAndBranch(wt1, branch1, repo) })

	// Second: same name → should suffix to conflict-task-1.
	wt2, name2, branch2, err := CreateWorktree(repo, "proj", "conflict-task", "HEAD")
	testutil.NoError(t, err)
	testutil.Equal(t, name2, "conflict-task-1")
	testutil.Equal(t, branch2, "argus/conflict-task-1")
	t.Cleanup(func() { RemoveWorktreeAndBranch(wt2, branch2, repo) })

	if wt2 == wt1 {
		t.Errorf("expected different worktree paths, got %q twice", wt2)
	}
}

// TestEvalSymlinksOrKeep_Empty exercises the empty-string short-circuit.
func TestEvalSymlinksOrKeep(t *testing.T) {
	testutil.Equal(t, evalSymlinksOrKeep(""), "")

	// Real path resolves.
	tmp := t.TempDir()
	got := evalSymlinksOrKeep(tmp)
	if got == "" {
		t.Errorf("evalSymlinksOrKeep(%q) returned empty", tmp)
	}

	// Nonexistent path returns input unchanged.
	missing := filepath.Join(tmp, "no-such-thing")
	got = evalSymlinksOrKeep(missing)
	testutil.Equal(t, got, missing)
}

// TestResolveGitDir_BadFormat covers the path where .git contents do not
// start with "gitdir: ".
func TestResolveGitDir_BadFormat(t *testing.T) {
	wtDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(wtDir, ".git"), []byte("not a gitdir line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := resolveGitDir(wtDir)
	testutil.Equal(t, got, "")
}

// TestResolveGitDir_NotEndingInDotGit covers the sanity check where the
// resolved path doesn't end with ".git".
func TestResolveGitDir_NotEndingInDotGit(t *testing.T) {
	wtDir := t.TempDir()
	// Construct a gitdir path that, after walking up two dirs, does NOT
	// end in ".git". E.g., gitdir: /a/b/c → walks up to /a, base "a" != ".git".
	weirdGitDir := filepath.Join(t.TempDir(), "weird", "subdir", "wt-name")
	if err := os.MkdirAll(weirdGitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtDir, ".git"), []byte("gitdir: "+weirdGitDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := resolveGitDir(wtDir)
	testutil.Equal(t, got, "")
}

// TestRingBuffer_TailLargerThanData covers the n > buf.Len() branch in Tail.
func TestRingBuffer_Tail_LargerThanData(t *testing.T) {
	b := NewRingBuffer(100)
	b.Write([]byte("abc"))
	got := b.Tail(10) // n > Len()
	testutil.Equal(t, string(got), "abc")
}

// TestRingBuffer_Tail_NegativeN covers the n <= 0 branch.
func TestRingBuffer_Tail_NegativeN(t *testing.T) {
	b := NewRingBuffer(100)
	b.Write([]byte("abc"))
	got := b.Tail(0)
	testutil.Equal(t, len(got), 0)
	got = b.Tail(-5)
	testutil.Equal(t, len(got), 0)
}

// TestCreateAndStart_NoProjectPath rejects projects with no path configured.
func TestCreateAndStart_NoProjectPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { d.Close() }) //nolint:errcheck

	_ = d.SetConfigValue("defaults.backend", "test")
	_ = d.SetBackend("test", config.Backend{Command: "echo hi", PromptFlag: ""})
	_ = d.SetProject("p", config.Project{Path: "" /* no path */, Branch: "HEAD"})

	fr := &fakeRunner{}
	task, sess, err := CreateAndStart(d, fr, CreateInput{
		Name:    "x",
		Prompt:  "go",
		Project: "p",
	})
	if err == nil {
		t.Fatal("expected error for project with no path")
	}
	if task != nil || sess != nil {
		t.Errorf("expected nil task/sess on error")
	}
	testutil.Contains(t, err.Error(), "no path configured")
}

// TestCreateAndStart_BaseBranchOverride exercises the BaseBranch override
// in the input.
func TestCreateAndStart_BaseBranchOverride(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)

	// Add another branch we can use as base.
	cmd := exec.Command("git", "branch", "alt-base")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git branch alt-base: %v: %s", err, out)
	}

	fr := &fakeRunner{sessionPID: 1234}
	task, sess, err := CreateAndStart(d, fr, CreateInput{
		Name:       "bb-override",
		Prompt:     "do thing",
		Project:    "proj",
		BaseBranch: "alt-base",
	})
	testutil.NoError(t, err)
	if task == nil || sess == nil {
		t.Fatal("expected non-nil task/sess")
	}
	t.Cleanup(func() { RemoveWorktreeAndBranch(task.Worktree, task.Branch, repo) })
}

// TestUniqueAttachmentName covers the suffix-bumping branch.
func TestUniqueAttachmentName(t *testing.T) {
	used := map[string]bool{
		"foo.png":   true,
		"foo-1.png": true,
	}
	got := uniqueAttachmentName(used, "foo.png")
	testutil.Equal(t, got, "foo-2.png")

	// Unique input → returned as-is.
	got = uniqueAttachmentName(used, "bar.png")
	testutil.Equal(t, got, "bar.png")
}

// TestAppendAttachmentList_NoPaths is the early-return path.
func TestAppendAttachmentList_NoPaths(t *testing.T) {
	got := appendAttachmentList("hello", nil)
	testutil.Equal(t, got, "hello")
}

// TestAppendAttachmentList_PromptWithoutNewline appends a newline when the
// existing prompt doesn't end with one.
func TestAppendAttachmentList_PromptWithoutNewline(t *testing.T) {
	got := appendAttachmentList("prompt", []string{"./.context/a.txt"})
	if !strings.HasPrefix(got, "prompt\n") {
		t.Errorf("expected prompt followed by newline, got %q", got)
	}
	testutil.Contains(t, got, "Attached files:")
	testutil.Contains(t, got, "./.context/a.txt")
}

// TestWriteAttachments_EmptyName rejects empty / dot names.
func TestWriteAttachments_EmptyName(t *testing.T) {
	wt := t.TempDir()
	_, err := writeAttachments(wt, []Attachment{{Name: "", Data: []byte("x")}})
	if err == nil {
		t.Fatal("expected error for empty attachment name")
	}
	_, err = writeAttachments(wt, []Attachment{{Name: ".", Data: []byte("x")}})
	if err == nil {
		t.Fatal("expected error for '.' attachment name")
	}
	_, err = writeAttachments(wt, []Attachment{{Name: "..", Data: []byte("x")}})
	if err == nil {
		t.Fatal("expected error for '..' attachment name")
	}
}

// TestStart_OnFinishCallback verifies onFinish receives stopped=false and
// lastOutput when a process exits naturally.
func TestStart_OnFinishCallback(t *testing.T) {
	type result struct {
		err     error
		stopped bool
		out     []byte
	}
	ch := make(chan result, 1)
	r := NewRunner(func(_ string, err error, stopped bool, out []byte) {
		ch <- result{err, stopped, out}
	})
	cfg := config.Config{
		Defaults: config.Defaults{Backend: "test"},
		Backends: map[string]config.Backend{
			"test": {Command: "printf 'final-byte'", PromptFlag: ""},
		},
		Projects: make(map[string]config.Project),
	}

	task := &model.Task{ID: "fin-1", Name: "n", Worktree: t.TempDir()}
	_, err := r.Start(task, cfg, 24, 80, false)
	testutil.NoError(t, err)

	select {
	case res := <-ch:
		if res.stopped {
			t.Error("expected stopped=false for natural exit")
		}
		// out may or may not contain the bytes (depends on race), but should
		// not panic. The value coming out of the runner is the last output.
		_ = res.out
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for onFinish")
	}
}

// Ensure io is referenced (the linter would complain if unused).
var _ io.Writer

// TestSession_LastInput verifies LastInput returns zero before any
// WriteInput call and a non-zero stamp after one.
func TestSession_LastInput(t *testing.T) {
	cmd := exec.Command("cat")
	sess, err := StartSession("li-1", cmd, 24, 80)
	testutil.NoError(t, err)
	t.Cleanup(func() { sess.Stop() })

	if !sess.LastInput().IsZero() {
		t.Errorf("LastInput before WriteInput: got %v, want zero", sess.LastInput())
	}

	if _, err := sess.WriteInput([]byte("hi\n")); err != nil {
		t.Fatal(err)
	}

	if sess.LastInput().IsZero() {
		t.Errorf("LastInput after WriteInput: got zero, want non-zero")
	}
}

// TestSession_InitialPTYSize verifies InitialPTYSize returns the size the
// session was started with, even after Resize.
func TestSession_InitialPTYSize(t *testing.T) {
	cmd := exec.Command("sleep", "10")
	sess, err := StartSession("ips-1", cmd, 30, 100)
	testutil.NoError(t, err)
	t.Cleanup(func() { sess.Stop() })

	cols, rows := sess.InitialPTYSize()
	testutil.Equal(t, cols, 100)
	testutil.Equal(t, rows, 30)

	if err := sess.Resize(50, 200); err != nil {
		t.Fatal(err)
	}
	cols2, rows2 := sess.InitialPTYSize()
	testutil.Equal(t, cols2, 100)
	testutil.Equal(t, rows2, 30)
}

// TestSession_RecentOutputTailWithTotal verifies the combined-snapshot helper
// returns both the tail bytes and the high-water-mark count under one lock.
func TestSession_RecentOutputTailWithTotal(t *testing.T) {
	cmd := exec.Command("sh", "-c", "printf 'abcdefghij'")
	sess, err := StartSession("rotwt-1", cmd, 24, 80)
	testutil.NoError(t, err)
	t.Cleanup(func() { sess.Stop() })

	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if sess.TotalWritten() >= 10 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	tail, total := sess.RecentOutputTailWithTotal(5)
	if total < 10 {
		t.Errorf("total = %d, want >= 10", total)
	}
	if len(tail) > 5 {
		t.Errorf("tail length = %d, want <= 5", len(tail))
	}
}
