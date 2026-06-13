package main

import (
	"bytes"
	"io"
	"log"
	"log/slog"
	"os"
	"strings"
	"testing"
)

// TestConfigureProcessLogging_DoesNotReachStderr is the regression guard for
// the session-supervisor's logging discipline (CLAUDE.md rule 6): the supervisor
// forks PTY children, so slog.*/log.* must land in the supervisor.log writer,
// never on the process stderr. Mirrors uxlog's
// TestSlogWithUxlogWriter_DoesNotReachStderr.
func TestConfigureProcessLogging_DoesNotReachStderr(t *testing.T) {
	// Save process-global state we mutate; restore in LIFO before assertions
	// that read the buffer so late slog calls don't race the restore.
	origSlog := slog.Default()
	origLog := log.Writer()
	origStderr := os.Stderr
	t.Cleanup(func() {
		slog.SetDefault(origSlog)
		log.SetOutput(origLog)
		os.Stderr = origStderr
	})

	// Capture anything that hits stderr during this test.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w

	var buf bytes.Buffer
	configureProcessLogging(&buf)

	slog.Info("slog info from supervisor process")
	slog.Error("slog error from supervisor process", "task", "test-task")
	log.Printf("stdlib log print from supervisor process")

	// Restore before reading the pipe so no further writes target it.
	slog.SetDefault(origSlog)
	log.SetOutput(origLog)
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

	content := buf.String()
	for _, want := range []string{
		"slog info from supervisor process",
		"slog error from supervisor process",
		"stdlib log print from supervisor process",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("expected %q in supervisor log writer, got: %s", want, content)
		}
	}
}
