package tui2

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRemoveWorktreeAndBranch(t *testing.T) {
	// Create a temporary git repo to act as the main repo.
	repoDir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")

	// Create an initial commit so we can branch.
	f := filepath.Join(repoDir, "README.md")
	os.WriteFile(f, []byte("hello"), 0o644) //nolint:errcheck
	run("add", ".")
	run("commit", "-m", "init")

	// Create a worktree under a recognized .argus/worktrees/ path.
	wtBase := filepath.Join(t.TempDir(), ".argus", "worktrees", "proj")
	os.MkdirAll(wtBase, 0o755) //nolint:errcheck
	wtPath := filepath.Join(wtBase, "my-task")
	branch := "argus/my-task"

	run("worktree", "add", "-b", branch, wtPath, "HEAD")

	// Verify worktree and branch exist.
	if !dirExists(wtPath) {
		t.Fatal("worktree dir should exist")
	}
	if !branchExists(repoDir, branch) {
		t.Fatal("branch should exist")
	}

	// Now clean up.
	removeWorktreeAndBranch(wtPath, branch, repoDir)

	// Worktree directory should be gone.
	if dirExists(wtPath) {
		t.Error("worktree dir should have been removed")
	}

	// Branch should be gone.
	if branchExists(repoDir, branch) {
		t.Error("branch should have been deleted")
	}
}

func TestRemoveWorktreeAndBranch_InfersBranch(t *testing.T) {
	// Test that when task.Branch is a base branch (not argus/*),
	// the cleanup infers the correct argus/* branch from the dir name.
	repoDir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")
	f := filepath.Join(repoDir, "README.md")
	os.WriteFile(f, []byte("hello"), 0o644) //nolint:errcheck
	run("add", ".")
	run("commit", "-m", "init")

	wtBase := filepath.Join(t.TempDir(), ".argus", "worktrees", "proj")
	os.MkdirAll(wtBase, 0o755) //nolint:errcheck
	wtPath := filepath.Join(wtBase, "fix-bug")
	branch := "argus/fix-bug"

	run("worktree", "add", "-b", branch, wtPath, "HEAD")

	// Simulate the old bug: task.Branch has the base branch, not the worktree branch.
	storedBranch := "origin/master"

	removeWorktreeAndBranch(wtPath, storedBranch, repoDir)

	if dirExists(wtPath) {
		t.Error("worktree dir should have been removed")
	}
	if branchExists(repoDir, branch) {
		t.Error("inferred branch argus/fix-bug should have been deleted")
	}
}

func TestRemoveWorktree_CleansEmptyDir(t *testing.T) {
	// Verify that even when git worktree remove succeeds but leaves an
	// empty directory, we clean it up.
	repoDir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")
	f := filepath.Join(repoDir, "README.md")
	os.WriteFile(f, []byte("hello"), 0o644) //nolint:errcheck
	run("add", ".")
	run("commit", "-m", "init")

	wtBase := filepath.Join(t.TempDir(), ".argus", "worktrees", "proj")
	os.MkdirAll(wtBase, 0o755) //nolint:errcheck
	wtPath := filepath.Join(wtBase, "leftover")

	run("worktree", "add", "-b", "argus/leftover", wtPath, "HEAD")

	// Create an untracked file in the worktree to simulate leftover content.
	os.WriteFile(filepath.Join(wtPath, "untracked.txt"), []byte("junk"), 0o644) //nolint:errcheck

	removeWorktree(wtPath, repoDir)

	// The directory should be completely removed.
	if dirExists(wtPath) {
		t.Error("worktree dir should have been fully removed including leftovers")
	}
}

func TestCountOrphanedWorktrees(t *testing.T) {
	// Create a fake ~/.argus/worktrees structure in a temp dir.
	// We can't override the home dir, so test walkOrphanedWorktrees directly
	// by creating the structure it expects.
	home := t.TempDir()
	t.Setenv("HOME", home)

	wtRoot := filepath.Join(home, ".argus", "worktrees")
	os.MkdirAll(filepath.Join(wtRoot, "proj1", "task-a"), 0o755) //nolint:errcheck
	os.MkdirAll(filepath.Join(wtRoot, "proj1", "task-b"), 0o755) //nolint:errcheck
	os.MkdirAll(filepath.Join(wtRoot, "proj2", "task-c"), 0o755) //nolint:errcheck

	// task-a is known, task-b and task-c are orphans.
	known := map[string]bool{
		filepath.Join(wtRoot, "proj1", "task-a"): true,
	}

	count := countOrphanedWorktrees(known)
	if count != 2 {
		t.Errorf("expected 2 orphans, got %d", count)
	}
}

func TestSweepOrphanedWorktrees(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	wtRoot := filepath.Join(home, ".argus", "worktrees")
	orphanPath := filepath.Join(wtRoot, "proj1", "orphan-task")
	os.MkdirAll(orphanPath, 0o755) //nolint:errcheck

	// Write a dummy file so the dir is non-empty.
	os.WriteFile(filepath.Join(orphanPath, "dummy.txt"), []byte("x"), 0o644) //nolint:errcheck

	known := map[string]bool{} // no known paths — everything is an orphan

	// Pass empty projects map — removeWorktreeAndBranch will skip git ops
	// but os.RemoveAll will still clean the dir.
	swept := sweepOrphanedWorktrees(known, map[string]string{})
	if swept != 1 {
		t.Errorf("expected 1 swept, got %d", swept)
	}

	// The orphan path should be gone (isWorktreeSubdir check will pass since
	// the path contains /.argus/worktrees/).
	if dirExists(orphanPath) {
		t.Error("orphan directory should have been removed")
	}

	// Parent project dir should also be cleaned up since it's now empty.
	projDir := filepath.Join(wtRoot, "proj1")
	if dirExists(projDir) {
		t.Error("empty project directory should have been removed")
	}
}

func branchExists(repoDir, branch string) bool {
	cmd := exec.Command("git", "rev-parse", "--verify", branch)
	cmd.Dir = repoDir
	return cmd.Run() == nil
}
