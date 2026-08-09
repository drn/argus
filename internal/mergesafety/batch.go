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

// ClassifyBatch classifies many candidates at once. Tier A runs per
// candidate (local, cheap — no grouping needed). Candidates that don't
// resolve via Tier A are grouped by RepoSlug and issued as ONE batched Tier B
// GraphQL call per repo — the per-repo-batching contract every caller of
// this package's network tier must follow to stay within the shared GitHub
// GraphQL budget (see the classifier's design doc).
func ClassifyBatch(ctx context.Context, candidates []Candidate) map[string]Verdict {
	results := make(map[string]Verdict, len(candidates))

	type repoGroup struct {
		branches map[string]string // branch -> alias
		members  []Candidate       // every candidate needing this repo's Tier B lookup
	}
	groups := map[string]*repoGroup{}

	for _, c := range candidates {
		if v, ok := classifyLocal(c.RepoDir, c.Branch, c.DefaultRef); ok {
			results[c.ID] = v
			continue
		}
		if c.RepoSlug == "" {
			results[c.ID] = Verdict{Safe: false, Reason: "no repo resolvable for a merged-PR lookup"}
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
				results[c.ID] = Verdict{Safe: false, Reason: fmt.Sprintf("merged-PR lookup failed: %v", err)}
				continue
			}
			results[c.ID] = evaluateCandidates(candResults[c.Branch], c.DefaultShort, c.TaskCreatedAt)
		}
	}

	return results
}
