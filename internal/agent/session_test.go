package agent

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"strings"
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

	output := string(sess.buf.Bytes())
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
	time.Sleep(50 * time.Millisecond)

	output := string(sess.RecentOutput())
	if !strings.Contains(output, "recent output test") {
		t.Errorf("RecentOutput() = %q, want it to contain 'recent output test'", output)
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
