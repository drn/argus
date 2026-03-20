package tui2

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/drn/argus/internal/uxlog"
)

// removeWorktreeAndBranch removes a git worktree and deletes its local and
// remote branches.
func removeWorktreeAndBranch(worktreePath, branch, repoDir string) {
	uxlog.Log("[worktree] removeWorktreeAndBranch: path=%q branch=%q repoDir=%q", worktreePath, branch, repoDir)
	removeWorktree(worktreePath, repoDir)

	if branch == "" {
		uxlog.Log("[worktree] branch is empty, skipping branch cleanup")
		return
	}

	dir := repoDir
	if dir == "" {
		dir = filepath.Dir(worktreePath)
	}

	// Prune stale worktree references so git allows branch deletion.
	pruneWorktrees(dir)

	// If the stored branch is a base branch (e.g. "origin/master", "master"),
	// not an argus/* worktree branch, infer the worktree branch from the
	// directory name. This handles tasks created before the branch-name fix.
	actualBranch := branch
	if !strings.HasPrefix(branch, "argus/") {
		inferred := "argus/" + filepath.Base(worktreePath)
		uxlog.Log("[worktree] stored branch %q is not argus/*, trying inferred branch %q", branch, inferred)
		actualBranch = inferred
	}

	deleteBranch(dir, actualBranch)
	deleteRemoteBranch(dir, actualBranch)
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
		uxlog.Log("[worktree] deleteBranch: skipping (repoDir=%q branch=%q)", repoDir, branch)
		return
	}
	cmd := exec.Command("git", "branch", "-D", branch)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		uxlog.Log("[worktree] git branch -D %q failed in %q: %v: %s", branch, repoDir, err, out)
	} else {
		uxlog.Log("[worktree] deleted local branch %q in %q", branch, repoDir)
	}
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
		uxlog.Log("[worktree] removeWorktree: path %q does not exist, skipping", worktreePath)
		return
	}
	if !isWorktreeSubdir(worktreePath) {
		uxlog.Log("[worktree] removeWorktree: path %q is not a worktree subdir, skipping", worktreePath)
		return
	}
	cleaned := filepath.Clean(worktreePath)
	cmd := exec.Command("git", "worktree", "remove", "--force", cleaned)
	if repoDir != "" {
		cmd.Dir = repoDir
	} else {
		cmd.Dir = filepath.Dir(cleaned)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		uxlog.Log("[worktree] git worktree remove %q failed: %v: %s", cleaned, err, out)
	} else {
		uxlog.Log("[worktree] git worktree remove succeeded for %q", cleaned)
	}
	// Always remove the directory — git worktree remove can succeed but leave
	// behind empty dirs or untracked files.
	if dirExists(cleaned) {
		uxlog.Log("[worktree] removing leftover directory %q", cleaned)
		_ = os.RemoveAll(cleaned)
	}
}

// pruneWorktrees runs "git worktree prune" to clean up stale worktree
// references. This is needed before deleting branches that were associated
// with already-removed worktrees — git refuses to delete a branch if a
// stale worktree reference still points to it.
func pruneWorktrees(repoDir string) {
	if repoDir == "" {
		return
	}
	cmd := exec.Command("git", "worktree", "prune")
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		uxlog.Log("[worktree] git worktree prune failed in %q: %v: %s", repoDir, err, out)
	}
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
