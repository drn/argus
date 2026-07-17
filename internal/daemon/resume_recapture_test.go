package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drn/argus/internal/claudesession"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// seedResumeTranscript writes a UUID-named transcript into the Claude project
// dir for worktree so claudesession.List discovers it, stamping mtime so
// newest-first ordering is deterministic.
func seedResumeTranscript(t *testing.T, home, worktree, id string, mod time.Time) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "projects", claudesession.EncodeProjectDir(worktree))
	testutil.NoError(t, os.MkdirAll(dir, 0o755))
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte("{}\n"), 0o644))
	if !mod.IsZero() {
		testutil.NoError(t, os.Chtimes(filepath.Join(dir, id+".jsonl"), mod, mod))
	}
}

// TestReconcileOnStartup_Supervised_RefreshesOrphanClaudeSessionID pins the
// resume-time recapture at the supervisor-restart reconcile: a Claude worker
// orphaned by the reconcile (supervisor no longer reports it live) has its
// recorded session ID refreshed to the newest worktree transcript, so its
// later resume targets the most recent in-place session rather than the stale
// create-time UUID.
func TestReconcileOnStartup_Supervised_RefreshesOrphanClaudeSessionID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	d, _ := testDaemon(t)

	wt := filepath.Join(home, "orphan-wt")
	testutil.NoError(t, os.MkdirAll(wt, 0o755))
	original := "11111111-1111-7111-9111-111111111111"
	newest := "22222222-2222-7222-9222-222222222222"
	seedResumeTranscript(t, home, wt, original, time.Now().Add(-1*time.Hour))
	seedResumeTranscript(t, home, wt, newest, time.Now())

	orphan := &model.Task{
		Name:      "orphan",
		Project:   "proj",
		Status:    model.StatusInProgress,
		Backend:   "claude",
		Worktree:  wt,
		SessionID: original,
	}
	testutil.NoError(t, d.db.Add(orphan))

	// Supervisor reports NO live sessions (authoritative empty) → the task is a
	// true orphan: flipped to in_review and its session ID refreshed.
	fake := &fakeSupClient{running: []string{}}
	d.UseSupervisorRunner(fake)

	d.ReconcileOnStartup()

	got, err := d.db.Get(orphan.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, model.StatusInReview)
	testutil.Equal(t, got.SessionID, newest) // refreshed to the newest transcript
}

// TestReconcileOnStartup_Supervised_NonClaudeOrphanUnchanged proves the
// Claude-only gate at the reconcile seam: a codex orphan keeps its recorded
// session ID even if a Claude transcript happens to sit in the worktree's
// project dir.
func TestReconcileOnStartup_Supervised_NonClaudeOrphanUnchanged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	d, _ := testDaemon(t)

	wt := filepath.Join(home, "codex-wt")
	testutil.NoError(t, os.MkdirAll(wt, 0o755))
	seedResumeTranscript(t, home, wt, "99999999-9999-7999-9999-999999999999", time.Now())

	original := "codex-session-id"
	orphan := &model.Task{
		Name:      "codex-orphan",
		Project:   "proj",
		Status:    model.StatusInProgress,
		Backend:   "codex",
		Worktree:  wt,
		SessionID: original,
	}
	testutil.NoError(t, d.db.Add(orphan))

	fake := &fakeSupClient{running: []string{}}
	d.UseSupervisorRunner(fake)

	d.ReconcileOnStartup()

	got, err := d.db.Get(orphan.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, model.StatusInReview)
	testutil.Equal(t, got.SessionID, original) // codex resume semantics untouched
}
