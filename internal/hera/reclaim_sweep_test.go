package hera

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// fakeSessionChecker is the SessionChecker test double — a plain set of
// task IDs currently reporting a live session.
type fakeSessionChecker struct{ live map[string]bool }

func (f *fakeSessionChecker) HasSession(taskID string) bool { return f.live[taskID] }

func newLeakedWorktreeDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), ".argus", "worktrees", "wt")
	testutil.NoError(t, os.MkdirAll(dir, 0o755))
	return dir
}

// seedNukedTask adds task, binds it to a fresh worker role under a fresh
// orchestrator, then ends that binding with the cascade-nuke path's own end
// reason — the exact shape HeraNukeReclaimCandidates selects on.
func seedNukedTask(t *testing.T, d *db.DB, task *model.Task) {
	t.Helper()
	testutil.NoError(t, d.Add(task))
	orch, err := d.CreateHeraOrchestrator(task.Name+"-orch", "master")
	testutil.NoError(t, err)
	_, binding, err := d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID,
		Name:           "worker",
		Kind:           db.HeraKindWorker,
		ArgusProject:   task.Project,
	}, task.ID, task.Worktree)
	testutil.NoError(t, err)
	testutil.NoError(t, d.EndHeraBinding(binding.ID, db.HeraEndReasonUserDeleted))
}

func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	testutil.NoError(t, err)
	return tm
}

// --- git fixture helpers (mirrors internal/mergesafety's own private copies —
// unexported there, so duplicated here rather than shared across packages) ---

func initGitRepo(t *testing.T, dir string) string {
	t.Helper()
	if out, err := exec.Command("git", "init", "-b", "master", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	gitRun(t, dir, "config", "user.email", "test@example.com")
	gitRun(t, dir, "config", "user.name", "Test")
	gitRun(t, dir, "config", "commit.gpgsign", "false")
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# init\n"), 0o644))
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "init")
	return dir
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func addGitCommit(t *testing.T, dir, file, contents string) {
	t.Helper()
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, file), []byte(contents), 0o644))
	gitRun(t, dir, "add", file)
	gitRun(t, dir, "commit", "-m", "add "+file)
}

func gitBranchExists(dir, branch string) bool {
	err := exec.Command("git", "-C", dir, "rev-parse", "--verify", "--quiet", branch).Run()
	return err == nil
}

func TestReconcileHeraReclaims_LeakedWorktreeRemovedAndPruned(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	wt := newLeakedWorktreeDir(t)
	task := &model.Task{Name: "leaked", Status: model.StatusInReview, Archived: true, Worktree: wt, Branch: "argus/leaked"}
	seedNukedTask(t, d, task)

	sum, err := ReconcileHeraReclaims(d, &fakeSessionChecker{})
	testutil.NoError(t, err)
	testutil.Equal(t, sum.Candidates, 1)
	testutil.Equal(t, sum.WorktreesRetried, 1)
	testutil.Equal(t, sum.Pruned, 1)
	testutil.Equal(t, sum.SkippedLiveSession, 0)

	_, statErr := os.Stat(wt)
	testutil.True(t, os.IsNotExist(statErr))

	_, err = d.Get(task.ID)
	testutil.ErrorIs(t, err, db.ErrTaskNotFound)
}

func TestReconcileHeraReclaims_AlreadyReclaimedJustPrunes(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	// Worktree already gone (empty), no confirmed-safe stacked descendant —
	// nothing left to do but prune the row.
	task := &model.Task{Name: "already-gone", Status: model.StatusInReview, Archived: true}
	seedNukedTask(t, d, task)

	sum, err := ReconcileHeraReclaims(d, &fakeSessionChecker{})
	testutil.NoError(t, err)
	testutil.Equal(t, sum.Candidates, 1)
	testutil.Equal(t, sum.WorktreesRetried, 0)
	testutil.Equal(t, sum.StackedBranchesDeleted, 0)
	testutil.Equal(t, sum.Pruned, 1)

	_, err = d.Get(task.ID)
	testutil.ErrorIs(t, err, db.ErrTaskNotFound)
}

func TestReconcileHeraReclaims_LiveBindingNeverTouched(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	task := &model.Task{Name: "rebound", Status: model.StatusInProgress}
	seedNukedTask(t, d, task) // one binding already ended user_deleted

	// A second, LIVE binding under a different orchestrator/role for the SAME
	// task — e.g. re-adopted after the original nuke — must exclude it from
	// the candidate set entirely.
	orch2, err := d.CreateHeraOrchestrator("rebound-orch-2", "master")
	testutil.NoError(t, err)
	role2, err := d.CreateHeraRole(db.CreateHeraRoleInput{
		OrchestratorID: orch2.ID, Name: "worker-2", Kind: db.HeraKindWorker, ArgusProject: task.Project,
	})
	testutil.NoError(t, err)
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{
		RoleID: role2.ID, OrchestratorID: orch2.ID, ArgusTaskID: task.ID, WorktreePath: task.Worktree,
	})
	testutil.NoError(t, err)

	sum, err := ReconcileHeraReclaims(d, &fakeSessionChecker{})
	testutil.NoError(t, err)
	testutil.Equal(t, sum.Candidates, 0)
	testutil.Equal(t, sum.Pruned, 0)

	got, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.ID, task.ID)
}

func TestReconcileHeraReclaims_LiveSessionRetriesButDoesNotPrune(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	wt := newLeakedWorktreeDir(t)
	task := &model.Task{Name: "still-live", Status: model.StatusInProgress, Worktree: wt, Branch: "argus/still-live"}
	seedNukedTask(t, d, task)

	runner := &fakeSessionChecker{live: map[string]bool{task.ID: true}}
	sum, err := ReconcileHeraReclaims(d, runner)
	testutil.NoError(t, err)
	testutil.Equal(t, sum.Candidates, 1)
	testutil.Equal(t, sum.WorktreesRetried, 1)
	testutil.Equal(t, sum.Pruned, 0)
	testutil.Equal(t, sum.SkippedLiveSession, 1)

	_, statErr := os.Stat(wt)
	testutil.True(t, os.IsNotExist(statErr))

	got, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.ID, task.ID)
}

// TestReconcileHeraReclaims_ExcludedStackedBranchStays exercises the
// secondary, best-effort stacked-branch piece (design.md Decision D5): task X
// (the nuked candidate) has a descendant chain X -> M -> Tip, where only
// Tip ever merged into master. Once Tip classifies confirmed-safe (Tier A,
// local ancestor), both X's own branch and M's branch resolve stack-inferred
// safe — but M's branch was recorded operator-excluded, so it must survive
// untouched while X's is deleted.
func TestReconcileHeraReclaims_ExcludedStackedBranchStays(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	dir := initGitRepo(t, t.TempDir())
	gitRun(t, dir, "checkout", "-b", "x-branch")
	addGitCommit(t, dir, "x.txt", "x1\n")
	gitRun(t, dir, "checkout", "-b", "m-branch")
	addGitCommit(t, dir, "m.txt", "m1\n")
	gitRun(t, dir, "checkout", "-b", "tip-branch")
	addGitCommit(t, dir, "tip.txt", "tip1\n")
	gitRun(t, dir, "checkout", "master")
	gitRun(t, dir, "merge", "--no-ff", "tip-branch", "-m", "merge tip")
	// Simulate origin still holding both stale stacked branches (no fetch —
	// branchExistsOnOrigin is a purely local refs/remotes/ check).
	gitRun(t, dir, "update-ref", "refs/remotes/origin/x-branch", "x-branch")
	gitRun(t, dir, "update-ref", "refs/remotes/origin/m-branch", "m-branch")

	testutil.NoError(t, d.SetProject("proj", config.Project{Path: dir}))

	taskX := &model.Task{Name: "task-x", Project: "proj", Branch: "x-branch", CreatedAt: mustParseTime(t, "2026-01-01T00:00:00Z")}
	seedNukedTask(t, d, taskX)

	taskM := &model.Task{Name: "task-m", Project: "proj", Branch: "m-branch", BaseBranch: "x-branch", CreatedAt: mustParseTime(t, "2026-01-02T00:00:00Z")}
	testutil.NoError(t, d.Add(taskM))

	taskTip := &model.Task{Name: "task-tip", Project: "proj", Branch: "tip-branch", BaseBranch: "m-branch", CreatedAt: mustParseTime(t, "2026-01-03T00:00:00Z")}
	testutil.NoError(t, d.Add(taskTip))

	testutil.NoError(t, d.ExcludeCleanupBranch(taskM.ID, "m-branch"))

	sum, err := ReconcileHeraReclaims(d, &fakeSessionChecker{})
	testutil.NoError(t, err)
	testutil.Equal(t, sum.StackedBranchesDeleted, 1)

	testutil.Equal(t, gitBranchExists(dir, "x-branch"), false)
	testutil.Equal(t, gitBranchExists(dir, "m-branch"), true)
}
