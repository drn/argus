package agent

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
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

	// Give readLoop time to capture output
	time.Sleep(50 * time.Millisecond)

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
	time.Sleep(50 * time.Millisecond)

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
	if err := sess.Signal(syscall.SIGTERM); err != ErrNotRunning {
		t.Errorf("Signal with nil process: got %v, want ErrNotRunning", err)
	}
}

func TestSession_RecentOutput(t *testing.T) {
	cmd := exec.Command("echo", "recent output test")
	sess, err := StartSession("ro-1", cmd, 24, 80)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
	// readLoop drains the PTY into the ring buffer asynchronously; wait for
	// it to land rather than relying on a fixed sleep (was flaky on CI).
	deadline := time.Now().Add(2 * time.Second)
	var output string
	for time.Now().Before(deadline) {
		output = string(sess.RecentOutput())
		if strings.Contains(output, "recent output test") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("RecentOutput() = %q, want it to contain 'recent output test'", output)
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
	// Wait for readLoop to drain rather than fixed sleep (CI-flake fix).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(string(sess.RecentOutput()), "tail output test") {
			break
		}
		time.Sleep(20 * time.Millisecond)
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
	if err != ErrAlreadyAttached {
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
	time.Sleep(50 * time.Millisecond)

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
		if err != nil && err != io.EOF {
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
