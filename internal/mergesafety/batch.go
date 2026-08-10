package mergesafety

import (
	"context"
	"fmt"
	"time"
)

// Candidate is one task's classification request for ClassifyBatch, keyed by
// the caller's own ID (e.g. a task ID) in the returned result map.
type Candidate struct {
	ID            string
	RepoDir       string
	RepoSlug      string
	Branch        string
	DefaultRef    string
	DefaultShort  string
	TaskCreatedAt time.Time
}

// ClassifyBatch classifies many candidates at once and returns every result
// together once the whole batch finishes. It is a synchronous wrapper over
// ClassifyBatchFunc for callers that don't need progress as it happens.
//
// A caller classifying a large candidate set — where most of it may fall
// through to Tier B — should call ClassifyBatchFunc directly instead: this
// function's all-at-once map means nothing is observable (e.g. cacheable)
// until every single repo group's network call has returned, which for a
// backlog spanning several repos can be a long, completely silent wait.
func ClassifyBatch(ctx context.Context, candidates []Candidate) map[string]Verdict {
	results := make(map[string]Verdict, len(candidates))
	ClassifyBatchFunc(ctx, candidates, func(id string, v Verdict) {
		results[id] = v
	})
	return results
}

// ClassifyBatchFunc is ClassifyBatch's incremental form: onResult is invoked
// once per candidate as soon as ITS verdict is available, in two waves —
// every Tier-A-resolved (or repo-unresolvable) candidate fires immediately,
// in input order, before any Tier B network call is even issued; then each
// Tier B repo group's members fire together the moment that one repo's
// single aliased GraphQL call returns (success or error). Tier B can't be
// split any finer than one call per repo (see the package doc), so a
// candidate set spanning multiple repos still yields one update per repo
// group rather than a single update for the entire batch — real, cacheable
// progress instead of an all-or-nothing wait.
func ClassifyBatchFunc(ctx context.Context, candidates []Candidate, onResult func(id string, v Verdict)) {
	type repoGroup struct {
		branches map[string]string // branch -> alias
		members  []Candidate       // every candidate needing this repo's Tier B lookup
	}
	groups := map[string]*repoGroup{}

	for _, c := range candidates {
		if v, ok := classifyLocal(c.RepoDir, c.Branch, c.DefaultRef); ok {
			onResult(c.ID, v)
			continue
		}
		if c.RepoSlug == "" {
			onResult(c.ID, Verdict{Safe: false, Reason: "no repo resolvable for a merged-PR lookup"})
			continue
		}
		g := groups[c.RepoSlug]
		if g == nil {
			g = &repoGroup{branches: map[string]string{}}
			groups[c.RepoSlug] = g
		}
		if _, known := g.branches[c.Branch]; !known {
			g.branches[c.Branch] = fmt.Sprintf("b%d", len(g.branches))
		}
		g.members = append(g.members, c)
	}

	for repo, g := range groups {
		candResults, _, err := fetchMergeCandidates(ctx, repo, g.branches)
		for _, c := range g.members {
			if err != nil {
				onResult(c.ID, Verdict{Safe: false, Reason: fmt.Sprintf("merged-PR lookup failed: %v", err)})
				continue
			}
			onResult(c.ID, evaluateCandidates(candResults[c.Branch], c.DefaultShort, c.TaskCreatedAt))
		}
	}
}
