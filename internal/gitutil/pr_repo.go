package gitutil

import (
	"context"
	"net/url"
	"strings"
	"time"
)

// prRepoRunner is the test seam for resolving a worktree's default GitHub repo,
// mirroring prRunner / prGraphQLRunner. The real implementation shells
// `gh repo view --json nameWithOwner -q .nameWithOwner` in the worktree dir —
// the same repo gh would target there (honoring the gh default-repo override and
// the remote set) — and returns trimmed stdout, the exit code, and any
// exec-level error. Tests swap this variable to avoid spawning a process.
var prRepoRunner = func(ctx context.Context, dir string, args ...string) (string, int, error) {
	return prRunner(ctx, dir, args...)
}

// ResolveDefaultRepo returns the "owner/name" of the GitHub repo gh would target
// for the given worktree directory — the Decision 2 fallback used when a task has
// no cached PR url to parse a repo from. It is the default binding for the
// daemon's prResolveRepo seam.
//
// Returns ("", false) when the worktree is empty, gh is absent/unauthenticated,
// the command fails, or the output is not a valid "owner/name" — the caller then
// drops the task from grouping (it cannot be queried without an owner/name).
func ResolveDefaultRepo(ctx context.Context, worktree string) (string, bool) {
	if worktree == "" {
		return "", false
	}
	const timeout = 5 * time.Second
	fetchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	out, code, err := prRepoRunner(fetchCtx, worktree, "repo", "view", "--json", "nameWithOwner", "-q", ".nameWithOwner")
	if err != nil || code != 0 {
		return "", false
	}
	repo := strings.TrimSpace(out)
	if _, _, ok := splitRepo(repo); !ok {
		return "", false
	}
	return repo, true
}

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
// returning repo → branch → []alias-id. Resolution is url-first (ParsePRRepo on
// CachedURL), falling back to resolveDefault(ctx, worktree) — the worktree's
// default GitHub repo as gh would target it. Inputs that resolve to neither are
// dropped: without an owner/name they cannot be queried.
//
// The inner value is a SLICE of alias ids because two distinct tasks in the same
// repo can share a branch (e.g. a stacked retry pointed at the same head ref).
// They share the SAME PR, so the caller queries that branch once and fans the
// single PRResult out to every alias in the slice — no task is silently dropped
// (the old branch→id map would have let the second task overwrite the first,
// losing it from the query, the write, and the counts).
func GroupBranchesByRepo(ctx context.Context, inputs []BranchRepoInput, resolveDefault func(ctx context.Context, worktree string) (string, bool)) map[string]map[string][]string {
	groups := map[string]map[string][]string{}
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
			groups[repo] = map[string][]string{}
		}
		groups[repo][in.Branch] = append(groups[repo][in.Branch], in.ID)
	}
	return groups
}
