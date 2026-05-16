package agent

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
// real worktrees.
func isRealDataDir(path string) bool {
	cleaned := filepath.Clean(path)
	realData := filepath.Clean(db.DataDir())
	return strings.HasPrefix(cleaned, realData+string(filepath.Separator)) || cleaned == realData
}

// testGuard returns true (and logs a warning) if we detect that we're running
// inside "go test" and the path targets the real ~/.argus/ directory. This
// prevents test code from accidentally deleting real worktrees.
//
// Paths under os.TempDir() are always allowed, even when they happen to
// resolve through an overridden $HOME to match db.DataDir(). Tests that
// t.Setenv("HOME", t.TempDir()) legitimately need to operate on a synthetic
// data dir; without this exemption the guard would block their cleanup.
func testGuard(path string) bool {
	if !isTestBinary() {
		return false
	}
	cleaned := filepath.Clean(path)
	tmpRoot := filepath.Clean(os.TempDir()) + string(filepath.Separator)
	if strings.HasPrefix(cleaned, tmpRoot) {
		return false
	}
	if !isRealDataDir(path) {
		return false
	}
	uxlog.Log("[worktree] BLOCKED: refusing to operate on real path %q during go test", path)
	return true
}

// RemoveWorktreeAndBranch removes a git worktree and deletes its local and
// remote branches. Best-effort: failures are logged, never returned — callers
// use this as a compensating cleanup action where a partial failure must not
// block the rest of the unwind chain.
func RemoveWorktreeAndBranch(worktreePath, branch, repoDir string) {
	if testGuard(worktreePath) {
		return
	}
	uxlog.Log("[worktree] RemoveWorktreeAndBranch: path=%q branch=%q repoDir=%q", worktreePath, branch, repoDir)
	RemoveWorktree(worktreePath, repoDir)

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

	DeleteBranch(dir, actualBranch)
	DeleteRemoteBranch(dir, actualBranch)
}

// DeleteRemoteBranch deletes a remote branch on origin. Best-effort.
func DeleteRemoteBranch(repoDir, branch string) {
	if branch == "" || repoDir == "" {
		return
	}
	cmd := exec.Command("git", "push", "origin", "--delete", branch)
	cmd.Dir = repoDir
	_ = cmd.Run()
}

// DeleteBranch force-deletes a local git branch. Best-effort.
func DeleteBranch(repoDir, branch string) {
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

// IsWorktreeSubdir returns true if the given path is inside a recognized
// worktree directory. Checks both the new ~/.argus/worktrees/ location and
// the legacy .claude/worktrees/ location.
func IsWorktreeSubdir(worktreePath string) bool {
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

// RemoveWorktree removes a git worktree directory. Best-effort.
func RemoveWorktree(worktreePath, repoDir string) {
	if testGuard(worktreePath) {
		return
	}
	if !dirExists(worktreePath) {
		uxlog.Log("[worktree] RemoveWorktree: path %q does not exist, skipping", worktreePath)
		return
	}
	if !IsWorktreeSubdir(worktreePath) {
		uxlog.Log("[worktree] RemoveWorktree: path %q is not a worktree subdir, skipping", worktreePath)
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
// references. Needed before deleting branches that were associated with
// already-removed worktrees — git refuses to delete a branch if a stale
// worktree reference still points to it.
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

// DirExists is the exported variant for callers in other packages
// (e.g. the TUI's orphan-sweep walks worktree directories).
func DirExists(path string) bool { return dirExists(path) }

// isTestBinary returns true when the current process is a Go test binary.
// Go test compiles a binary named *.test (e.g., "tui.test") before running.
// Keep in sync with the identical copies in internal/daemon/client/client.go
// and internal/api/selfupdate.go — same detection, different packages so
// each can refuse without importing across boundaries.
func isTestBinary() bool {
	return strings.HasSuffix(os.Args[0], ".test") ||
		strings.Contains(os.Args[0], "/_test/")
}
