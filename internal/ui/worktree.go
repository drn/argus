package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/drn/argus/internal/db"
)

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// discoverWorktree checks the deterministic worktree path under ~/.argus/worktrees/
// for the given project and task name. Since Argus now creates worktrees at a
// known location, discovery is just a stat check.
func discoverWorktree(projectName, taskName string) string {
	if projectName == "" || taskName == "" {
		return ""
	}

	wtDir := filepath.Join(db.DataDir(), "worktrees", projectName, taskName)
	if _, err := os.Stat(filepath.Join(wtDir, ".git")); err == nil {
		return wtDir
	}
	return ""
}

// killStaleProcess sends SIGTERM to a process if it's still alive and waits
// briefly for it to exit. Used to clean up orphaned agent processes from a
// previous Argus session before resuming with --resume.
func killStaleProcess(pid int) {
	if pid <= 0 {
		return
	}
	if syscall.Kill(pid, 0) != nil {
		return // already dead
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)

	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		if syscall.Kill(pid, 0) != nil {
			return
		}
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

// removeWorktreeAndBranch removes a git worktree and deletes its local and
// remote branches.
func removeWorktreeAndBranch(worktreePath, branch, repoDir string) {
	removeWorktree(worktreePath, repoDir)
	if branch == "" {
		return
	}
	dir := repoDir
	if dir == "" {
		dir = filepath.Dir(worktreePath)
	}
	deleteBranch(dir, branch)
	deleteRemoteBranch(dir, branch)
}

// deleteRemoteBranch deletes a remote branch on origin.
func deleteRemoteBranch(repoDir, branch string) {
	if branch == "" || repoDir == "" {
		return
	}
	cmd := exec.Command("git", "push", "origin", "--delete", branch)
	cmd.Dir = repoDir
	_ = cmd.Run()
}

// deleteBranch force-deletes a local git branch.
func deleteBranch(repoDir, branch string) {
	if branch == "" || repoDir == "" {
		return
	}
	cmd := exec.Command("git", "branch", "-D", branch)
	cmd.Dir = repoDir
	_ = cmd.Run()
}

// isWorktreeSubdir returns true if the given path is inside a recognized
// worktree directory. Checks both the new ~/.argus/worktrees/ location and
// the legacy .claude/worktrees/ location. This prevents accidental deletion
// of the root project directory.
func isWorktreeSubdir(worktreePath string) bool {
	cleaned := filepath.Clean(worktreePath)
	sep := string(filepath.Separator)
	// New location: ~/.argus/worktrees/
	if strings.Contains(cleaned, sep+".argus"+sep+"worktrees"+sep) {
		return true
	}
	// Legacy location: <project>/.claude/worktrees/
	if strings.Contains(cleaned, sep+".claude"+sep+"worktrees"+sep) {
		return true
	}
	return false
}

// removeWorktree removes a git worktree directory. repoDir should be the main
// repository path so that `git worktree remove` can find the repo metadata.
// Falls back to os.RemoveAll if git fails.
func removeWorktree(worktreePath, repoDir string) {
	if !dirExists(worktreePath) {
		return
	}
	if !isWorktreeSubdir(worktreePath) {
		return
	}
	cmd := exec.Command("git", "worktree", "remove", "--force", filepath.Clean(worktreePath))
	if repoDir != "" {
		cmd.Dir = repoDir
	} else {
		cmd.Dir = filepath.Dir(worktreePath)
	}
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(worktreePath)
	}
}
