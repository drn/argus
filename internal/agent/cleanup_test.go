package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestRemoveWorktreeAndBranch(t *testing.T) {
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
	wtPath := filepath.Join(wtBase, "my-task")
	branch := "argus/my-task"

	run("worktree", "add", "-b", branch, wtPath, "HEAD")

	if !dirExists(wtPath) {
		t.Fatal("worktree dir should exist")
	}
	if !branchExists(repoDir, branch) {
		t.Fatal("branch should exist")
	}

	RemoveWorktreeAndBranch(wtPath, branch, repoDir)

	if dirExists(wtPath) {
		t.Error("worktree dir should have been removed")
	}
	if branchExists(repoDir, branch) {
		t.Error("branch should have been deleted")
	}
}

func TestRemoveWorktreeAndBranch_InfersBranch(t *testing.T) {
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

	storedBranch := "origin/master"
	RemoveWorktreeAndBranch(wtPath, storedBranch, repoDir)

	if dirExists(wtPath) {
		t.Error("worktree dir should have been removed")
	}
	if branchExists(repoDir, branch) {
		t.Error("inferred branch argus/fix-bug should have been deleted")
	}
}

func TestRemoveWorktree_CleansEmptyDir(t *testing.T) {
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

	os.WriteFile(filepath.Join(wtPath, "untracked.txt"), []byte("junk"), 0o644) //nolint:errcheck

	RemoveWorktree(wtPath, repoDir)

	if dirExists(wtPath) {
		t.Error("worktree dir should have been fully removed including leftovers")
	}
}

func branchExists(repoDir, branch string) bool {
	cmd := exec.Command("git", "rev-parse", "--verify", branch)
	cmd.Dir = repoDir
	return cmd.Run() == nil
}

func TestIsTestBinary(t *testing.T) {
	if !isTestBinary() {
		t.Fatal("isTestBinary should return true during go test")
	}
}

func TestTestGuard_BlocksRealPath(t *testing.T) {
	home, _ := os.UserHomeDir()
	realPath := filepath.Join(home, ".argus", "worktrees", "proj", "task")
	if !testGuard(realPath) {
		t.Fatal("testGuard should block real ~/.argus/ path during go test")
	}
}

func TestTestGuard_AllowsTempPath(t *testing.T) {
	tmpPath := filepath.Join(t.TempDir(), ".argus", "worktrees", "proj", "task")
	if testGuard(tmpPath) {
		t.Fatal("testGuard should allow temp dir path during go test")
	}
}

func TestIsRealDataDir(t *testing.T) {
	home, _ := os.UserHomeDir()
	tests := []struct {
		path string
		want bool
	}{
		{filepath.Join(home, ".argus", "worktrees", "proj", "task"), true},
		{filepath.Join(home, ".argus"), true},
		{filepath.Join("/tmp", ".argus", "worktrees", "proj"), false},
		{filepath.Join(home, ".argus-other"), false},
	}
	for _, tt := range tests {
		if got := isRealDataDir(tt.path); got != tt.want {
			t.Errorf("isRealDataDir(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

// TestDirExists verifies the exported DirExists wrapper.
func TestDirExists(t *testing.T) {
	tmp := t.TempDir()
	testutil.Equal(t, DirExists(tmp), true)

	missing := filepath.Join(tmp, "no-such-dir")
	testutil.Equal(t, DirExists(missing), false)

	f := filepath.Join(tmp, "file.txt")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Equal(t, DirExists(f), false)
}

// TestIsWorktreeSubdir covers both legacy and modern paths plus negative case.
func TestIsWorktreeSubdir(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/home/u/.argus/worktrees/proj/task", true},
		{"/home/u/.claude/worktrees/proj/task", true},
		{"/home/u/.argus/worktrees/proj/task/subdir", true},
		{"/home/u/projects/whatever", false},
		{"/.argus", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			testutil.Equal(t, IsWorktreeSubdir(tt.path), tt.want)
		})
	}
}

// TestRemoveWorktree_NotASubdir is a no-op when the path is not a worktree.
func TestRemoveWorktree_NotASubdir(t *testing.T) {
	tmp := t.TempDir()

	RemoveWorktree(tmp, "")
	if !dirExists(tmp) {
		t.Error("RemoveWorktree should not have removed a non-worktree path")
	}
}

// TestRemoveWorktree_PathDoesNotExist is a no-op when the dir is missing.
func TestRemoveWorktree_PathDoesNotExist(t *testing.T) {
	missing := filepath.Join(t.TempDir(), ".argus", "worktrees", "proj", "task")

	RemoveWorktree(missing, "")
}

// TestDeleteBranch_EmptyArgs is a no-op when repoDir or branch is empty.
func TestDeleteBranch_EmptyArgs(t *testing.T) {

	DeleteBranch("", "argus/foo")
	DeleteBranch("/some/repo", "")
	DeleteBranch("", "")
}

// TestDeleteRemoteBranch_EmptyArgs is a no-op when repoDir or branch is empty.
func TestDeleteRemoteBranch_EmptyArgs(t *testing.T) {

	DeleteRemoteBranch("", "argus/foo")
	DeleteRemoteBranch("/some/repo", "")
	DeleteRemoteBranch("", "")
}

// TestPruneWorktrees_EmptyDir is a no-op with empty repoDir.
func TestPruneWorktrees_EmptyDir(t *testing.T) {

	pruneWorktrees("")
}

// TestRemoveWorktreeAndBranch_EmptyBranch skips branch cleanup when branch="".
func TestRemoveWorktreeAndBranch_EmptyBranch(t *testing.T) {
	repoDir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "init")

	wtBase := filepath.Join(t.TempDir(), ".argus", "worktrees", "proj")
	if err := os.MkdirAll(wtBase, 0o755); err != nil {
		t.Fatal(err)
	}
	wtPath := filepath.Join(wtBase, "task-x")
	run("worktree", "add", "-b", "argus/task-x", wtPath, "HEAD")

	RemoveWorktreeAndBranch(wtPath, "", repoDir)

	if dirExists(wtPath) {
		t.Error("worktree should have been removed even with empty branch")
	}

	if !branchExists(repoDir, "argus/task-x") {
		t.Error("branch should NOT have been deleted when branch arg was empty")
	}

	DeleteBranch(repoDir, "argus/task-x")
}
