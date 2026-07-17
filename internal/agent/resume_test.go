package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drn/argus/internal/claudesession"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// seedClaudeTranscript writes a UUID-named transcript into the Claude project
// dir for worktree so claudesession.List discovers it, optionally stamping its
// mtime so newest-first ordering is deterministic.
func seedClaudeTranscript(t *testing.T, home, worktree, id string, mod time.Time) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "projects", claudesession.EncodeProjectDir(worktree))
	testutil.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, id+".jsonl")
	testutil.NoError(t, os.WriteFile(path, []byte("{}\n"), 0o644))
	if !mod.IsZero() {
		testutil.NoError(t, os.Chtimes(path, mod, mod))
	}
}

// newResumeTestDB returns an in-memory DB (default config has a claude backend).
func newResumeTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// TestRefreshResumeSessionID_ClaudeRefreshesToNewest is the core regression: a
// Claude task pinned to an older UUID whose worktree holds a newer transcript
// must be refreshed to the newest UUID, both in memory and persisted.
func TestRefreshResumeSessionID_ClaudeRefreshesToNewest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	d := newResumeTestDB(t)

	wt := filepath.Join(home, "wt")
	testutil.NoError(t, os.MkdirAll(wt, 0o755))

	original := "11111111-1111-7111-9111-111111111111"
	newest := "22222222-2222-7222-9222-222222222222"
	past := time.Now().Add(-1 * time.Hour)
	seedClaudeTranscript(t, home, wt, original, past)
	seedClaudeTranscript(t, home, wt, newest, time.Now())

	task := &model.Task{Name: "t", Project: "p", Worktree: wt, Backend: "claude", SessionID: original}
	testutil.NoError(t, d.Add(task))

	RefreshResumeSessionID(d, task)

	testutil.Equal(t, task.SessionID, newest) // in-memory task updated for the immediate resume
	got, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.SessionID, newest) // persisted
}

// TestRefreshResumeSessionID_NonClaudeUnchanged proves the Claude-only gate: a
// codex task is left byte-identical even if a Claude transcript happens to sit
// in the worktree's project dir.
func TestRefreshResumeSessionID_NonClaudeUnchanged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	d := newResumeTestDB(t)

	wt := filepath.Join(home, "wt")
	testutil.NoError(t, os.MkdirAll(wt, 0o755))
	seedClaudeTranscript(t, home, wt, "99999999-9999-7999-9999-999999999999", time.Now())

	original := "codex-original-id"
	task := &model.Task{Name: "t", Project: "p", Worktree: wt, Backend: "codex", SessionID: original}
	testutil.NoError(t, d.Add(task))

	RefreshResumeSessionID(d, task)

	testutil.Equal(t, task.SessionID, original)
	got, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.SessionID, original)
}

// TestRefreshResumeSessionID_ZeroTranscriptFallback: a Claude task whose
// worktree has no transcript keeps its recorded ID (never blanked).
func TestRefreshResumeSessionID_ZeroTranscriptFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	d := newResumeTestDB(t)

	wt := filepath.Join(home, "wt")
	testutil.NoError(t, os.MkdirAll(wt, 0o755))

	original := "11111111-1111-7111-9111-111111111111"
	task := &model.Task{Name: "t", Project: "p", Worktree: wt, Backend: "claude", SessionID: original}
	testutil.NoError(t, d.Add(task))

	RefreshResumeSessionID(d, task)

	testutil.Equal(t, task.SessionID, original)
	got, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.SessionID, original)
}

// TestRefreshResumeSessionID_EmptySessionIDNotFabricated: with no recorded ID,
// the helper must not derive or write one (first-start stays byte-identical).
func TestRefreshResumeSessionID_EmptySessionIDNotFabricated(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	d := newResumeTestDB(t)

	wt := filepath.Join(home, "wt")
	testutil.NoError(t, os.MkdirAll(wt, 0o755))
	seedClaudeTranscript(t, home, wt, "33333333-3333-7333-9333-333333333333", time.Now())

	task := &model.Task{Name: "t", Project: "p", Worktree: wt, Backend: "claude", SessionID: ""}
	testutil.NoError(t, d.Add(task))

	RefreshResumeSessionID(d, task)

	testutil.Equal(t, task.SessionID, "")
	got, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.SessionID, "")
}

// TestRefreshResumeSessionID_UnchangedNewestNoOp: when the newest transcript
// already equals the recorded ID, the helper is a clean no-op.
func TestRefreshResumeSessionID_UnchangedNewestNoOp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	d := newResumeTestDB(t)

	wt := filepath.Join(home, "wt")
	testutil.NoError(t, os.MkdirAll(wt, 0o755))
	id := "44444444-4444-7444-9444-444444444444"
	seedClaudeTranscript(t, home, wt, id, time.Now())

	task := &model.Task{Name: "t", Project: "p", Worktree: wt, Backend: "claude", SessionID: id}
	testutil.NoError(t, d.Add(task))

	RefreshResumeSessionID(d, task)

	testutil.Equal(t, task.SessionID, id)
	got, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.SessionID, id)
}

// TestRefreshResumeSessionID_NilAndNoWorktree: defensive guards never panic.
func TestRefreshResumeSessionID_NilAndNoWorktree(t *testing.T) {
	d := newResumeTestDB(t)
	RefreshResumeSessionID(d, nil) // nil task
	task := &model.Task{Name: "t", Project: "p", Worktree: "", Backend: "claude", SessionID: "x"}
	testutil.NoError(t, d.Add(task))
	RefreshResumeSessionID(d, task) // empty worktree → no-op
	testutil.Equal(t, task.SessionID, "x")
}
