package agent

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

// CountOrphanedWorktrees returns the number of worktree directories under
// wtRoot that are not tracked in the DB.
func CountOrphanedWorktrees(wtRoot string, knownPaths map[string]bool) int {
	return walkOrphanedWorktrees(wtRoot, knownPaths, nil)
}

// SweepOrphanedWorktrees removes orphaned worktree directories and their
// associated branches. projects maps project name → repo path.
// Returns the count of cleaned directories.
func SweepOrphanedWorktrees(wtRoot string, knownPaths map[string]bool, projects map[string]string) int {
	return walkOrphanedWorktrees(wtRoot, knownPaths, projects)
}

// firstKnownDescendant returns a known worktree path that lives strictly
// inside `dir`, or "" if there is none. Defends against historical task names
// whose stored worktree path goes deeper than the fixed wtRoot/<project>/<task>
// layout the walker assumes — without this guard the walker would misclassify
// the parent dir as an orphan and `os.RemoveAll` it, taking the live worktree
// underneath with it. Returning the descendant (rather than a bool) lets the
// caller log which tracked path triggered the skip.
func firstKnownDescendant(dir string, knownPaths map[string]bool) string {
	prefix := filepath.Clean(dir) + string(filepath.Separator)
	for known := range knownPaths {
		if strings.HasPrefix(filepath.Clean(known), prefix) {
			return known
		}
	}
	return ""
}

// walkOrphanedWorktrees scans wtRoot/<project>/<task>/ dirs.
// If projects is nil, it just counts orphans. If non-nil, it removes them.
func walkOrphanedWorktrees(wtRoot string, knownPaths map[string]bool, projects map[string]string) int {
	if !DirExists(wtRoot) {
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
			if known := firstKnownDescendant(wtPath, knownPaths); known != "" {
				uxlog.Log("[worktree] orphan sweep: skipping %q — ancestor of tracked worktree %q", wtPath, known)
				continue
			}
			count++
			if projects != nil {
				repoDir := projects[projEntry.Name()]
				branch := "argus/" + taskEntry.Name()
				uxlog.Log("[worktree] sweeping orphan: path=%q branch=%q repoDir=%q", wtPath, branch, repoDir)
				RemoveWorktreeAndBranch(wtPath, branch, repoDir)
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

// PruneOptions configures PrunePrepare and PruneCompleted.
type PruneOptions struct {
	// WtRoot is the worktree root directory (~/.argus/worktrees). Required for
	// the orphan sweep. If empty, the orphan sweep is skipped.
	WtRoot string

	// Projects maps project name → repo path; used by the orphan sweep to
	// resolve branch-deletion repoDir. Still removes worktree dirs without it.
	Projects map[string]string

	// ResolveRepoDir maps a task to its repo path. Called for each pruned task
	// with a non-empty Worktree. Typically `func(t) string { return agent.ResolveDir(t, cfg) }`.
	ResolveRepoDir func(*model.Task) string

	// Runner stops sessions for pruned tasks during PrunePrepare. Pass nil to
	// skip session stop (e.g., in tests or when no sessions are managed in this
	// process). Accepts the SessionProvider interface so both in-process
	// (*Runner) and remote (daemon client) implementations work.
	Runner SessionProvider
}

// PrunePlan is the result of PrunePrepare — DB rows are already deleted, the
// corresponding sessions stopped, and session logs removed, but worktree
// cleanup has NOT yet run. Call Run to execute the slow phase.
//
// The split exists so callers like the TUI can refresh their task list (now
// that the rows are gone from the DB) before the multi-second worktree
// cleanup begins.
type PrunePlan struct {
	// Pruned is the set of tasks removed from the DB during PrunePrepare.
	Pruned []*model.Task

	// WorktreeCount is the number of per-task worktrees that will be cleaned
	// by Run.
	WorktreeCount int

	// OrphanCount is the number of orphaned worktree directories that will be
	// cleaned by Run.
	OrphanCount int

	toClean        []*model.Task
	wtRoot         string
	projects       map[string]string
	knownPaths     map[string]bool
	resolveRepoDir func(*model.Task) string
	ran            sync.Once
}

// PrunePrepare runs the synchronous portion of a prune sweep: it removes all
// completed tasks from the DB, stops their sessions, removes their session
// logs, and computes the count of remaining worktree cleanup work. Call Run
// on the returned plan to execute the slow worktree/branch cleanup.
func PrunePrepare(database *db.DB, opts PruneOptions) (*PrunePlan, error) {
	pruned, err := database.PruneCompleted()
	if err != nil {
		return nil, err
	}
	if len(pruned) == 0 {
		return &PrunePlan{}, nil
	}

	uxlog.Log("[prune] pruning %d completed tasks", len(pruned))

	// Stop sessions synchronously (fast, in-process).
	if opts.Runner != nil {
		for _, t := range pruned {
			if opts.Runner.HasSession(t.ID) {
				_ = opts.Runner.Stop(t.ID)
			}
		}
	}

	// Remove session logs.
	for _, t := range pruned {
		os.Remove(SessionLogPath(t.ID)) //nolint:errcheck
	}

	var toClean []*model.Task
	for _, t := range pruned {
		if t.Worktree != "" {
			toClean = append(toClean, t)
		}
	}

	// Decide whether to run an orphan sweep.
	var (
		knownPaths  map[string]bool
		orphanCount int
	)
	if opts.WtRoot != "" {
		paths, err := database.WorktreePaths()
		if err != nil {
			uxlog.Log("[prune] WorktreePaths failed, skipping orphan sweep: %v", err)
		} else {
			knownPaths = paths
			// PruneCompleted already deleted these from the DB, so their
			// worktree dirs would be misidentified as orphans. Mark them
			// known so they aren't double-counted.
			for _, t := range toClean {
				knownPaths[t.Worktree] = true
			}
			orphanCount = CountOrphanedWorktrees(opts.WtRoot, knownPaths)
		}
	}

	return &PrunePlan{
		Pruned:         pruned,
		WorktreeCount:  len(toClean),
		OrphanCount:    orphanCount,
		toClean:        toClean,
		wtRoot:         opts.WtRoot,
		projects:       opts.Projects,
		knownPaths:     knownPaths,
		resolveRepoDir: opts.ResolveRepoDir,
	}, nil
}

// Run executes the slow phase of a prune sweep: removes per-task worktrees +
// branches in parallel, then runs the orphan sweep. Blocks until all goroutines
// complete.
//
// MUST be called exactly once per plan. Subsequent calls are no-ops (guarded
// by sync.Once) — the plan's `knownPaths` map is consumed by the orphan sweep
// goroutine, and the `cleaned` progress counter would overrun on a re-run.
//
// onProgress, if non-nil, fires from worker goroutines as each unit (one
// worktree clean or the entire orphan sweep batch) completes. Implementations
// MUST be safe to call concurrently — multiple goroutines invoke it in parallel.
func (p *PrunePlan) Run(onProgress func(done, total int)) {
	p.ran.Do(func() {
		p.runOnce(onProgress)
	})
}

func (p *PrunePlan) runOnce(onProgress func(done, total int)) {
	orphanUnits := 0
	if p.OrphanCount > 0 {
		orphanUnits = 1
	}
	total := len(p.toClean) + orphanUnits
	if total == 0 {
		return
	}

	var wg sync.WaitGroup
	var cleaned atomic.Int32
	bump := func() {
		n := cleaned.Add(1)
		if onProgress != nil {
			onProgress(int(n), total)
		}
	}

	for _, t := range p.toClean {
		wg.Add(1)
		go func(t *model.Task) {
			defer wg.Done()
			var repoDir string
			if p.resolveRepoDir != nil {
				repoDir = p.resolveRepoDir(t)
			}
			uxlog.Log("[prune] cleanup: task=%s name=%q worktree=%q branch=%q repoDir=%q project=%q",
				t.ID, t.Name, t.Worktree, t.Branch, repoDir, t.Project)
			RemoveWorktreeAndBranch(t.Worktree, t.Branch, repoDir)
			bump()
		}(t)
	}

	if p.OrphanCount > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			swept := SweepOrphanedWorktrees(p.wtRoot, p.knownPaths, p.projects)
			uxlog.Log("[prune] orphan sweep cleaned %d directories", swept)
			bump()
		}()
	}

	wg.Wait()
}

// PruneCompleted is a convenience wrapper that runs PrunePrepare and then
// PrunePlan.Run synchronously. Used by callers (like the HTTP API) that don't
// need an intermediate UI refresh.
func PruneCompleted(database *db.DB, opts PruneOptions) (*PrunePlan, error) {
	plan, err := PrunePrepare(database, opts)
	if err != nil {
		return nil, err
	}
	plan.Run(nil)
	return plan, nil
}
