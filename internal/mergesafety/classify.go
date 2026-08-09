// Package mergesafety determines whether a task's git branch has been
// confirmed to land in its project's default branch, before that branch (and
// its worktree) gets destroyed by a Hera nuke or a cleanup sweep.
//
// It fails closed by design: every case it cannot positively confirm — a
// deleted branch with no matching merged PR, an ambiguous branch-name reuse,
// an unresolvable repo, a transient network failure — returns "not safe".
// It never guesses toward "safe" without direct evidence. See
// openspec/changes/add-merge-safety-classifier/design.md for the full
// rationale (in particular why Tier A never fetches, and why Tier B
// evaluates up to 5 candidates instead of trusting the single most recent
// one).
package mergesafety

import (
	"context"
	"fmt"
	"time"

	"github.com/drn/argus/internal/gitutil"
)

// Tier labels surfaced on a confirmed-safe Verdict.
const (
	TierLocalAncestor = "local-ancestor"
	TierMergedPR      = "merged-pr"
)

// skewSlack is the clock-skew allowance applied when checking that a
// candidate PR's creation time does not predate the task it's being checked
// against — see the "branch-name reuse" guard in the package doc.
const skewSlack = 6 * time.Hour

// Verdict is the classifier's answer for one task's branch.
type Verdict struct {
	// Safe is true only when the branch's work is confirmed to have landed
	// in the project's default branch.
	Safe bool
	// Tier is which evidence tier produced a Safe=true verdict (TierLocalAncestor
	// or TierMergedPR). Empty when Safe is false.
	Tier string
	// Reason is a human-readable explanation, always populated — for a Safe
	// verdict it names the evidence; for a not-safe verdict it names why.
	Reason string
}

// Params is one task's classification request.
type Params struct {
	// RepoDir is the local git repository directory to check Tier A
	// against (a live task's worktree, or a project's checkout path for an
	// already-archived task). Empty skips Tier A entirely.
	RepoDir string
	// RepoSlug is "owner/name" for the Tier B GitHub lookup. Empty skips
	// Tier B entirely — a task with neither a resolvable RepoDir nor a
	// RepoSlug always classifies as not-safe ("unresolvable repo").
	RepoSlug string
	// Branch is the task's branch name.
	Branch string
	// DefaultRef is the project's default branch as a ref usable directly
	// with `git merge-base` (e.g. "origin/main", "drn/master") — see
	// gitutil.ResolveDefaultBranch.
	DefaultRef string
	// DefaultShort is the project's default branch's short name (e.g.
	// "main", "master") — compared against a candidate PR's baseRefName.
	DefaultShort string
	// TaskCreatedAt guards Tier B against branch-name reuse: a candidate PR
	// that predates the task cannot be this task's own merge.
	TaskCreatedAt time.Time
}

// fetchMergeCandidates is the Tier B seam, defaulting to
// gitutil.FetchMergeCandidatesBatch. Tests swap it to avoid any network call.
var fetchMergeCandidates = gitutil.FetchMergeCandidatesBatch

// Classify determines whether p.Branch's work is confirmed to have landed in
// p.DefaultRef. The returned error is reserved for a fundamentally malformed
// call (not currently used — every "couldn't confirm" outcome, including a
// Tier B transport failure, is folded into a not-safe Verdict rather than an
// error, since both mean the same thing to a caller: don't treat this as
// safe, and show the operator why).
func Classify(ctx context.Context, p Params) (Verdict, error) {
	if v, ok := classifyLocal(p.RepoDir, p.Branch, p.DefaultRef); ok {
		return v, nil
	}

	if p.RepoSlug == "" {
		return Verdict{Safe: false, Reason: "no repo resolvable for a merged-PR lookup"}, nil
	}

	const alias = "b0"
	res, _, err := fetchMergeCandidates(ctx, p.RepoSlug, map[string]string{p.Branch: alias})
	if err != nil {
		return Verdict{Safe: false, Reason: fmt.Sprintf("merged-PR lookup failed: %v", err)}, nil
	}

	return evaluateCandidates(res[p.Branch], p.DefaultShort, p.TaskCreatedAt), nil
}

// classifyLocal runs Tier A: true (ok=true) only when the branch resolves
// locally AND is a confirmed ancestor of defaultRef. Any other outcome —
// branch doesn't resolve, ancestor check fails, or it resolves but isn't an
// ancestor — returns ok=false so the caller falls through to Tier B.
func classifyLocal(repoDir, branch, defaultRef string) (Verdict, bool) {
	if repoDir == "" || branch == "" || defaultRef == "" {
		return Verdict{}, false
	}
	ok, err := gitutil.IsAncestor(repoDir, branch, defaultRef)
	if err != nil || !ok {
		return Verdict{}, false
	}
	return Verdict{
		Safe:   true,
		Tier:   TierLocalAncestor,
		Reason: fmt.Sprintf("branch %q is an ancestor of %q", branch, defaultRef),
	}, true
}

// evaluateCandidates applies the plausibility filter (state==MERGED,
// baseRefName==defaultShort, createdAt not earlier than taskCreatedAt minus
// skewSlack) to a Tier B candidate list, requiring EXACTLY one match.
func evaluateCandidates(candidates []gitutil.MergeCandidate, defaultShort string, taskCreatedAt time.Time) Verdict {
	var plausible []gitutil.MergeCandidate
	for _, c := range candidates {
		if c.State != "MERGED" || c.BaseRefName != defaultShort {
			continue
		}
		createdAt, err := time.Parse(time.RFC3339, c.CreatedAt)
		if err != nil {
			continue
		}
		if createdAt.Before(taskCreatedAt.Add(-skewSlack)) {
			continue
		}
		plausible = append(plausible, c)
	}

	switch len(plausible) {
	case 1:
		return Verdict{
			Safe:   true,
			Tier:   TierMergedPR,
			Reason: fmt.Sprintf("merged PR %s confirmed merged into %q", plausible[0].URL, defaultShort),
		}
	case 0:
		return Verdict{Safe: false, Reason: "no matching merged pull request found"}
	default:
		return Verdict{Safe: false, Reason: fmt.Sprintf("ambiguous: branch name matches %d merged pull requests", len(plausible))}
	}
}
