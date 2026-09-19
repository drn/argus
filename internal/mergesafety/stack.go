package mergesafety

import (
	"context"
	"fmt"

	"github.com/drn/argus/internal/gitutil"
	"github.com/drn/argus/internal/model"
)

// ResolveStackTip walks a base_branch stack forward from t — a chain of
// tasks each branched off the previous one via model.Task.BaseBranch, where
// only the last task in the chain ever opens a standalone PR — to find that
// chain's terminal task.
//
// byBaseBranch maps a branch name to the task whose BaseBranch equals it
// (i.e. the task branched FROM that branch); build it once per scope by
// indexing every candidate task on its own BaseBranch field. At each step
// the walk looks up byBaseBranch[cur.Branch] to find the task branched off
// the current one; it stops at the first task with no such descendant and
// returns that as the tip. A task with no descendant of its own (t is
// already the tip) returns t, ok=true.
//
// A cycle — revisiting a branch already seen earlier in the same walk —
// returns ok=false instead of looping forever.
func ResolveStackTip(t *model.Task, byBaseBranch map[string]*model.Task) (tip *model.Task, ok bool) {
	if t == nil {
		return nil, false
	}
	seen := make(map[string]bool)
	cur := t
	for {
		if cur.Branch != "" {
			if seen[cur.Branch] {
				return nil, false
			}
			seen[cur.Branch] = true
		}
		next, exists := byBaseBranch[cur.Branch]
		if !exists || next == nil {
			return cur, true
		}
		cur = next
	}
}

// StackParams is a not-safe candidate task's Tier D (stack-inferred)
// classification request.
type StackParams struct {
	// Task is the not-safe candidate being considered for rescue.
	Task *model.Task
	// ByBaseBranch is ResolveStackTip's lookup, scoped to the same
	// cascade/project as Task.
	ByBaseBranch map[string]*model.Task
	// RepoDir, RepoSlug, DefaultRef, DefaultShort are the same project-level
	// fields as on Params, forwarded to Classify when classifying the
	// resolved stack tip. RepoDir is also used directly for the local
	// ancestry check of Task's own branch against the tip's branch.
	RepoDir      string
	RepoSlug     string
	DefaultRef   string
	DefaultShort string
}

// ClassifyStackInferred attempts to rescue a not-safe candidate task (Tier
// D, TierStackInferred): it resolves the base_branch stack's terminal task
// via ResolveStackTip, classifies that tip via the existing Tier A/B
// Classify, and — only when the tip itself classifies confirmed-safe —
// verifies via a purely local `git merge-base --is-ancestor` check that
// p.Task's own branch is a real ancestor of the TIP's branch, never the
// project's default branch. A plain ancestry check against the default
// branch is exactly what a squash merge breaks: the squashed commit on the
// default branch is never a literal ancestor of anything in the chain's own
// history. The tip's branch still holds every earlier link's real,
// un-squashed commits, so checking against it stays sound, and still fails
// closed if anything mid-stack was rebased or cherry-picked.
//
// Every failure mode — an unresolvable/cyclic stack, no descendant found, a
// tip that doesn't classify safe, a broken ancestry check, or a hard error
// from either sub-check — leaves the returned Verdict at Safe=false and
// returns a nil error; like Classify, this tier never guesses toward safe
// and never surfaces a Go error for "couldn't confirm."
//
// tipVerdicts, if non-nil, memoizes a resolved tip's Classify verdict by
// branch name. A caller processing many candidates from the same cascade
// should share one map across calls so a tip common to several earlier
// links is classified — and, for Tier B, network-queried — exactly once.
// The map is a plain map with no internal locking: it is not safe for
// concurrent access, and a caller invoking this from multiple goroutines
// must synchronize its own reads/writes.
func ClassifyStackInferred(ctx context.Context, p StackParams, tipVerdicts map[string]Verdict) (Verdict, error) {
	tip, ok := ResolveStackTip(p.Task, p.ByBaseBranch)
	if !ok {
		return Verdict{Safe: false, Reason: "stack-inferred: cycle detected walking the base_branch chain"}, nil
	}
	if tip == p.Task {
		return Verdict{Safe: false, Reason: "stack-inferred: no descendant found in the base_branch chain"}, nil
	}

	tipVerdict, cached := tipVerdicts[tip.Branch]
	if !cached {
		v, err := Classify(ctx, Params{
			RepoDir:       p.RepoDir,
			RepoSlug:      p.RepoSlug,
			Branch:        tip.Branch,
			DefaultRef:    p.DefaultRef,
			DefaultShort:  p.DefaultShort,
			TaskCreatedAt: tip.CreatedAt,
		})
		if err != nil {
			return Verdict{Safe: false, Reason: fmt.Sprintf("stack-inferred: tip classification failed: %v", err)}, nil
		}
		tipVerdict = v
		if tipVerdicts != nil {
			tipVerdicts[tip.Branch] = v
		}
	}
	if !tipVerdict.Safe {
		return Verdict{Safe: false, Reason: fmt.Sprintf("stack-inferred: stack tip %q not confirmed safe: %s", tip.Branch, tipVerdict.Reason)}, nil
	}

	ancestor, err := gitutil.IsAncestor(p.RepoDir, p.Task.Branch, tip.Branch)
	if err != nil || !ancestor {
		return Verdict{Safe: false, Reason: fmt.Sprintf("stack-inferred: branch %q not confirmed an ancestor of stack tip %q", p.Task.Branch, tip.Branch)}, nil
	}

	return Verdict{
		Safe: true,
		Tier: TierStackInferred,
		Reason: fmt.Sprintf("stack tip %s (branch %q) confirmed via %s: %s",
			tip.Name, tip.Branch, tipVerdict.Tier, tipVerdict.Reason),
	}, nil
}
