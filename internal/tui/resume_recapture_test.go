package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/claudesession"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func seedTUITranscript(t *testing.T, home, worktree, id string, mod time.Time) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "projects", claudesession.EncodeProjectDir(worktree))
	testutil.NoError(t, os.MkdirAll(dir, 0o755))
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte("{}\n"), 0o644))
	if !mod.IsZero() {
		testutil.NoError(t, os.Chtimes(filepath.Join(dir, id+".jsonl"), mod, mod))
	}
}

// TestApp_refreshResumeSessionID covers the TUI-side resume guard: on a resume
// in local mode it refreshes a Claude task's session ID to the newest worktree
// transcript; on a fresh start it is a no-op. (The underlying Claude-only /
// idempotent / never-blank behavior is unit-tested in internal/agent.)
func TestApp_refreshResumeSessionID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	app := New(d, agent.NewRunner(nil), false)

	wt := filepath.Join(home, "wt")
	original := "11111111-1111-7111-9111-111111111111"
	newest := "22222222-2222-7222-9222-222222222222"
	seedTUITranscript(t, home, wt, original, time.Now().Add(-1*time.Hour))
	seedTUITranscript(t, home, wt, newest, time.Now())

	t.Run("resume refreshes to newest", func(t *testing.T) {
		task := &model.Task{Name: "t", Project: "p", Worktree: wt, Backend: "claude", SessionID: original}
		testutil.NoError(t, d.Add(task))
		app.refreshResumeSessionID(task, true)
		testutil.Equal(t, task.SessionID, newest)
		got, err := d.Get(task.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, got.SessionID, newest)
	})

	t.Run("fresh start is a no-op", func(t *testing.T) {
		task := &model.Task{Name: "t2", Project: "p", Worktree: wt, Backend: "claude", SessionID: original}
		testutil.NoError(t, d.Add(task))
		app.refreshResumeSessionID(task, false) // resume=false → untouched
		testutil.Equal(t, task.SessionID, original)
	})
}
