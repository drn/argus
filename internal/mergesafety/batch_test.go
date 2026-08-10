package mergesafety

import (
	"context"
	"sync"
	"testing"
	"time"

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

// --- ClassifyBatchFunc: the incremental form add-cleanup-popup-fixes added
// so a caller with a large, mostly-Tier-B candidate set can cache/observe
// each verdict as it lands instead of waiting for the whole batch. ---

// TestClassifyBatchFunc_TierAResultsDeliveredBeforeAnyTierBCall proves the
// ordering guarantee callers rely on for real progress: every Tier-A (local
// git, no network) candidate's onResult fires before ClassifyBatchFunc ever
// issues the Tier B network call for a repo group in the same batch — a
// caller watching for incremental cache writes sees the fast candidates
// resolve immediately, not held hostage behind a slow/blocked network call.
func TestClassifyBatchFunc_TierAResultsDeliveredBeforeAnyTierBCall(t *testing.T) {
	dir := initRepo(t, t.TempDir())
	gitRun(t, dir, "checkout", "-b", "feature")
	addCommit(t, dir, "f.txt", "v1\n")
	gitRun(t, dir, "checkout", "master")
	gitRun(t, dir, "merge", "--no-ff", "feature", "-m", "merge feature")

	var order []string
	restore := installFetchSeam(t, func(_ context.Context, _ string, branches map[string]string) (map[string][]gitutil.MergeCandidate, int, error) {
		order = append(order, "tier-b-call")
		return nil, 0, nil
	})
	defer restore()

	ClassifyBatchFunc(context.Background(), []Candidate{
		{ID: "local", RepoDir: dir, Branch: "feature", DefaultRef: "master"},
		{ID: "network", RepoSlug: "drn/argus", Branch: "argus/a", DefaultRef: "master", DefaultShort: "master", TaskCreatedAt: mustParse("2026-06-01T00:00:00Z")},
	}, func(id string, v Verdict) {
		order = append(order, "result:"+id)
	})

	testutil.Equal(t, len(order), 3)
	testutil.Equal(t, order[0], "result:local") // Tier A delivered first, before the Tier B call is even issued
	testutil.Equal(t, order[1], "tier-b-call")
	testutil.Equal(t, order[2], "result:network")
}

// TestClassifyBatchFunc_EachRepoGroupDeliveredIndependently proves the
// per-repo-group incremental delivery: results for one repo's group land as
// soon as THAT repo's call returns, without waiting for a second repo's
// (still in-flight) call — the granularity a caller needs to cache real
// progress across a multi-repo backlog instead of one all-or-nothing wait.
func TestClassifyBatchFunc_EachRepoGroupDeliveredIndependently(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	var seen []string
	appendSeen := func(id string) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, id)
	}
	snapshotSeen := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}

	restore := installFetchSeam(t, func(_ context.Context, repo string, branches map[string]string) (map[string][]gitutil.MergeCandidate, int, error) {
		if repo == "thanx/sketch" {
			<-release // second repo's call blocks until the test releases it
		}
		out := map[string][]gitutil.MergeCandidate{}
		for branch := range branches {
			out[branch] = []gitutil.MergeCandidate{
				{State: "MERGED", BaseRefName: "master", CreatedAt: "2026-06-20T00:00:00Z", URL: "https://example/pr/1"},
			}
		}
		return out, 1, nil
	})
	defer restore()

	done := make(chan struct{})
	go func() {
		ClassifyBatchFunc(context.Background(), []Candidate{
			{ID: "t1", RepoSlug: "drn/argus", Branch: "argus/a", DefaultRef: "master", DefaultShort: "master", TaskCreatedAt: mustParse("2026-06-01T00:00:00Z")},
			{ID: "t2", RepoSlug: "thanx/sketch", Branch: "sketch/a", DefaultRef: "main", DefaultShort: "main", TaskCreatedAt: mustParse("2026-06-01T00:00:00Z")},
		}, func(id string, v Verdict) {
			appendSeen(id)
		})
		close(done)
	}()

	// t1's repo group (drn/argus) has no artificial block, so it must resolve
	// and be delivered while t2's group is still stuck on release.
	deadline := time.Now().Add(2 * time.Second)
	for len(snapshotSeen()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the unblocked repo group's result")
		}
		time.Sleep(5 * time.Millisecond)
	}
	first := snapshotSeen()
	testutil.Equal(t, first[0], "t1")

	select {
	case <-done:
		t.Fatal("ClassifyBatchFunc returned before the blocked repo group's call was released")
	default:
	}

	close(release)
	<-done
	testutil.Equal(t, len(snapshotSeen()), 2)
}

// TestClassifyBatchFunc_MatchesClassifyBatchResults proves ClassifyBatch's
// wrapper behavior is unchanged: collecting ClassifyBatchFunc's callbacks
// into a map produces the exact same results ClassifyBatch itself returns.
func TestClassifyBatchFunc_MatchesClassifyBatchResults(t *testing.T) {
	restore := installFetchSeam(t, func(_ context.Context, _ string, branches map[string]string) (map[string][]gitutil.MergeCandidate, int, error) {
		out := map[string][]gitutil.MergeCandidate{}
		for branch := range branches {
			out[branch] = []gitutil.MergeCandidate{
				{State: "MERGED", BaseRefName: "master", CreatedAt: "2026-06-20T00:00:00Z", URL: "https://example/pr/1"},
			}
		}
		return out, 1, nil
	})
	defer restore()

	cands := []Candidate{
		{ID: "t1", RepoSlug: "drn/argus", Branch: "argus/a", DefaultRef: "master", DefaultShort: "master", TaskCreatedAt: mustParse("2026-06-01T00:00:00Z")},
	}
	want := ClassifyBatch(context.Background(), cands)

	got := map[string]Verdict{}
	ClassifyBatchFunc(context.Background(), cands, func(id string, v Verdict) { got[id] = v })

	testutil.DeepEqual(t, got, want)
}
