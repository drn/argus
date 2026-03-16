package uxlog

import (
	"os"
	"path/filepath"
	"strings"
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
