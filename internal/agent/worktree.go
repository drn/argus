package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/drn/argus/internal/db"
)

// WorktreeDir returns the deterministic worktree path for a task:
// ~/.argus/worktrees/<projectName>/<taskName>
func WorktreeDir(projectName, taskName string) string {
	return filepath.Join(db.DataDir(), "worktrees", projectName, taskName)
}

// CreateWorktree creates a git worktree at the deterministic path with branch
// argus/<taskName>. If the worktree already exists, returns the path without error.
func CreateWorktree(projectPath, projectName, taskName, baseBranch string) (string, error) {
	wtDir := WorktreeDir(projectName, taskName)

	// If worktree already exists, return it.
	if info, err := os.Stat(filepath.Join(wtDir, ".git")); err == nil && !info.IsDir() {
		return wtDir, nil
	}
	// Also check if it's a directory (worktree .git can be a file or dir).
	if _, err := os.Stat(wtDir); err == nil {
		// Directory exists — check if it's a valid worktree.
		gitPath := filepath.Join(wtDir, ".git")
		if _, err := os.Stat(gitPath); err == nil {
			return wtDir, nil
		}
	}

	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(wtDir), 0o755); err != nil {
		return "", fmt.Errorf("creating worktree parent dir: %w", err)
	}

	branch := "argus/" + taskName
	if baseBranch == "" {
		baseBranch = "HEAD"
	}

	cmd := exec.Command("git", "worktree", "add", "-b", branch, wtDir, baseBranch)
	cmd.Dir = projectPath
	if out, err := cmd.CombinedOutput(); err != nil {
		// If branch already exists, try without -b (just attach to existing branch).
		cmd2 := exec.Command("git", "worktree", "add", wtDir, branch)
		cmd2.Dir = projectPath
		if out2, err2 := cmd2.CombinedOutput(); err2 != nil {
			return "", fmt.Errorf("git worktree add: %s\n%s", err, string(append(out, out2...)))
		}
	}

	return wtDir, nil
}
