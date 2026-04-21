package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
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
