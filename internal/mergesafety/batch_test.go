package mergesafety

import (
	"context"
	"testing"

	"github.com/drn/argus/internal/gitutil"
	"github.com/drn/argus/internal/testutil"
)

func TestClassifyBatch_TierAOnlyIssuesNoNetworkCall(t *testing.T) {
	dir := initRepo(t, t.TempDir())
	gitRun(t, dir, "checkout", "-b", "feature")
	addCommit(t, dir, "f.txt", "v1\n")
	gitRun(t, dir, "checkout", "master")
	gitRun(t, dir, "merge", "--no-ff", "feature", "-m", "merge feature")

	calls := 0
	restore := installFetchSeam(t, func(_ context.Context, _ string, _ map[string]string) (map[string][]gitutil.MergeCandidate, int, error) {
		calls++
		return nil, 0, nil
	})
	defer restore()

	results := ClassifyBatch(context.Background(), []Candidate{
		{ID: "t1", RepoDir: dir, Branch: "feature", DefaultRef: "master"},
	})
	testutil.Equal(t, calls, 0)
	testutil.True(t, results["t1"].Safe)
	testutil.Equal(t, results["t1"].Tier, TierLocalAncestor)
}

func TestClassifyBatch_GroupsTierBCandidatesByRepoInOneCallEach(t *testing.T) {
	seenRepos := map[string]int{}
	seenBranchesByRepo := map[string]map[string]bool{}
	restore := installFetchSeam(t, func(_ context.Context, repo string, branches map[string]string) (map[string][]gitutil.MergeCandidate, int, error) {
		seenRepos[repo]++
		if seenBranchesByRepo[repo] == nil {
			seenBranchesByRepo[repo] = map[string]bool{}
		}
		out := map[string][]gitutil.MergeCandidate{}
		for branch := range branches {
			seenBranchesByRepo[repo][branch] = true
			out[branch] = []gitutil.MergeCandidate{
				{State: "MERGED", BaseRefName: "master", MergedAt: "2026-06-20T00:00:00Z", CreatedAt: "2026-06-20T00:00:00Z", URL: "https://github.com/" + repo + "/pull/1"},
			}
		}
		return out, 1, nil
	})
	defer restore()

	results := ClassifyBatch(context.Background(), []Candidate{
		{ID: "t1", RepoSlug: "drn/argus", Branch: "argus/a", DefaultRef: "master", DefaultShort: "master", TaskCreatedAt: mustParse("2026-06-01T00:00:00Z")},
		{ID: "t2", RepoSlug: "drn/argus", Branch: "argus/b", DefaultRef: "master", DefaultShort: "master", TaskCreatedAt: mustParse("2026-06-01T00:00:00Z")},
		{ID: "t3", RepoSlug: "thanx/sketch", Branch: "sketch/a", DefaultRef: "main", DefaultShort: "main", TaskCreatedAt: mustParse("2026-06-01T00:00:00Z")},
	})

	testutil.Equal(t, seenRepos["drn/argus"], 1)
	testutil.Equal(t, seenRepos["thanx/sketch"], 1)
	testutil.True(t, seenBranchesByRepo["drn/argus"]["argus/a"])
	testutil.True(t, seenBranchesByRepo["drn/argus"]["argus/b"])

	testutil.True(t, results["t1"].Safe)
	testutil.True(t, results["t2"].Safe)
	// t3's fake candidate is hardcoded to baseRefName "master", but t3's
	// DefaultShort is "main" — a deliberate mismatch to prove each repo
	// group's candidates are evaluated against THAT candidate's own
	// DefaultShort, not some other group's.
	testutil.False(t, results["t3"].Safe)
}

func TestClassifyBatch_SharedBranchNameInSameRepoBothResolve(t *testing.T) {
	restore := installFetchSeam(t, func(_ context.Context, _ string, branches map[string]string) (map[string][]gitutil.MergeCandidate, int, error) {
		out := map[string][]gitutil.MergeCandidate{}
		for branch := range branches {
			out[branch] = []gitutil.MergeCandidate{
				{State: "MERGED", BaseRefName: "master", MergedAt: "2026-06-20T00:00:00Z", CreatedAt: "2026-06-20T00:00:00Z", URL: "https://github.com/drn/argus/pull/1"},
			}
		}
		return out, 1, nil
	})
	defer restore()

	results := ClassifyBatch(context.Background(), []Candidate{
		{ID: "t1", RepoSlug: "drn/argus", Branch: "argus/shared", DefaultRef: "master", DefaultShort: "master", TaskCreatedAt: mustParse("2026-06-01T00:00:00Z")},
		{ID: "t2", RepoSlug: "drn/argus", Branch: "argus/shared", DefaultRef: "master", DefaultShort: "master", TaskCreatedAt: mustParse("2026-06-01T00:00:00Z")},
	})
	testutil.True(t, results["t1"].Safe)
	testutil.True(t, results["t2"].Safe)
}

func TestClassifyBatch_UnresolvableRepoSkipsNetworkEntirely(t *testing.T) {
	calls := 0
	restore := installFetchSeam(t, func(_ context.Context, _ string, _ map[string]string) (map[string][]gitutil.MergeCandidate, int, error) {
		calls++
		return nil, 0, nil
	})
	defer restore()

	results := ClassifyBatch(context.Background(), []Candidate{
		{ID: "t1", Branch: "argus/a"}, // no RepoDir, no RepoSlug at all
	})
	testutil.Equal(t, calls, 0)
	testutil.False(t, results["t1"].Safe)
}

func TestClassifyBatch_EmptyInput(t *testing.T) {
	results := ClassifyBatch(context.Background(), nil)
	testutil.Equal(t, len(results), 0)
}
