package gitutil

import (
	"context"
	"errors"
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// These tests cover the batched GraphQL PR-state fetcher (Stage 1, Prove-It).
// The functions under test (FetchPRStatesBatch, the prGraphQLRunner seam,
// ParsePRRepo, GroupBranchesByRepo) do not exist yet — these tests prove the
// gap and will go green once Stage 2/3 land the implementation.

// graphQLSuccess is the JSON GitHub returns for an aliased batch query. The
// alias keys are sanitized task ids (tN); each resolves to a pullRequests
// connection with a nodes array. An empty nodes array means "no PR".
//
// This particular fixture covers, in one query:
//   - t1: an open, non-draft, approved PR
//   - t2: NO PR (empty nodes) → none
//   - t3: a MERGED PR whose head branch was deleted (still resolves) → merged-closed
const graphQLSuccess = `{
  "data": {
    "rateLimit": {"cost": 1, "remaining": 4999},
    "repo": {
      "t1": {"nodes": [{"state": "OPEN", "isDraft": false, "reviewDecision": "APPROVED", "url": "https://github.com/drn/argus/pull/11"}]},
      "t2": {"nodes": []},
      "t3": {"nodes": [{"state": "MERGED", "isDraft": false, "reviewDecision": "APPROVED", "url": "https://github.com/drn/argus/pull/13"}]}
    }
  }
}`

// fakeGraphQLRunner returns canned output and records how many times it was
// invoked plus the args of each call, so tests can assert query-count and
// chunking behavior.
type fakeGraphQLRunner struct {
	out   string
	code  int
	err   error
	calls int
	args  [][]string
}

func (f *fakeGraphQLRunner) run(_ context.Context, _ string, args ...string) (string, int, error) {
	f.calls++
	f.args = append(f.args, args)
	return f.out, f.code, f.err
}

// installGraphQLRunner swaps the package-level seam for the duration of a test.
func installGraphQLRunner(t *testing.T, f func(ctx context.Context, dir string, args ...string) (string, int, error)) {
	t.Helper()
	orig := prGraphQLRunner
	t.Cleanup(func() { prGraphQLRunner = orig })
	prGraphQLRunner = f
}

// Scenario: Resolves repo from cached PR url / one query per repo (batch).
// Scenario: Reports no PR as none.
// Scenario: Resolves state for a deleted merged branch.
// Acceptance: "same fields the cache stores: state, isDraft-derived, reviewDecision, url".
func TestFetchPRStatesBatch_ParsesPerBranchResults(t *testing.T) {
	fake := &fakeGraphQLRunner{out: graphQLSuccess, code: 0}
	installGraphQLRunner(t, fake.run)

	// branch→id association the caller holds; the fetcher aliases by id.
	branches := map[string]string{
		"argus/open":    "t1",
		"argus/none":    "t2",
		"argus/deleted": "t3",
	}

	res, cost, err := FetchPRStatesBatch(context.Background(), "drn/argus", branches)
	testutil.NoError(t, err)

	// Exactly ONE query for the whole batch, regardless of branch count.
	testutil.Equal(t, fake.calls, 1)

	// The GraphQL cost is parsed from data.rateLimit.cost and surfaced.
	testutil.Equal(t, cost, 1)

	testutil.Equal(t, res["argus/open"].State, model.PRApproved)
	testutil.Equal(t, res["argus/open"].URL, "https://github.com/drn/argus/pull/11")
	testutil.Equal(t, res["argus/open"].Merged, false)

	// Empty nodes → none.
	testutil.Equal(t, res["argus/none"].State, model.PRNone)
	testutil.Equal(t, res["argus/none"].Merged, false)

	// Merged PR with a deleted head branch still resolves to merged-closed.
	testutil.Equal(t, res["argus/deleted"].State, model.PRMergedClosed)
	testutil.Equal(t, res["argus/deleted"].URL, "https://github.com/drn/argus/pull/13")
	testutil.Equal(t, res["argus/deleted"].Merged, true)
}

// TestMapBatchNode_MergedDistinguishesFromClosed pins PRResult.Merged
// (add-hera-accept-lifecycle): State collapses MERGED and CLOSED into the
// same PRMergedClosed value, but Merged must tell them apart – the PR-merge
// coordinator nudge must never fire for an unmerged close.
func TestMapBatchNode_MergedDistinguishesFromClosed(t *testing.T) {
	merged := mapBatchNode([]prJSON{{State: "MERGED", URL: "https://example/pr/1"}})
	testutil.Equal(t, merged.State, model.PRMergedClosed)
	testutil.Equal(t, merged.Merged, true)

	closed := mapBatchNode([]prJSON{{State: "CLOSED", URL: "https://example/pr/2"}})
	testutil.Equal(t, closed.State, model.PRMergedClosed)
	testutil.Equal(t, closed.Merged, false)

	open := mapBatchNode([]prJSON{{State: "OPEN", URL: "https://example/pr/3"}})
	testutil.Equal(t, open.Merged, false)

	none := mapBatchNode(nil)
	testutil.Equal(t, none.State, model.PRNone)
	testutil.Equal(t, none.Merged, false)
}

// Scenario: One query per repo, not per task — the builder emits a single
// aliased query covering every branch in the group (one runner invocation).
func TestFetchPRStatesBatch_OneQueryForNBranches(t *testing.T) {
	fake := &fakeGraphQLRunner{out: graphQLSuccess, code: 0}
	installGraphQLRunner(t, fake.run)

	branches := map[string]string{
		"argus/open":    "t1",
		"argus/none":    "t2",
		"argus/deleted": "t3",
	}
	_, _, err := FetchPRStatesBatch(context.Background(), "drn/argus", branches)
	testutil.NoError(t, err)
	testutil.Equal(t, fake.calls, 1)
}

// Acceptance: a whole-query error returns an error so the caller keeps-stale.
func TestFetchPRStatesBatch_QueryErrorReturnsError(t *testing.T) {
	fake := &fakeGraphQLRunner{out: "", code: 1, err: errors.New("network timeout")}
	installGraphQLRunner(t, fake.run)

	branches := map[string]string{"argus/open": "t1"}
	_, _, err := FetchPRStatesBatch(context.Background(), "drn/argus", branches)
	testutil.Error(t, err)
}

// An invalid repo string (no owner/name split) is rejected before any query.
func TestFetchPRStatesBatch_InvalidRepoNoQuery(t *testing.T) {
	fake := &fakeGraphQLRunner{out: graphQLSuccess, code: 0}
	installGraphQLRunner(t, fake.run)

	_, _, err := FetchPRStatesBatch(context.Background(), "not-a-repo", map[string]string{"argus/open": "t1"})
	testutil.Error(t, err)
	testutil.Equal(t, fake.calls, 0)
}

// A non-zero exit with no runner error (e.g. gh prints to stderr and exits 1)
// is still surfaced as an error so the caller keeps-stale.
func TestFetchPRStatesBatch_NonZeroExitReturnsError(t *testing.T) {
	fake := &fakeGraphQLRunner{out: "boom", code: 1, err: nil}
	installGraphQLRunner(t, fake.run)

	_, _, err := FetchPRStatesBatch(context.Background(), "drn/argus", map[string]string{"argus/open": "t1"})
	testutil.Error(t, err)
}

// Malformed JSON from gh is an error, not a silent empty result.
func TestFetchPRStatesBatch_BadJSONReturnsError(t *testing.T) {
	fake := &fakeGraphQLRunner{out: "{not json", code: 0}
	installGraphQLRunner(t, fake.run)

	_, _, err := FetchPRStatesBatch(context.Background(), "drn/argus", map[string]string{"argus/open": "t1"})
	testutil.Error(t, err)
}

// A well-formed envelope whose per-alias connection is malformed is an error.
func TestFetchPRStatesBatch_BadAliasConnReturnsError(t *testing.T) {
	const badAlias = `{
  "data": {
    "rateLimit": {"cost": 1, "remaining": 4999},
    "repo": {"t1": "not-an-object"}
  }
}`
	fake := &fakeGraphQLRunner{out: badAlias, code: 0}
	installGraphQLRunner(t, fake.run)

	_, _, err := FetchPRStatesBatch(context.Background(), "drn/argus", map[string]string{"argus/open": "t1"})
	testutil.Error(t, err)
}

// An alias in the response that the caller never asked for is ignored, not mapped.
func TestFetchPRStatesBatch_UnknownAliasIgnored(t *testing.T) {
	const extraAlias = `{
  "data": {
    "rateLimit": {"cost": 2, "remaining": 4998},
    "repo": {
      "t1": {"nodes": [{"state": "OPEN", "isDraft": false, "reviewDecision": "APPROVED", "url": "https://github.com/drn/argus/pull/11"}]},
      "t99": {"nodes": [{"state": "OPEN", "isDraft": false, "url": "https://github.com/drn/argus/pull/99"}]}
    }
  }
}`
	fake := &fakeGraphQLRunner{out: extraAlias, code: 0}
	installGraphQLRunner(t, fake.run)

	res, cost, err := FetchPRStatesBatch(context.Background(), "drn/argus", map[string]string{"argus/open": "t1"})
	testutil.NoError(t, err)
	testutil.Equal(t, cost, 2)
	testutil.Equal(t, len(res), 1)
	testutil.Equal(t, res["argus/open"].State, model.PRApproved)
}

// Empty branch set is a no-op: no query is issued and an empty map returns.
func TestFetchPRStatesBatch_EmptyBranchesNoQuery(t *testing.T) {
	fake := &fakeGraphQLRunner{out: graphQLSuccess, code: 0}
	installGraphQLRunner(t, fake.run)

	res, cost, err := FetchPRStatesBatch(context.Background(), "drn/argus", map[string]string{})
	testutil.NoError(t, err)
	testutil.Equal(t, fake.calls, 0)
	testutil.Equal(t, len(res), 0)
	testutil.Equal(t, cost, 0)
}

// --- Repo resolution + grouping (Decision 2) ---

// Scenario: Resolves repo from cached PR url.
func TestParsePRRepo_FromCachedURL(t *testing.T) {
	for _, tc := range []struct {
		name string
		url  string
		want string
	}{
		{name: "https pull url", url: "https://github.com/drn/argus/pull/780", want: "drn/argus"},
		{name: "anutron repo", url: "https://github.com/anutron/things-mcp/pull/3", want: "anutron/things-mcp"},
		{name: "trailing slash tolerated", url: "https://github.com/drn/argus/", want: "drn/argus"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, ok := ParsePRRepo(tc.url)
			testutil.Equal(t, ok, true)
			testutil.Equal(t, repo, tc.want)
		})
	}
}

// An empty or non-github url is not parseable; caller falls back to git remote.
func TestParsePRRepo_Unparseable(t *testing.T) {
	for _, url := range []string{"", "not a url", "https://example.com/foo/bar"} {
		_, ok := ParsePRRepo(url)
		testutil.Equal(t, ok, false)
	}
}

// Scenario: Group branches sharing a repo into one group.
// Scenario: Falls back to worktree default repo (resolveDefault seam) when no url.
func TestGroupBranchesByRepo(t *testing.T) {
	// Two tasks carry a cached url on drn/argus, one has none (→ default repo
	// via the stubbed resolver), one carries a url on a different repo.
	inputs := []BranchRepoInput{
		{ID: "t1", Branch: "argus/a", Worktree: "/wt/t1", CachedURL: "https://github.com/drn/argus/pull/1"},
		{ID: "t2", Branch: "argus/b", Worktree: "/wt/t2", CachedURL: "https://github.com/drn/argus/pull/2"},
		{ID: "t3", Branch: "argus/c", Worktree: "/wt/t3", CachedURL: ""},
		{ID: "t4", Branch: "feat/x", Worktree: "/wt/t4", CachedURL: "https://github.com/anutron/gmail-mcp/pull/9"},
	}

	// Stub the worktree-default resolver: t3 → drn/argus (joins the dominant group).
	resolveDefault := func(_ context.Context, worktree string) (string, bool) {
		if worktree == "/wt/t3" {
			return "drn/argus", true
		}
		return "", false
	}

	groups := GroupBranchesByRepo(context.Background(), inputs, resolveDefault)

	// drn/argus has 3 distinct branches (t1, t2, t3 — cached + default fallback);
	// anutron/gmail-mcp has 1.
	testutil.Equal(t, len(groups["drn/argus"]), 3)
	testutil.Equal(t, len(groups["anutron/gmail-mcp"]), 1)

	// Each group maps branch → []sanitized id (the alias keys). Distinct branches
	// carry a single-element slice.
	testutil.DeepEqual(t, groups["drn/argus"]["argus/a"], []string{"t1"})
	testutil.DeepEqual(t, groups["drn/argus"]["argus/c"], []string{"t3"})
	testutil.DeepEqual(t, groups["anutron/gmail-mcp"]["feat/x"], []string{"t4"})
}

// Two distinct tasks in the same repo sharing a branch (same head ref → same PR)
// must BOTH appear under that branch — neither silently overwritten/dropped.
func TestGroupBranchesByRepo_SharedBranchKeepsBothTasks(t *testing.T) {
	inputs := []BranchRepoInput{
		{ID: "t1", Branch: "argus/shared", Worktree: "/wt/t1", CachedURL: "https://github.com/drn/argus/pull/1"},
		{ID: "t2", Branch: "argus/shared", Worktree: "/wt/t2", CachedURL: "https://github.com/drn/argus/pull/1"},
	}
	resolveDefault := func(_ context.Context, _ string) (string, bool) { return "", false }

	groups := GroupBranchesByRepo(context.Background(), inputs, resolveDefault)

	// One repo, one distinct branch, but BOTH task aliases under it.
	testutil.Equal(t, len(groups), 1)
	testutil.Equal(t, len(groups["drn/argus"]), 1)
	testutil.DeepEqual(t, groups["drn/argus"]["argus/shared"], []string{"t1", "t2"})
}

// A task whose repo cannot be resolved at all (no url, default resolver fails)
// is dropped from grouping — it cannot be queried without an owner/name.
func TestGroupBranchesByRepo_UnresolvableDropped(t *testing.T) {
	inputs := []BranchRepoInput{
		{ID: "t1", Branch: "argus/a", Worktree: "/wt/t1", CachedURL: ""},
	}
	resolveDefault := func(_ context.Context, _ string) (string, bool) { return "", false }

	groups := GroupBranchesByRepo(context.Background(), inputs, resolveDefault)
	testutil.Equal(t, len(groups), 0)
}
