package agent

import (
	"os/exec"
	"strings"
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
