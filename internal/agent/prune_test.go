package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestCountOrphanedWorktrees(t *testing.T) {
	wtRoot := filepath.Join(t.TempDir(), "worktrees")
	os.MkdirAll(filepath.Join(wtRoot, "proj1", "task-a"), 0o755) //nolint:errcheck
	os.MkdirAll(filepath.Join(wtRoot, "proj1", "task-b"), 0o755) //nolint:errcheck
	os.MkdirAll(filepath.Join(wtRoot, "proj2", "task-c"), 0o755) //nolint:errcheck

	known := map[string]bool{
		filepath.Join(wtRoot, "proj1", "task-a"): true,
	}

	count := CountOrphanedWorktrees(wtRoot, known)
	if count != 2 {
		t.Errorf("expected 2 orphans, got %d", count)
	}
}

func TestSweepOrphanedWorktrees(t *testing.T) {
	wtRoot := filepath.Join(t.TempDir(), ".argus", "worktrees")
	orphanPath := filepath.Join(wtRoot, "proj1", "orphan-task")
	os.MkdirAll(orphanPath, 0o755) //nolint:errcheck

	os.WriteFile(filepath.Join(orphanPath, "dummy.txt"), []byte("x"), 0o644) //nolint:errcheck

	known := map[string]bool{}

	swept := SweepOrphanedWorktrees(wtRoot, known, map[string]string{})
	if swept != 1 {
		t.Errorf("expected 1 swept, got %d", swept)
	}

	if DirExists(orphanPath) {
		t.Error("orphan directory should have been removed")
	}

	projDir := filepath.Join(wtRoot, "proj1")
	if DirExists(projDir) {
		t.Error("empty project directory should have been removed")
	}
}

// Regression: a stored worktree path with extra depth (e.g. legacy task names
// whose safe form contained a `/`, producing wtRoot/proj/foo-https-/github)
// must NOT be classified as an orphan at the wtRoot/<project>/<task> level.
// Without the ancestor guard, the walker `os.RemoveAll`s the parent dir and
// destroys the live worktree underneath, surfacing later as
// "worktree path missing" on resume.
func TestWalkOrphanedWorktrees_SkipsAncestorsOfKnown(t *testing.T) {
	wtRoot := filepath.Join(t.TempDir(), ".argus", "worktrees")
	deepWT := filepath.Join(wtRoot, "nexus", "Rebase-https-", "github")
	if err := os.MkdirAll(deepWT, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(deepWT, "marker"), []byte("alive"), 0o644) //nolint:errcheck

	known := map[string]bool{deepWT: true}

	t.Run("count skips ancestor", func(t *testing.T) {
		if got := CountOrphanedWorktrees(wtRoot, known); got != 0 {
			t.Errorf("expected 0 orphans, got %d", got)
		}
	})

	t.Run("sweep does not destroy ancestor", func(t *testing.T) {
		swept := SweepOrphanedWorktrees(wtRoot, known, map[string]string{"nexus": ""})
		if swept != 0 {
			t.Errorf("expected 0 swept, got %d", swept)
		}
		if !DirExists(deepWT) {
			t.Error("live worktree underneath the ancestor was destroyed")
		}
	})
}

// TestSweepOrphanedWorktrees_RealRepo exercises the full branch-deletion path
// that production hits — a populated projects map pointing at a real git
// repository. The simpler TestSweepOrphanedWorktrees only checks the path
// where repoDir is empty and the directory gets unconditionally removed by
// os.RemoveAll.
func TestSweepOrphanedWorktrees_RealRepo(t *testing.T) {
	repoDir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:gosec // G204: git subprocess with controlled args in test
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	runGit("init", "-q")
	runGit("config", "user.email", "test@test.com")
	runGit("config", "user.name", "Test")
	os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("hi"), 0o644) //nolint:errcheck
	runGit("add", ".")
	runGit("commit", "-q", "-m", "init")

	wtRoot := filepath.Join(t.TempDir(), ".argus", "worktrees")
	wtPath := filepath.Join(wtRoot, "proj1", "orphan-task")
	os.MkdirAll(filepath.Dir(wtPath), 0o755) //nolint:errcheck
	runGit("worktree", "add", "-b", "argus/orphan-task", wtPath, "HEAD")

	if !DirExists(wtPath) {
		t.Fatal("worktree setup failed")
	}
	checkBranch := func() bool {
		cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", "argus/orphan-task")
		cmd.Dir = repoDir
		return cmd.Run() == nil
	}
	if !checkBranch() {
		t.Fatal("argus/orphan-task branch should exist before sweep")
	}

	known := map[string]bool{}
	projects := map[string]string{"proj1": repoDir}

	swept := SweepOrphanedWorktrees(wtRoot, known, projects)
	if swept != 1 {
		t.Errorf("expected 1 swept, got %d", swept)
	}
	if DirExists(wtPath) {
		t.Error("worktree dir should have been removed")
	}
	if checkBranch() {
		t.Error("argus/orphan-task branch should have been deleted")
	}
}

func TestCountOrphanedWorktrees_NoneFound(t *testing.T) {
	root := t.TempDir()
	count := CountOrphanedWorktrees(root, map[string]bool{})
	testutil.Equal(t, count, 0)
}

func TestCountOrphanedWorktrees_DetectsOrphans(t *testing.T) {
	root := t.TempDir()

	orphan := filepath.Join(root, "proj", "orphan-task")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	count := CountOrphanedWorktrees(root, map[string]bool{})
	testutil.Equal(t, count, 1)
}

func TestSweepOrphanedWorktrees_Empty(t *testing.T) {
	root := t.TempDir()
	swept := SweepOrphanedWorktrees(root, map[string]bool{}, map[string]string{})
	testutil.Equal(t, swept, 0)
}

// TestPruneCompleted exercises the end-to-end shared prune flow:
// DB prune, worktree cleanup, and orphan sweep — without an in-process Runner.
func TestPruneCompleted(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	wtRoot := filepath.Join(t.TempDir(), ".argus", "worktrees")

	// One complete task with a worktree path on disk.
	completedWT := filepath.Join(wtRoot, "proj", "done")
	testutil.NoError(t, os.MkdirAll(completedWT, 0o755))
	testutil.NoError(t, d.Add(&model.Task{
		ID: "done-1", Name: "done", Status: model.StatusComplete,
		Project: "proj", Worktree: completedWT,
	}))
	// One active task — should NOT be pruned.
	testutil.NoError(t, d.Add(&model.Task{
		ID: "active-1", Name: "active", Status: model.StatusInProgress, Project: "proj",
	}))
	// An orphan dir under wtRoot/proj/ — should be swept.
	orphanWT := filepath.Join(wtRoot, "proj", "orphan")
	testutil.NoError(t, os.MkdirAll(orphanWT, 0o755))

	plan, err := PruneCompleted(d, PruneOptions{
		WtRoot:   wtRoot,
		Projects: map[string]string{"proj": ""}, // empty repoDir — branch deletion no-ops
	})
	testutil.NoError(t, err)
	testutil.Equal(t, len(plan.Pruned), 1)
	testutil.Equal(t, plan.WorktreeCount, 1)
	testutil.Equal(t, plan.OrphanCount, 1)

	// Only the active task should remain.
	tasks, err := d.Tasks()
	testutil.NoError(t, err)
	testutil.Equal(t, len(tasks), 1)
	testutil.Equal(t, tasks[0].Name, "active")

	// Both worktree dirs gone.
	if DirExists(completedWT) {
		t.Error("completed task worktree should be removed")
	}
	if DirExists(orphanWT) {
		t.Error("orphan worktree should be swept")
	}
}

func TestPruneCompleted_NoneToPrune(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	testutil.NoError(t, d.Add(&model.Task{ID: "1", Name: "x", Status: model.StatusInProgress}))

	plan, err := PruneCompleted(d, PruneOptions{})
	testutil.NoError(t, err)
	testutil.Equal(t, len(plan.Pruned), 0)
	testutil.Equal(t, plan.WorktreeCount, 0)
}

// TestPlanRun_NoOpForEmpty ensures Run is safe to call on an empty plan
// (PrunePrepare returned early because no rows matched).
func TestPlanRun_NoOpForEmpty(t *testing.T) {
	plan := &PrunePlan{}
	called := false
	plan.Run(func(int, int) { called = true })
	if called {
		t.Error("OnProgress should not fire for empty plan")
	}
}

// TestPlanRun_CallOnceGuard verifies that a second Run is a no-op so the
// progress counter cannot overrun and the orphan goroutine cannot race on
// the shared knownPaths map.
func TestPlanRun_CallOnceGuard(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	wtRoot := filepath.Join(t.TempDir(), ".argus", "worktrees")
	completedWT := filepath.Join(wtRoot, "proj", "done")
	testutil.NoError(t, os.MkdirAll(completedWT, 0o755))
	testutil.NoError(t, d.Add(&model.Task{
		ID: "done-1", Name: "done", Status: model.StatusComplete,
		Project: "proj", Worktree: completedWT,
	}))

	plan, err := PrunePrepare(d, PruneOptions{
		WtRoot:   wtRoot,
		Projects: map[string]string{"proj": ""},
	})
	testutil.NoError(t, err)

	var firstCount, secondCount int
	plan.Run(func(_, _ int) { firstCount++ })
	plan.Run(func(_, _ int) { secondCount++ })

	if firstCount == 0 {
		t.Error("first Run should fire onProgress at least once")
	}
	if secondCount != 0 {
		t.Errorf("second Run should be a no-op, got %d progress callbacks", secondCount)
	}
}
