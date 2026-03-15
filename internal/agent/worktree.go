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
// argus/<taskName>. If the path conflicts with an existing worktree for a
// different task, appends -1, -2, etc. until a free slot is found. Returns
// the worktree path and the final task name (which may have a suffix).
func CreateWorktree(projectPath, projectName, taskName, baseBranch string) (wtPath, finalName string, err error) {
	if baseBranch == "" {
		baseBranch = "HEAD"
	}

	// Try the base name first, then -1, -2, ... up to 99.
	candidate := taskName
	for i := 0; i <= 99; i++ {
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", taskName, i)
		}
		wtDir := WorktreeDir(projectName, candidate)

		// If worktree already exists at this path, skip to next suffix.
		if _, statErr := os.Stat(wtDir); statErr == nil {
			continue
		}

		// Ensure parent directory exists.
		if mkErr := os.MkdirAll(filepath.Dir(wtDir), 0o755); mkErr != nil {
			return "", "", fmt.Errorf("creating worktree parent dir: %w", mkErr)
		}

		branch := "argus/" + candidate
		cmd := exec.Command("git", "worktree", "add", "-b", branch, wtDir, baseBranch)
		cmd.Dir = projectPath
		if out, cmdErr := cmd.CombinedOutput(); cmdErr != nil {
			// If branch already exists, try without -b (attach to existing branch).
			cmd2 := exec.Command("git", "worktree", "add", wtDir, branch)
			cmd2.Dir = projectPath
			if out2, cmdErr2 := cmd2.CombinedOutput(); cmdErr2 != nil {
				return "", "", fmt.Errorf("git worktree add: %s\n%s", cmdErr, string(append(out, out2...)))
			}
		}

		return wtDir, candidate, nil
	}

	return "", "", fmt.Errorf("could not create worktree: too many name conflicts for %q", taskName)
}
