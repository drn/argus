package tui2

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/uxlog"
)

// isRealDataDir returns true if path is under the real ~/.argus/ directory
// (not a test temp dir). Used to prevent tests from accidentally operating on
// real worktrees — the "re-still-properly-cleaning" incident wiped 12 live
// worktrees when a test scanned the real filesystem.
func isRealDataDir(path string) bool {
	cleaned := filepath.Clean(path)
	realData := filepath.Clean(db.DataDir())
	return strings.HasPrefix(cleaned, realData+string(filepath.Separator)) || cleaned == realData
}

// testGuard returns true (and logs a warning) if we detect that we're running
// inside "go test" and the path targets the real ~/.argus/ directory. This
// prevents test code from accidentally deleting real worktrees.
func testGuard(path string) bool {
	if !isTestBinary() {
		return false
	}
	if !isRealDataDir(path) {
		return false
	}
	uxlog.Log("[worktree] BLOCKED: refusing to operate on real path %q during go test", path)
	return true
}

// removeWorktreeAndBranch removes a git worktree and deletes its local and
// remote branches.
func removeWorktreeAndBranch(worktreePath, branch, repoDir string) {
	if testGuard(worktreePath) {
		return
	}
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
	if testGuard(worktreePath) {
		return
	}
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

// countOrphanedWorktrees returns the number of worktree directories under
// wtRoot that are not tracked in the DB.
func countOrphanedWorktrees(wtRoot string, knownPaths map[string]bool) int {
	return walkOrphanedWorktrees(wtRoot, knownPaths, nil)
}

// sweepOrphanedWorktrees removes orphaned worktree directories and their
// associated branches. projects maps project name → repo path.
// Returns the count of cleaned directories.
func sweepOrphanedWorktrees(wtRoot string, knownPaths map[string]bool, projects map[string]string) int {
	return walkOrphanedWorktrees(wtRoot, knownPaths, projects)
}

// walkOrphanedWorktrees scans wtRoot/<project>/<task>/ dirs.
// If projects is nil, it just counts orphans. If non-nil, it removes them.
func walkOrphanedWorktrees(wtRoot string, knownPaths map[string]bool, projects map[string]string) int {
	if !dirExists(wtRoot) {
		return 0
	}

	projEntries, err := os.ReadDir(wtRoot)
	if err != nil {
		return 0
	}

	count := 0
	for _, projEntry := range projEntries {
		if !projEntry.IsDir() {
			continue
		}
		projDir := filepath.Join(wtRoot, projEntry.Name())
		taskEntries, err := os.ReadDir(projDir)
		if err != nil {
			continue
		}
		for _, taskEntry := range taskEntries {
			if !taskEntry.IsDir() {
				continue
			}
			wtPath := filepath.Join(projDir, taskEntry.Name())
			if knownPaths[wtPath] {
				continue
			}
			count++
			if projects != nil {
				repoDir := projects[projEntry.Name()]
				branch := "argus/" + taskEntry.Name()
				uxlog.Log("[worktree] sweeping orphan: path=%q branch=%q repoDir=%q", wtPath, branch, repoDir)
				removeWorktreeAndBranch(wtPath, branch, repoDir)
			}
		}
		// Remove empty project directories after sweep.
		if projects != nil {
			remaining, _ := os.ReadDir(projDir)
			if len(remaining) == 0 {
				os.Remove(projDir) //nolint:errcheck
			}
		}
	}
	return count
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// isTestBinary returns true when the current process is a Go test binary.
// Go test compiles a binary named *.test (e.g., "tui2.test") before running.
func isTestBinary() bool {
	return strings.HasSuffix(os.Args[0], ".test") ||
		strings.Contains(os.Args[0], "/_test/")
}
