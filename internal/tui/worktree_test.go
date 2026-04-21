package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/agent"
)

func TestCountOrphanedWorktrees(t *testing.T) {
	// Create a fake worktree structure in a temp dir.
	wtRoot := filepath.Join(t.TempDir(), "worktrees")
	os.MkdirAll(filepath.Join(wtRoot, "proj1", "task-a"), 0o755) //nolint:errcheck
	os.MkdirAll(filepath.Join(wtRoot, "proj1", "task-b"), 0o755) //nolint:errcheck
	os.MkdirAll(filepath.Join(wtRoot, "proj2", "task-c"), 0o755) //nolint:errcheck

	// task-a is known, task-b and task-c are orphans.
	known := map[string]bool{
		filepath.Join(wtRoot, "proj1", "task-a"): true,
	}

	count := countOrphanedWorktrees(wtRoot, known)
	if count != 2 {
		t.Errorf("expected 2 orphans, got %d", count)
	}
}

func TestSweepOrphanedWorktrees(t *testing.T) {
	wtRoot := filepath.Join(t.TempDir(), ".argus", "worktrees")
	orphanPath := filepath.Join(wtRoot, "proj1", "orphan-task")
	os.MkdirAll(orphanPath, 0o755) //nolint:errcheck

	// Write a dummy file so the dir is non-empty.
	os.WriteFile(filepath.Join(orphanPath, "dummy.txt"), []byte("x"), 0o644) //nolint:errcheck

	known := map[string]bool{} // no known paths — everything is an orphan

	// Pass empty projects map — RemoveWorktreeAndBranch will skip git ops
	// but os.RemoveAll will still clean the dir.
	swept := sweepOrphanedWorktrees(wtRoot, known, map[string]string{})
	if swept != 1 {
		t.Errorf("expected 1 swept, got %d", swept)
	}

	// The orphan path should be gone (IsWorktreeSubdir check will pass since
	// the path contains /.argus/worktrees/).
	if agent.DirExists(orphanPath) {
		t.Error("orphan directory should have been removed")
	}

	// Parent project dir should also be cleaned up since it's now empty.
	projDir := filepath.Join(wtRoot, "proj1")
	if agent.DirExists(projDir) {
		t.Error("empty project directory should have been removed")
	}
}

// TestSweepOrphanedWorktrees_RealRepo exercises the full branch-deletion path
// that production hits — a populated projects map pointing at a real git
// repository. The simpler TestSweepOrphanedWorktrees only checks the path
// where repoDir is empty and the directory gets unconditionally removed by
// os.RemoveAll.
func TestSweepOrphanedWorktrees_RealRepo(t *testing.T) {
	// Build a real git repo to serve as the project.
	repoDir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
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

	// Add a worktree under a .argus/worktrees/ path so IsWorktreeSubdir passes.
	wtRoot := filepath.Join(t.TempDir(), ".argus", "worktrees")
	wtPath := filepath.Join(wtRoot, "proj1", "orphan-task")
	os.MkdirAll(filepath.Dir(wtPath), 0o755) //nolint:errcheck
	runGit("worktree", "add", "-b", "argus/orphan-task", wtPath, "HEAD")

	if !agent.DirExists(wtPath) {
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

	known := map[string]bool{} // every worktree is orphaned
	projects := map[string]string{"proj1": repoDir}

	swept := sweepOrphanedWorktrees(wtRoot, known, projects)
	if swept != 1 {
		t.Errorf("expected 1 swept, got %d", swept)
	}
	if agent.DirExists(wtPath) {
		t.Error("worktree dir should have been removed")
	}
	if checkBranch() {
		t.Error("argus/orphan-task branch should have been deleted")
	}
}
