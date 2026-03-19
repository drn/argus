package tui2

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

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
// the legacy .claude/worktrees/ location.
func isWorktreeSubdir(worktreePath string) bool {
	cleaned := filepath.Clean(worktreePath)
	sep := string(filepath.Separator)
	if strings.Contains(cleaned, sep+".argus"+sep+"worktrees"+sep) {
		return true
	}
	if strings.Contains(cleaned, sep+".claude"+sep+"worktrees"+sep) {
		return true
	}
	return false
}

// removeWorktree removes a git worktree directory.
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

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
