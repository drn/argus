package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// discoverClaudeWorktree looks for a Claude Code worktree under baseDir/.claude/worktrees/
// that matches the given task name. Worktrees are created as "argus/<taskName>" so we first
// check the expected path directly, then fall back to git worktree list and directory scan.
func discoverClaudeWorktree(baseDir, taskName string) string {
	claudeWtDir := filepath.Join(baseDir, ".claude", "worktrees")
	if !dirExists(claudeWtDir) {
		return ""
	}

	if taskName == "" {
		return ""
	}

	// Fast path: check the expected path directly (argus/<task-name>).
	expected := filepath.Join(claudeWtDir, "argus", taskName)
	if _, err := os.Stat(filepath.Join(expected, ".git")); err == nil {
		return expected
	}

	// Try git worktree list for accuracy
	out, err := runGit(baseDir, "worktree", "list", "--porcelain")
	if err == nil {
		for _, block := range strings.Split(out, "\n\n") {
			for _, line := range strings.Split(block, "\n") {
				if strings.HasPrefix(line, "worktree ") {
					wt := strings.TrimPrefix(line, "worktree ")
					if !strings.HasPrefix(wt, claudeWtDir+string(filepath.Separator)) && !strings.HasPrefix(wt, claudeWtDir+"/") {
						continue
					}
					if filepath.Base(wt) == taskName {
						return wt
					}
				}
			}
		}
	}

	// Fallback: scan directory recursively for a worktree matching the task name
	return findWorktreeByName(claudeWtDir, taskName)
}

// findWorktreeByName recursively scans a directory for a git worktree whose
// directory name matches the given task name. Returns empty string if not found.
func findWorktreeByName(dir, taskName string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		candidate := filepath.Join(dir, e.Name())
		if e.Name() == taskName {
			if _, err := os.Stat(filepath.Join(candidate, ".git")); err == nil {
				return candidate
			}
		}
		if found := findWorktreeByName(candidate, taskName); found != "" {
			return found
		}
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

// removeWorktreeAndBranch removes a git worktree and deletes its associated branch.
func removeWorktreeAndBranch(worktreePath, branch, repoDir string) {
	removeWorktree(worktreePath)
	if branch == "" {
		return
	}
	dir := repoDir
	if dir == "" {
		dir = filepath.Dir(worktreePath)
	}
	deleteBranch(dir, branch)
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

// isWorktreeSubdir returns true if the given path is inside a .claude/worktrees/
// directory, which is the expected location for Claude Code worktrees. This
// prevents accidental deletion of the root project directory.
func isWorktreeSubdir(worktreePath string) bool {
	cleaned := filepath.Clean(worktreePath)
	return strings.Contains(cleaned, string(filepath.Separator)+".claude"+string(filepath.Separator)+"worktrees"+string(filepath.Separator))
}

func removeWorktree(worktreePath string) {
	if !dirExists(worktreePath) {
		return
	}
	if !isWorktreeSubdir(worktreePath) {
		return
	}
	cmd := exec.Command("git", "worktree", "remove", "--force", filepath.Clean(worktreePath))
	cmd.Dir = filepath.Dir(worktreePath)
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(worktreePath)
	}
}
