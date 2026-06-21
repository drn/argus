package gitutil

import (
	"context"
	"net/url"
	"strings"
)

// ParsePRRepo extracts "owner/name" from a github.com PR (or repo) url. It
// tolerates a trailing slash and the full "/pull/<n>" suffix. Returns false
// for an empty string, a non-url, or a non-github host — the caller then falls
// back to the worktree's default git remote (Decision 2).
func ParsePRRepo(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", false
	}
	if !strings.EqualFold(u.Host, "github.com") {
		return "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	return parts[0] + "/" + parts[1], true
}

// BranchRepoInput is one task's contribution to a poll cycle's grouping: its
// alias ID (sanitized, GraphQL-safe), its branch name, the worktree directory
// (for the git-remote fallback), and its cached PR url (authoritative when set).
type BranchRepoInput struct {
	ID        string
	Branch    string
	Worktree  string
	CachedURL string
}

// GroupBranchesByRepo buckets inputs by their resolved PR repo ("owner/name"),
// returning repo → branch → alias-id. Resolution is url-first (ParsePRRepo on
// CachedURL), falling back to resolveDefault(ctx, worktree) — the worktree's
// default GitHub repo as gh would target it. Inputs that resolve to neither are
// dropped: without an owner/name they cannot be queried.
func GroupBranchesByRepo(ctx context.Context, inputs []BranchRepoInput, resolveDefault func(ctx context.Context, worktree string) (string, bool)) map[string]map[string]string {
	groups := map[string]map[string]string{}
	for _, in := range inputs {
		repo, ok := ParsePRRepo(in.CachedURL)
		if !ok {
			if resolveDefault == nil {
				continue
			}
			repo, ok = resolveDefault(ctx, in.Worktree)
			if !ok {
				continue
			}
		}
		if groups[repo] == nil {
			groups[repo] = map[string]string{}
		}
		groups[repo][in.Branch] = in.ID
	}
	return groups
}
