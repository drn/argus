// reclaim_sweep.go is Stage 3 of fix-hera-nuke-cleanup: a reconciliation
// sweep that durably finishes cascade-nuke cleanup left behind by a daemon
// crash or restart mid-teardown. heraReclaimAndArchiveTask
// (internal/tui/heraactions.go) reclaims a nuked task's worktree+branch in a
// backgrounded goroutine and then prunes the row once that settles — both of
// which die with the process if the daemon exits first. ReconcileHeraReclaims
// is the sibling to heraadopt.ReconcileBindings (same idempotent,
// safe-to-rerun, run-on-every-boot contract): it re-discovers exactly the
// work an interrupted nuke left unfinished and retries it.
package hera

import (
	"context"
	"slices"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/gitutil"
	"github.com/drn/argus/internal/mergesafety"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

// SessionChecker is the narrow runner surface ReconcileHeraReclaims needs to
// tell whether a candidate task's reclaim-triggered session stop has
// settled yet. Mirrors agent.SessionProvider's HasSession(taskID string)
// bool exactly (see internal/tui/heraactions.go's a.runner.HasSession usage)
// so the daemon's real agent.SessionRunner satisfies it with no wrapping.
type SessionChecker interface {
	HasSession(taskID string) bool
}

// Summary reports what one ReconcileHeraReclaims pass did, for the caller
// (daemon startup / periodic ticker, Stage 6) to log.
type Summary struct {
	// Candidates is the size of the candidate set this pass considered.
	Candidates int
	// WorktreesRetried counts candidates whose worktree directory still
	// existed on disk, so its worktree + own branch removal was retried.
	WorktreesRetried int
	// StackedBranchesDeleted counts additional stacked branches (belonging
	// to other tasks in a candidate's base_branch chain) deleted via the
	// best-effort Tier D re-discovery — see reconcileStackedBranches.
	StackedBranchesDeleted int
	// Pruned counts candidate rows deleted this pass.
	Pruned int
	// SkippedLiveSession counts candidates left in place because a live
	// session still exists for the task — retried worktree/branch cleanup,
	// but not yet safe to prune.
	SkippedLiveSession int
}

// ReconcileHeraReclaims durably finishes cascade-nuke cleanup for every task
// left behind by an interrupted nuke. The candidate set is every task with a
// hera_bindings row ended for the nuke path's own reason
// (db.HeraEndReasonUserDeleted) that holds no live binding
// (db.HeraNukeReclaimCandidates — the same live-binding guard PruneTasks
// re-verifies at delete time). For each candidate: retry the worktree + its
// own local/remote branch removal if the worktree directory still exists on
// disk, best-effort retry deleting any additional stacked branch this
// candidate's base_branch chain confirms safe to delete (see
// reconcileStackedBranches), then prune the row once no live session remains
// for the task — leaving it in place for a later sweep otherwise.
//
// Idempotent and safe to call repeatedly, including on every daemon boot: a
// candidate whose cleanup already fully completed (worktree gone, nothing
// left to classify in its stack) performs no filesystem or network work at
// all before being pruned.
func ReconcileHeraReclaims(d *db.DB, runner SessionChecker) (Summary, error) {
	var sum Summary

	candidates, err := d.HeraNukeReclaimCandidates()
	if err != nil {
		return sum, err
	}
	sum.Candidates = len(candidates)
	if len(candidates) == 0 {
		return sum, nil
	}

	cfg := d.Config()
	ctx := context.Background()

	allTasks, err := d.Tasks()
	if err != nil {
		return sum, err
	}
	byBaseBranch := make(map[string]*model.Task, len(allTasks))
	for _, t := range allTasks {
		if t.BaseBranch != "" {
			byBaseBranch[t.BaseBranch] = t
		}
	}
	tipVerdicts := make(map[string]mergesafety.Verdict)

	for _, t := range candidates {
		repoDir := agent.ResolveDir(t, cfg)
		if t.Worktree != "" && agent.DirExists(t.Worktree) {
			agent.RemoveWorktreeAndBranch(t.Worktree, t.Branch, repoDir)
			sum.WorktreesRetried++
			uxlog.Log("[hera] reclaim sweep: retried worktree+branch removal task=%s", t.ID)
		}

		sum.StackedBranchesDeleted += reconcileStackedBranches(ctx, d, cfg, byBaseBranch, t, tipVerdicts)

		if runner.HasSession(t.ID) {
			sum.SkippedLiveSession++
			uxlog.Log("[hera] reclaim sweep: prune deferred task=%s (session still live)", t.ID)
			continue
		}
		pruned, _, pErr := d.PruneTasks([]string{t.ID})
		if pErr != nil {
			uxlog.Log("[hera] reclaim sweep: prune failed task=%s: %v", t.ID, pErr)
			continue
		}
		if len(pruned) > 0 {
			sum.Pruned++
			uxlog.Log("[hera] reclaim sweep: pruned task row %s", t.ID)
		}
	}

	uxlog.Log("[hera] reclaim sweep: candidates=%d worktrees_retried=%d stacked_branches_deleted=%d pruned=%d skipped_live_session=%d",
		sum.Candidates, sum.WorktreesRetried, sum.StackedBranchesDeleted, sum.Pruned, sum.SkippedLiveSession)

	return sum, nil
}

// walkStackForward walks a base_branch stack forward from x exactly like
// mergesafety.ResolveStackTip (same cycle guard, same "no descendant found"
// terminal condition), except it ALSO returns every task visited along the
// way — INCLUDING x, EXCLUDING the resolved tip — so the caller can consider
// each one for stacked-branch deletion once the tip itself classifies safe.
// ok is false on the same cycle condition ResolveStackTip fails closed on;
// an empty chain (tip == x) means x has no descendant in this scope, exactly
// mirroring ClassifyStackInferred's own "no descendant found" case.
func walkStackForward(x *model.Task, byBaseBranch map[string]*model.Task) (chain []*model.Task, tip *model.Task, ok bool) {
	seen := make(map[string]bool)
	cur := x
	for {
		if cur.Branch != "" {
			if seen[cur.Branch] {
				return nil, nil, false
			}
			seen[cur.Branch] = true
		}
		next, exists := byBaseBranch[cur.Branch]
		if !exists || next == nil {
			return chain, cur, true
		}
		chain = append(chain, cur)
		cur = next
	}
}

// reconcileStackedBranches best-effort re-discovers and retries deleting any
// additional stacked branch in candidate x's base_branch chain — a task
// branched off x (or off a descendant of x) that never got a standalone PR
// of its own, so only the chain's resolved tip classifies safe via ordinary
// Tier A/B (fix-hera-nuke-cleanup design.md Decision D5: the operator's
// exclusion choice at nuke time is the one piece of durable state this
// sweep can't re-derive, so everything else — INCLUDING which branches are
// even candidates — is re-computed fresh every run). A branch the operator
// recorded excluded (db.ExcludedCleanupBranches) is never touched, however
// its chain classifies.
//
// This is secondary to the core worktree-retry/prune sweep above: any error
// here is logged and skipped, never surfaced to the caller, and a resolution
// failure (no project, no repo, tip not confirmed safe) simply results in
// zero deletions for this candidate.
func reconcileStackedBranches(ctx context.Context, d *db.DB, cfg config.Config, byBaseBranch map[string]*model.Task, x *model.Task, tipVerdicts map[string]mergesafety.Verdict) int {
	chain, tip, ok := walkStackForward(x, byBaseBranch)
	if !ok || len(chain) == 0 {
		return 0
	}

	repoDir, repoSlug, defaultRef, defaultShort := projectMergeSafetyContext(ctx, cfg, x)
	if repoDir == "" {
		return 0
	}

	tipVerdict, cached := tipVerdicts[tip.Branch]
	if !cached {
		v, err := mergesafety.Classify(ctx, mergesafety.Params{
			RepoDir:       repoDir,
			RepoSlug:      repoSlug,
			Branch:        tip.Branch,
			DefaultRef:    defaultRef,
			DefaultShort:  defaultShort,
			TaskCreatedAt: tip.CreatedAt,
		})
		if err != nil {
			uxlog.Log("[hera] reclaim sweep: stack tip classify failed task=%s tip=%s: %v", x.ID, tip.ID, err)
			return 0
		}
		tipVerdict = v
		tipVerdicts[tip.Branch] = v
	}
	if !tipVerdict.Safe {
		return 0
	}

	deleted := 0
	for _, y := range chain {
		excluded, err := d.ExcludedCleanupBranches(y.ID)
		if err != nil {
			uxlog.Log("[hera] reclaim sweep: read excluded branches failed task=%s: %v", y.ID, err)
			continue
		}
		if slices.Contains(excluded, y.Branch) {
			continue
		}

		v, err := mergesafety.ClassifyStackInferred(ctx, mergesafety.StackParams{
			Task:         y,
			ByBaseBranch: byBaseBranch,
			RepoDir:      repoDir,
			RepoSlug:     repoSlug,
			DefaultRef:   defaultRef,
			DefaultShort: defaultShort,
		}, tipVerdicts)
		if err != nil {
			uxlog.Log("[hera] reclaim sweep: stack-inferred classify failed task=%s: %v", y.ID, err)
			continue
		}
		if !v.Safe {
			continue
		}
		if !branchExistsOnOrigin(repoDir, y.Branch) {
			continue
		}

		agent.DeleteBranch(repoDir, y.Branch)
		agent.DeleteRemoteBranch(repoDir, y.Branch)
		deleted++
		uxlog.Log("[hera] reclaim sweep: deleted stacked branch %q (task=%s) confirmed via %s", y.Branch, y.ID, v.Tier)
	}
	return deleted
}

// projectMergeSafetyContext resolves the shared, project-level
// mergesafety.Params/StackParams fields for task t's project — the same
// resolution steps internal/api/cleanup_candidates.go's cleanupCandidateFor
// performs, reused here rather than reimplemented. A task whose project no
// longer exists in config (or has an empty path) returns an empty repoDir;
// callers treat that as unresolvable, per Classify's own "no repo
// resolvable" fail-closed handling.
func projectMergeSafetyContext(ctx context.Context, cfg config.Config, t *model.Task) (repoDir, repoSlug, defaultRef, defaultShort string) {
	repoDir = agent.ResolveDir(t, cfg)
	if repoDir == "" {
		return "", "", "", ""
	}
	if slug, ok := gitutil.ResolveDefaultRepo(ctx, repoDir); ok {
		repoSlug = slug
	}
	proj := cfg.Projects[t.Project]
	if short, ref, err := gitutil.ResolveDefaultBranch(repoDir, proj.Branch); err == nil {
		defaultShort, defaultRef = short, ref
	}
	return repoDir, repoSlug, defaultRef, defaultShort
}

// branchExistsOnOrigin reports whether branch still has a remote-tracking
// ref under origin — a purely local check (git for-each-ref against the
// repo's own refs/remotes/, no fetch), mirroring Tier A's own no-fetch
// staleness tradeoff rather than a live network round trip.
func branchExistsOnOrigin(repoDir, branch string) bool {
	target := "origin/" + branch
	return slices.Contains(gitutil.ListRemoteBranches(repoDir), target)
}
