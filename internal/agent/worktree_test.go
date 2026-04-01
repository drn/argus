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

	wtPath, finalName, branchName, err := CreateWorktree(repoDir, "testproject", "fix-bug", "")
	if err != nil {
		t.Fatalf("CreateWorktree failed: %v", err)
	}
	if finalName != "fix-bug" {
		t.Errorf("expected finalName %q, got %q", "fix-bug", finalName)
	}
	if branchName != "argus/fix-bug" {
		t.Errorf("expected branchName %q, got %q", "argus/fix-bug", branchName)
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

	// Creating again with same name should get -1 suffix.
	wtPath2, finalName2, branchName2, err := CreateWorktree(repoDir, "testproject", "fix-bug", "")
	if err != nil {
		t.Fatalf("second CreateWorktree failed: %v", err)
	}
	if finalName2 != "fix-bug-1" {
		t.Errorf("expected finalName %q, got %q", "fix-bug-1", finalName2)
	}
	if branchName2 != "argus/fix-bug-1" {
		t.Errorf("expected branchName %q, got %q", "argus/fix-bug-1", branchName2)
	}
	expected2 := filepath.Join(tmpHome, ".argus", "worktrees", "testproject", "fix-bug-1")
	if wtPath2 != expected2 {
		t.Errorf("expected path %q, got %q", expected2, wtPath2)
	}
}

func TestCreateWorktree_RemoteBranch(t *testing.T) {
	// Test that CreateWorktree falls back to origin/<branch> when the local
	// branch doesn't exist (e.g., a bare clone with only remote-tracking refs).
	upstreamDir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = upstreamDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s\n%s", args, err, out)
		}
	}
	readme := filepath.Join(upstreamDir, "README.md")
	if err := os.WriteFile(readme, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "."},
		{"commit", "-m", "initial"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = upstreamDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s\n%s", args, err, out)
		}
	}

	// Clone into a repo, then delete the local default branch so only
	// the origin remote-tracking branch exists.
	repoDir := t.TempDir()
	cloneCmd := exec.Command("git", "clone", upstreamDir, repoDir)
	if out, err := cloneCmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone: %s\n%s", err, out)
	}
	// Detect the default branch name (could be master or main depending on git config).
	detectCmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	detectCmd.Dir = repoDir
	defaultBranchBytes, err := detectCmd.Output()
	if err != nil {
		t.Fatalf("detecting default branch: %v", err)
	}
	defaultBranch := strings.TrimSpace(string(defaultBranchBytes))
	// Create a different branch and delete local default branch so only origin/<branch> exists.
	for _, args := range [][]string{
		{"checkout", "-b", "other"},
		{"branch", "-d", defaultBranch},
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

	// baseBranch=defaultBranch should resolve to origin/<defaultBranch>.
	wtPath, finalName, _, err := CreateWorktree(repoDir, "testproj", "remote-test", defaultBranch)
	if err != nil {
		t.Fatalf("CreateWorktree with remote branch failed: %v", err)
	}
	if finalName != "remote-test" {
		t.Errorf("expected finalName %q, got %q", "remote-test", finalName)
	}
	if _, err := os.Stat(filepath.Join(wtPath, ".git")); err != nil {
		t.Errorf("expected worktree at %q", wtPath)
	}
}

func TestSanitizeBranchName(t *testing.T) {
	tests := []struct {
		input, expected string
	}{
		{"fix-bug", "fix-bug"},
		{"fails?", "fails"},
		{"hello world", "hello-world"},
		{"feat~1", "feat-1"},
		{"name..with..dots", "name-with-dots"},
		{"trailing.", "trailing"},
		{"a:b:c", "a-b-c"},
		{"has[brackets]", "has-brackets"},
		{"back\\slash", "back-slash"},
		{"star*name", "star-name"},
		{"caret^ref", "caret-ref"},
		{"ref@{0}", "ref@-0"},
		{".hidden", "hidden"},   // leading dot stripped
		{".dotfile.", "dotfile"},// both leading and trailing dots
		{"???", "task"},         // all invalid → fallback
		{"", "task"},            // empty → fallback
		{"normal-name", "normal-name"},
		{"Protect-production-branch-Lock-down-AWS-Lock-down-production-granting", "Protect-production-branch"},
		{"aaaaabbbbbcccccdddddeeeeefffff-this-part-is-too-long-and-should-be-truncated", "aaaaabbbbbcccccdddddeeeeefffff"},
		{"short-name", "short-name"},  // under limit, unchanged
	}
	for _, tt := range tests {
		got := sanitizeBranchName(tt.input)
		if got != tt.expected {
			t.Errorf("sanitizeBranchName(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestCreateWorktree_SpecialChars(t *testing.T) {
	// Task names with special characters should not fail worktree creation.
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

	// "fails?" contains ?, which is invalid in git branch names.
	wtPath, finalName, _, err := CreateWorktree(repoDir, "testproject", "fails?", "")
	if err != nil {
		t.Fatalf("CreateWorktree with special chars failed: %v", err)
	}
	if finalName != "fails" {
		t.Errorf("expected finalName %q, got %q", "fails", finalName)
	}
	if _, err := os.Stat(filepath.Join(wtPath, ".git")); err != nil {
		t.Errorf("expected worktree at %q", wtPath)
	}
}

func TestCreateWorktree_StaleRef(t *testing.T) {
	// Test that CreateWorktree succeeds even when a previous worktree was
	// deleted without `git worktree remove`, leaving a stale reference.
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

	// Create a worktree normally.
	wtPath, _, _, err := CreateWorktree(repoDir, "testproj", "stale", "")
	if err != nil {
		t.Fatalf("first CreateWorktree failed: %v", err)
	}

	// Simulate stale reference: remove the worktree directory WITHOUT
	// running `git worktree remove`.
	if err := os.RemoveAll(wtPath); err != nil {
		t.Fatalf("removing worktree dir: %v", err)
	}

	// Without pruning, the branch argus/stale is still locked to the stale
	// worktree entry. CreateWorktree should prune and succeed.
	wtPath2, finalName, _, err := CreateWorktree(repoDir, "testproj", "stale", "")
	if err != nil {
		t.Fatalf("second CreateWorktree with stale ref failed: %v", err)
	}
	if finalName != "stale" {
		t.Errorf("expected finalName %q, got %q", "stale", finalName)
	}
	if _, err := os.Stat(filepath.Join(wtPath2, ".git")); err != nil {
		t.Errorf("expected worktree at %q", wtPath2)
	}
}

func TestCleanGitOutput(t *testing.T) {
	tests := []struct {
		name   string
		input  [][]byte
		expect string
	}{
		{
			name:   "fatal line extracted",
			input:  [][]byte{[]byte("Preparing worktree\nfatal: branch already exists\n")},
			expect: "fatal: branch already exists",
		},
		{
			name:   "multiple fatal lines",
			input:  [][]byte{[]byte("fatal: first\n"), []byte("fatal: second\n")},
			expect: "fatal: first; fatal: second",
		},
		{
			name:   "no fatal lines",
			input:  [][]byte{[]byte("some\nother\nerror\n")},
			expect: "some other error",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanGitOutput(tt.input...)
			if got != tt.expect {
				t.Errorf("cleanGitOutput = %q, want %q", got, tt.expect)
			}
		})
	}
}

func TestResolveStartPoint(t *testing.T) {
	// HEAD should always be returned as-is.
	if got := resolveStartPoint("/nonexistent", "HEAD"); got != "HEAD" {
		t.Errorf("expected HEAD, got %q", got)
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

	wtPath, _, _, err := CreateWorktree(repoDir, "testproject", "my-task", "")
	if err != nil {
		t.Fatalf("CreateWorktree with existing branch failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(wtPath, ".git")); err != nil {
		t.Errorf("expected worktree to exist at %q", wtPath)
	}
}
