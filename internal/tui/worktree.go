package tui

import (
	"os"
	"path/filepath"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/uxlog"
)

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
	if !agent.DirExists(wtRoot) {
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
				agent.RemoveWorktreeAndBranch(wtPath, branch, repoDir)
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
