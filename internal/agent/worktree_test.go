package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorktreeDir(t *testing.T) {
	dir := WorktreeDir("myproject", "fix-bug")
	if dir == "" {
		t.Fatal("expected non-empty path")
	}
	if !strings.Contains(dir, "myproject") || !strings.Contains(dir, "fix-bug") {
		t.Errorf("expected path containing 'myproject' and 'fix-bug', got %q", dir)
	}
	if !strings.Contains(dir, filepath.Join(".argus", "worktrees")) {
		t.Errorf("expected path under .argus/worktrees/, got %q", dir)
	}
}

func TestWorktreeDir_Structure(t *testing.T) {
	dir := WorktreeDir("proj", "task")
	// Should end with /proj/task
	if !strings.HasSuffix(dir, filepath.Join("proj", "task")) {
		t.Errorf("expected path ending with proj/task, got %q", dir)
	}
}

func TestCreateWorktree(t *testing.T) {
	// Set up a temporary git repo to test worktree creation.
	repoDir := t.TempDir()

	// Initialize a git repo with a commit so worktrees can be created.
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s\n%s", args, err, out)
		}
	}

	// Create an initial commit (worktree add requires at least one commit).
	readme := filepath.Join(repoDir, "README.md")
	if err := os.WriteFile(readme, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "."},
		{"commit", "-m", "initial"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s\n%s", args, err, out)
		}
	}

	// Override HOME so WorktreeDir resolves to a temp location.
	origHome := os.Getenv("HOME")
	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	wtPath, err := CreateWorktree(repoDir, "testproject", "fix-bug", "")
	if err != nil {
		t.Fatalf("CreateWorktree failed: %v", err)
	}

	// Verify worktree was created.
	gitFile := filepath.Join(wtPath, ".git")
	if _, err := os.Stat(gitFile); err != nil {
		t.Errorf("expected .git to exist in worktree at %q", wtPath)
	}

	// Verify path matches expected structure.
	expected := filepath.Join(tmpHome, ".argus", "worktrees", "testproject", "fix-bug")
	if wtPath != expected {
		t.Errorf("expected path %q, got %q", expected, wtPath)
	}

	// Verify branch was created.
	cmd := exec.Command("git", "branch", "--list", "argus/fix-bug")
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git branch --list: %s\n%s", err, out)
	}
	if !strings.Contains(string(out), "argus/fix-bug") {
		t.Errorf("expected branch 'argus/fix-bug' to exist, got: %s", out)
	}

	// Calling CreateWorktree again should be idempotent.
	wtPath2, err := CreateWorktree(repoDir, "testproject", "fix-bug", "")
	if err != nil {
		t.Fatalf("second CreateWorktree failed: %v", err)
	}
	if wtPath2 != wtPath {
		t.Errorf("expected same path on second call, got %q vs %q", wtPath2, wtPath)
	}
}

func TestCreateWorktree_ExistingBranch(t *testing.T) {
	// Test the fallback path where the branch already exists but worktree doesn't.
	repoDir := t.TempDir()

	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s\n%s", args, err, out)
		}
	}
	readme := filepath.Join(repoDir, "README.md")
	if err := os.WriteFile(readme, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "."},
		{"commit", "-m", "initial"},
		{"branch", "argus/my-task"}, // pre-create the branch
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s\n%s", args, err, out)
		}
	}

	origHome := os.Getenv("HOME")
	tmpHome := t.TempDir()
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	wtPath, err := CreateWorktree(repoDir, "testproject", "my-task", "")
	if err != nil {
		t.Fatalf("CreateWorktree with existing branch failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(wtPath, ".git")); err != nil {
		t.Errorf("expected worktree to exist at %q", wtPath)
	}
}
