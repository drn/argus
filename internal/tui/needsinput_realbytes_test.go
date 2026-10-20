package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// TestDetectNeedsInputSticky_SettledClearDoesNotSwallowLaterPermissionPrompt
// replays REAL captured Claude Code bytes (trimmed session-log slices under
// testdata/needsinput) through the production TUI detector. Ground truth: a
// Sketch worker parked on a Bash permission prompt (`❯ 1. Yes / 2. No`, "Tab to
// amend" footer) never showed "(?)" because an EARLIER prompt had been cleared
// by the BUG-072 settle path. That clear recorded a cleared marker with no
// content fingerprint (the tail had no prompt on it by then), and the
// timestamp-only fallback then suppressed every later prompt — lastUserInput
// never advances for an agent that is only ever answered by... nobody typing.
func TestDetectNeedsInputSticky_SettledClearDoesNotSwallowLaterPermissionPrompt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join("testdata", "needsinput", name))
		testutil.NoError(t, err)
		return b
	}
	prompt1, idle, prompt2 := read("bash_perm_prompt_1.bin"), read("idle_no_prompt.bin"), read("bash_perm_prompt_2.bin")

	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	const id = "realbytes"
	testutil.NoError(t, d.Add(&model.Task{ID: id, Name: "w", Status: model.StatusInProgress, Project: "p"}))
	testutil.NoError(t, os.MkdirAll(agent.SessionsDir(), 0o755))
	agent.SaveSessionSize(id, 88, 48)
	app := New(d, agent.NewRunner(nil), false)
	app.refreshTasks()

	var prev []string
	tick := func(content []byte, idleNow bool) {
		t.Helper()
		testutil.NoError(t, os.WriteFile(agent.SessionLogPath(id), content, 0o644))
		var idleIDs []string
		if idleNow {
			idleIDs = []string{id}
		}
		prev = app.detectNeedsInputSticky(idleIDs, []string{id}, prev)
	}

	for i := 0; i < 12; i++ { // first prompt parks long enough to flag
		tick(prompt1, false)
	}
	testutil.Equal(t, len(prev), 1)

	for i := 0; i < 4; i++ { // worker resolves it: idle, no prompt -> settle clear
		tick(idle, true)
	}
	testutil.Equal(t, len(prev), 0)

	for i := 0; i < 12; i++ { // a NEW permission prompt parks (user never typed)
		tick(prompt2, false)
	}
	if len(prev) != 1 {
		t.Fatalf("second permission prompt was swallowed by the settled-clear marker; flagged=%v", prev)
	}
}
