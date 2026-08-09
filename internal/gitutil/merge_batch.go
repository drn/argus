package gitutil

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// MergeCandidate is one pull request GitHub returned for a given head ref
// name, as raw fields for the merge-safety classifier to interpret (State is
// the raw GraphQL enum — OPEN, CLOSED, or MERGED — deliberately not
// collapsed the way PRResult's State is, since the classifier needs to tell
// a genuinely merged PR apart from one that was merely closed).
type MergeCandidate struct {
	State       string
	BaseRefName string
	MergedAt    string // RFC3339; empty when State != "MERGED"
	CreatedAt   string // RFC3339
	URL         string
}

type mergeCandidateJSON struct {
	State       string `json:"state"`
	BaseRefName string `json:"baseRefName"`
	MergedAt    string `json:"mergedAt"`
	CreatedAt   string `json:"createdAt"`
	URL         string `json:"url"`
}

type mergeCandidateConnection struct {
	Nodes []mergeCandidateJSON `json:"nodes"`
}

// FetchMergeCandidatesBatch resolves up to 5 most-recently-created pull
// requests per head ref name, for every branch in branches, with a single
// aliased GraphQL query against repo ("owner/name") — the merge-safety
// classifier's Tier B lookup. branches maps each branch name to its alias id
// (already sanitized by the caller into a valid GraphQL identifier); the
// returned map is keyed by branch name.
//
// Unlike FetchPRStatesBatch's first:1 (which only needs the single
// most-recent PR for an ACTIVE task's own branch), this requests first:5 so
// the caller can apply its own plausibility filtering across multiple
// candidates — necessary because a branch name can be reused across
// unrelated tasks over time, and only the classifier (which knows the
// task's own creation time and the expected base branch) can tell which
// candidate, if any, actually corresponds to it.
//
// Reuses the exact same execution primitive as FetchPRStatesBatch
// (runAliasedRepoQuery: temp-file `gh api graphql` invocation, rate-limit-
// cost parsing) — see that function's doc for the shared contract (empty
// branches → no query; a whole-query error → (nil, 0, err); an unknown
// alias in the response is ignored).
func FetchMergeCandidatesBatch(ctx context.Context, repo string, branches map[string]string) (map[string][]MergeCandidate, int, error) {
	out := map[string][]MergeCandidate{}
	if len(branches) == 0 {
		return out, 0, nil
	}

	owner, name, ok := splitRepo(repo)
	if !ok {
		return nil, 0, fmt.Errorf("invalid repo %q (want owner/name)", repo)
	}

	query := buildMergeCandidateQuery(owner, name, branches)
	repoMap, cost, err := runAliasedRepoQuery(ctx, query)
	if err != nil {
		return nil, 0, err
	}

	aliasToBranch := make(map[string]string, len(branches))
	for branch, alias := range branches {
		aliasToBranch[alias] = branch
	}

	for alias, rawConn := range repoMap {
		branch, known := aliasToBranch[alias]
		if !known {
			continue
		}
		var conn mergeCandidateConnection
		if err := json.Unmarshal(rawConn, &conn); err != nil {
			return nil, 0, fmt.Errorf("parse graphql alias %q: %w", alias, err)
		}
		for _, n := range conn.Nodes {
			out[branch] = append(out[branch], MergeCandidate{
				State:       n.State,
				BaseRefName: n.BaseRefName,
				MergedAt:    n.MergedAt,
				CreatedAt:   n.CreatedAt,
				URL:         n.URL,
			})
		}
	}

	return out, cost, nil
}

// buildMergeCandidateQuery assembles the single aliased GraphQL query for a
// repo group, requesting up to 5 most-recent-by-creation candidates per head
// ref name. Aliases are emitted in sorted order for deterministic output.
func buildMergeCandidateQuery(owner, name string, branches map[string]string) string {
	type pair struct{ branch, alias string }
	pairs := make([]pair, 0, len(branches))
	for branch, alias := range branches {
		pairs = append(pairs, pair{branch: branch, alias: alias})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].alias < pairs[j].alias })

	var b strings.Builder
	b.WriteString("query {\n")
	b.WriteString("  rateLimit { cost remaining }\n")
	fmt.Fprintf(&b, "  repo: repository(owner: %q, name: %q) {\n", owner, name)
	for _, p := range pairs {
		fmt.Fprintf(&b,
			"    %s: pullRequests(headRefName: %q, first: 5, orderBy: {field: CREATED_AT, direction: DESC}) { nodes { state baseRefName mergedAt createdAt url } }\n",
			p.alias, p.branch)
	}
	b.WriteString("  }\n")
	b.WriteString("}\n")
	return b.String()
}
