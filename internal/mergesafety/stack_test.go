package mergesafety

import (
	"context"
	"testing"

	"github.com/drn/argus/internal/gitutil"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// --- ResolveStackTip: pure, in-memory, no git ---

func TestResolveStackTip(t *testing.T) {
	t.Run("no descendant returns self", func(t *testing.T) {
		x := &model.Task{ID: "x", Branch: "x"}
		tip, ok := ResolveStackTip(x, map[string]*model.Task{})
		testutil.True(t, ok)
		testutil.Equal(t, tip, x)
	})

	t.Run("linear chain resolves to terminal task", func(t *testing.T) {
		x := &model.Task{ID: "x", Branch: "x"}
		y := &model.Task{ID: "y", Branch: "y", BaseBranch: "x"}
		z := &model.Task{ID: "z", Branch: "z", BaseBranch: "y"}
		byBaseBranch := map[string]*model.Task{"x": y, "y": z}

		tip, ok := ResolveStackTip(x, byBaseBranch)
		testutil.True(t, ok)
		testutil.Equal(t, tip, z)
	})

	t.Run("cycle guard returns not-ok instead of looping forever", func(t *testing.T) {
		a := &model.Task{ID: "a", Branch: "a", BaseBranch: "b"}
		b := &model.Task{ID: "b", Branch: "b", BaseBranch: "a"}
		byBaseBranch := map[string]*model.Task{"a": b, "b": a}

		tip, ok := ResolveStackTip(a, byBaseBranch)
		testutil.False(t, ok)
		testutil.Nil(t, tip)
	})
}

// --- ClassifyStackInferred: git-fixture scenarios (Tier D) ---

func TestClassifyStackInferred_CleanLinearStackRescued(t *testing.T) {
	dir := initRepo(t, t.TempDir())

	gitRun(t, dir, "checkout", "-b", "x")
	addCommit(t, dir, "x.txt", "x1\n")
	gitRun(t, dir, "checkout", "-b", "y")
	addCommit(t, dir, "y.txt", "y1\n")
	gitRun(t, dir, "checkout", "-b", "z")
	addCommit(t, dir, "z.txt", "z1\n")
	gitRun(t, dir, "checkout", "master")
	gitRun(t, dir, "merge", "--no-ff", "z", "-m", "merge z")

	taskX := &model.Task{ID: "x", Branch: "x", CreatedAt: mustParse("2026-01-01T00:00:00Z")}
	taskY := &model.Task{ID: "y", Branch: "y", BaseBranch: "x", CreatedAt: mustParse("2026-01-02T00:00:00Z")}
	taskZ := &model.Task{ID: "z", Name: "z-task", Branch: "z", BaseBranch: "y", CreatedAt: mustParse("2026-01-03T00:00:00Z")}

	v, err := ClassifyStackInferred(context.Background(), StackParams{
		Task:         taskX,
		ByBaseBranch: map[string]*model.Task{"x": taskY, "y": taskZ},
		RepoDir:      dir,
		DefaultRef:   "master",
	}, nil)
	testutil.NoError(t, err)
	testutil.True(t, v.Safe)
	testutil.Equal(t, v.Tier, TierStackInferred)
	testutil.Contains(t, v.Reason, "z-task")
	testutil.Contains(t, v.Reason, TierLocalAncestor)
}

func TestClassifyStackInferred_SquashMergedTipRescuedViaTierB(t *testing.T) {
	dir := initRepo(t, t.TempDir())

	gitRun(t, dir, "checkout", "-b", "x")
	addCommit(t, dir, "x.txt", "x1\n")
	gitRun(t, dir, "checkout", "-b", "y")
	addCommit(t, dir, "y.txt", "y1\n")
	gitRun(t, dir, "checkout", "-b", "z")
	addCommit(t, dir, "z.txt", "z1\n")
	gitRun(t, dir, "checkout", "master") // z never locally merged - simulates a squash merge severing Tier A

	restore := installFetchSeam(t, func(_ context.Context, repo string, branches map[string]string) (map[string][]gitutil.MergeCandidate, int, error) {
		testutil.Equal(t, repo, "drn/argus")
		return map[string][]gitutil.MergeCandidate{
			"z": {
				{State: "MERGED", BaseRefName: "master", CreatedAt: "2026-01-03T00:00:00Z", URL: "https://github.com/drn/argus/pull/900"},
			},
		}, 1, nil
	})
	defer restore()

	taskX := &model.Task{ID: "x", Branch: "x", CreatedAt: mustParse("2026-01-01T00:00:00Z")}
	taskY := &model.Task{ID: "y", Branch: "y", BaseBranch: "x", CreatedAt: mustParse("2026-01-02T00:00:00Z")}
	taskZ := &model.Task{ID: "z", Name: "z-task", Branch: "z", BaseBranch: "y", CreatedAt: mustParse("2026-01-03T00:00:00Z")}

	v, err := ClassifyStackInferred(context.Background(), StackParams{
		Task:         taskX,
		ByBaseBranch: map[string]*model.Task{"x": taskY, "y": taskZ},
		RepoDir:      dir,
		RepoSlug:     "drn/argus",
		DefaultRef:   "master",
		DefaultShort: "master",
	}, nil)
	testutil.NoError(t, err)
	testutil.True(t, v.Safe)
	testutil.Equal(t, v.Tier, TierStackInferred)
	testutil.Contains(t, v.Reason, TierMergedPR)
}

func TestClassifyStackInferred_BrokenAncestryMidStackNotRescued(t *testing.T) {
	dir := initRepo(t, t.TempDir())

	gitRun(t, dir, "checkout", "-b", "x")
	addCommit(t, dir, "x.txt", "x1\n")
	gitRun(t, dir, "checkout", "-b", "y")
	addCommit(t, dir, "y.txt", "y1\n")

	// z is branched fresh from master and gets y's change cherry-picked on
	// top, instead of branching from y — simulates a rebase/cherry-pick
	// that severs x's real git ancestry even though the task metadata
	// still claims z.BaseBranch == y.
	gitRun(t, dir, "checkout", "master")
	gitRun(t, dir, "checkout", "-b", "z")
	gitRun(t, dir, "cherry-pick", "y")

	gitRun(t, dir, "checkout", "master")
	gitRun(t, dir, "merge", "--no-ff", "z", "-m", "merge z")

	taskX := &model.Task{ID: "x", Branch: "x", CreatedAt: mustParse("2026-01-01T00:00:00Z")}
	taskY := &model.Task{ID: "y", Branch: "y", BaseBranch: "x", CreatedAt: mustParse("2026-01-02T00:00:00Z")}
	taskZ := &model.Task{ID: "z", Branch: "z", BaseBranch: "y", CreatedAt: mustParse("2026-01-03T00:00:00Z")}

	v, err := ClassifyStackInferred(context.Background(), StackParams{
		Task:         taskX,
		ByBaseBranch: map[string]*model.Task{"x": taskY, "y": taskZ},
		RepoDir:      dir,
		DefaultRef:   "master",
	}, nil)
	testutil.NoError(t, err)
	testutil.False(t, v.Safe)
	testutil.Equal(t, v.Tier, "")
}

func TestClassifyStackInferred_UnconfirmedTipNotRescued(t *testing.T) {
	dir := initRepo(t, t.TempDir())

	gitRun(t, dir, "checkout", "-b", "x")
	addCommit(t, dir, "x.txt", "x1\n")
	gitRun(t, dir, "checkout", "-b", "y")
	addCommit(t, dir, "y.txt", "y1\n")
	gitRun(t, dir, "checkout", "-b", "z")
	addCommit(t, dir, "z.txt", "z1\n")
	gitRun(t, dir, "checkout", "master") // z never merged; no RepoSlug so Tier B is unreachable

	taskX := &model.Task{ID: "x", Branch: "x", CreatedAt: mustParse("2026-01-01T00:00:00Z")}
	taskY := &model.Task{ID: "y", Branch: "y", BaseBranch: "x", CreatedAt: mustParse("2026-01-02T00:00:00Z")}
	taskZ := &model.Task{ID: "z", Branch: "z", BaseBranch: "y", CreatedAt: mustParse("2026-01-03T00:00:00Z")}

	v, err := ClassifyStackInferred(context.Background(), StackParams{
		Task:         taskX,
		ByBaseBranch: map[string]*model.Task{"x": taskY, "y": taskZ},
		RepoDir:      dir,
		DefaultRef:   "master",
	}, nil)
	testutil.NoError(t, err)
	testutil.False(t, v.Safe)
}

func TestClassifyStackInferred_CycleGuardFailsClosed(t *testing.T) {
	a := &model.Task{ID: "a", Branch: "a", BaseBranch: "b"}
	b := &model.Task{ID: "b", Branch: "b", BaseBranch: "a"}

	v, err := ClassifyStackInferred(context.Background(), StackParams{
		Task:         a,
		ByBaseBranch: map[string]*model.Task{"a": b, "b": a},
		RepoDir:      "/nonexistent",
		DefaultRef:   "master",
	}, nil)
	testutil.NoError(t, err)
	testutil.False(t, v.Safe)
	testutil.Contains(t, v.Reason, "cycle")
}

func TestClassifyStackInferred_NeverProducedWhenNoDescendant(t *testing.T) {
	x := &model.Task{ID: "x", Branch: "x"}

	v, err := ClassifyStackInferred(context.Background(), StackParams{
		Task:         x,
		ByBaseBranch: map[string]*model.Task{},
		RepoDir:      "/nonexistent",
		DefaultRef:   "master",
	}, nil)
	testutil.NoError(t, err)
	testutil.False(t, v.Safe)
}

func TestClassifyStackInferred_TipMemoizedAcrossCandidates(t *testing.T) {
	dir := initRepo(t, t.TempDir())

	gitRun(t, dir, "checkout", "-b", "x")
	addCommit(t, dir, "x.txt", "x1\n")
	gitRun(t, dir, "checkout", "-b", "y")
	addCommit(t, dir, "y.txt", "y1\n")
	gitRun(t, dir, "checkout", "-b", "z")
	addCommit(t, dir, "z.txt", "z1\n")
	gitRun(t, dir, "checkout", "master") // z never locally merged - forces every classification through Tier B

	calls := 0
	restore := installFetchSeam(t, func(_ context.Context, _ string, _ map[string]string) (map[string][]gitutil.MergeCandidate, int, error) {
		calls++
		return map[string][]gitutil.MergeCandidate{
			"z": {
				{State: "MERGED", BaseRefName: "master", CreatedAt: "2026-01-03T00:00:00Z", URL: "https://github.com/drn/argus/pull/900"},
			},
		}, 1, nil
	})
	defer restore()

	taskX := &model.Task{ID: "x", Branch: "x", CreatedAt: mustParse("2026-01-01T00:00:00Z")}
	taskY := &model.Task{ID: "y", Branch: "y", BaseBranch: "x", CreatedAt: mustParse("2026-01-02T00:00:00Z")}
	taskZ := &model.Task{ID: "z", Branch: "z", BaseBranch: "y", CreatedAt: mustParse("2026-01-03T00:00:00Z")}
	byBaseBranch := map[string]*model.Task{"x": taskY, "y": taskZ}
	params := func(candidate *model.Task) StackParams {
		return StackParams{
			Task: candidate, ByBaseBranch: byBaseBranch,
			RepoDir: dir, RepoSlug: "drn/argus", DefaultRef: "master", DefaultShort: "master",
		}
	}

	shared := map[string]Verdict{}
	v1, err := ClassifyStackInferred(context.Background(), params(taskX), shared)
	testutil.NoError(t, err)
	testutil.True(t, v1.Safe)

	v2, err := ClassifyStackInferred(context.Background(), params(taskY), shared)
	testutil.NoError(t, err)
	testutil.True(t, v2.Safe)

	// Both x and y resolve to the same tip z. Sharing one cache across the
	// two calls must classify that tip exactly once instead of issuing a
	// second Tier B network round trip for the second candidate.
	testutil.Equal(t, calls, 1)
}
