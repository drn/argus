package mergesafety

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/drn/argus/internal/gitutil"
	"github.com/drn/argus/internal/testutil"
)

// --- local git fixtures (mirrors internal/gitutil's test helpers) ---

func initRepo(t *testing.T, dir string) string {
	t.Helper()
	if out, err := exec.Command("git", "init", "-b", "master", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	gitRun(t, dir, "config", "user.email", "test@example.com")
	gitRun(t, dir, "config", "user.name", "Test")
	gitRun(t, dir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "init")
	return dir
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func addCommit(t *testing.T, dir, file, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", file)
	gitRun(t, dir, "commit", "-m", "add "+file)
}

// --- Tier A: branch merged into default ---

func TestClassify_TierA_BranchIsAncestorOfDefault(t *testing.T) {
	dir := initRepo(t, t.TempDir())

	// Simulate: "feature" was merged into master (master now contains
	// feature's commit as an ancestor).
	gitRun(t, dir, "checkout", "-b", "feature")
	addCommit(t, dir, "f.txt", "v1\n")
	gitRun(t, dir, "checkout", "master")
	gitRun(t, dir, "merge", "--no-ff", "feature", "-m", "merge feature")

	v, err := Classify(context.Background(), Params{
		RepoDir:    dir,
		Branch:     "feature",
		DefaultRef: "master",
	})
	testutil.NoError(t, err)
	testutil.True(t, v.Safe)
	testutil.Equal(t, v.Tier, TierLocalAncestor)
}

func TestClassify_TierA_BranchNotAncestor_FallsThroughToTierB(t *testing.T) {
	dir := initRepo(t, t.TempDir())
	gitRun(t, dir, "checkout", "-b", "feature")
	addCommit(t, dir, "f.txt", "v1\n")
	gitRun(t, dir, "checkout", "master")

	// No RepoSlug supplied -> Tier B is unreachable -> not confirmed.
	v, err := Classify(context.Background(), Params{
		RepoDir:    dir,
		Branch:     "feature",
		DefaultRef: "master",
	})
	testutil.NoError(t, err)
	testutil.False(t, v.Safe)
}

func TestClassify_UnresolvableRepo(t *testing.T) {
	v, err := Classify(context.Background(), Params{
		RepoDir:    "/nonexistent/path/definitely",
		Branch:     "feature",
		DefaultRef: "master",
	})
	testutil.NoError(t, err)
	testutil.False(t, v.Safe)
	testutil.Contains(t, v.Reason, "repo")
}

// --- Tier B (via the classifier's own network seam) ---

func TestClassify_TierB_ConfirmedMerged(t *testing.T) {
	restore := installFetchSeam(t, func(_ context.Context, repo string, branches map[string]string) (map[string][]gitutil.MergeCandidate, int, error) {
		testutil.Equal(t, repo, "drn/argus")
		return map[string][]gitutil.MergeCandidate{
			"argus/gone": {
				{State: "MERGED", BaseRefName: "master", MergedAt: "2026-06-16T07:42:20Z", CreatedAt: "2026-06-16T07:00:00Z", URL: "https://github.com/drn/argus/pull/743"},
			},
		}, 1, nil
	})
	defer restore()

	v, err := Classify(context.Background(), Params{
		RepoDir:       "/nonexistent",
		RepoSlug:      "drn/argus",
		Branch:        "argus/gone",
		DefaultRef:    "master",
		DefaultShort:  "master",
		TaskCreatedAt: mustParse("2026-06-15T00:00:00Z"),
	})
	testutil.NoError(t, err)
	testutil.True(t, v.Safe)
	testutil.Equal(t, v.Tier, TierMergedPR)
	testutil.Contains(t, v.Reason, "743")
}

func TestClassify_TierB_RejectsCandidatePredatingTask(t *testing.T) {
	restore := installFetchSeam(t, func(_ context.Context, _ string, _ map[string]string) (map[string][]gitutil.MergeCandidate, int, error) {
		return map[string][]gitutil.MergeCandidate{
			"argus/reused": {
				{State: "MERGED", BaseRefName: "master", MergedAt: "2026-01-01T00:00:00Z", CreatedAt: "2026-01-01T00:00:00Z", URL: "https://github.com/drn/argus/pull/1"},
			},
		}, 1, nil
	})
	defer restore()

	v, err := Classify(context.Background(), Params{
		RepoDir:       "/nonexistent",
		RepoSlug:      "drn/argus",
		Branch:        "argus/reused",
		DefaultRef:    "master",
		DefaultShort:  "master",
		TaskCreatedAt: mustParse("2026-06-01T00:00:00Z"), // task created AFTER the candidate PR
	})
	testutil.NoError(t, err)
	testutil.False(t, v.Safe)
}

func TestClassify_TierB_RejectsWrongBaseRef(t *testing.T) {
	restore := installFetchSeam(t, func(_ context.Context, _ string, _ map[string]string) (map[string][]gitutil.MergeCandidate, int, error) {
		return map[string][]gitutil.MergeCandidate{
			"argus/x": {
				{State: "MERGED", BaseRefName: "some-other-branch", MergedAt: "2026-06-20T00:00:00Z", CreatedAt: "2026-06-20T00:00:00Z", URL: "https://github.com/drn/argus/pull/5"},
			},
		}, 1, nil
	})
	defer restore()

	v, err := Classify(context.Background(), Params{
		RepoDir:       "/nonexistent",
		RepoSlug:      "drn/argus",
		Branch:        "argus/x",
		DefaultRef:    "master",
		DefaultShort:  "master",
		TaskCreatedAt: mustParse("2026-06-01T00:00:00Z"),
	})
	testutil.NoError(t, err)
	testutil.False(t, v.Safe)
}

func TestClassify_TierB_AmbiguousMultipleMatches(t *testing.T) {
	restore := installFetchSeam(t, func(_ context.Context, _ string, _ map[string]string) (map[string][]gitutil.MergeCandidate, int, error) {
		return map[string][]gitutil.MergeCandidate{
			"argus/dup": {
				{State: "MERGED", BaseRefName: "master", MergedAt: "2026-06-20T00:00:00Z", CreatedAt: "2026-06-20T00:00:00Z", URL: "https://github.com/drn/argus/pull/5"},
				{State: "MERGED", BaseRefName: "master", MergedAt: "2026-07-20T00:00:00Z", CreatedAt: "2026-07-20T00:00:00Z", URL: "https://github.com/drn/argus/pull/9"},
			},
		}, 1, nil
	})
	defer restore()

	v, err := Classify(context.Background(), Params{
		RepoDir:       "/nonexistent",
		RepoSlug:      "drn/argus",
		Branch:        "argus/dup",
		DefaultRef:    "master",
		DefaultShort:  "master",
		TaskCreatedAt: mustParse("2026-06-01T00:00:00Z"),
	})
	testutil.NoError(t, err)
	testutil.False(t, v.Safe)
	testutil.Contains(t, v.Reason, "ambiguous")
}

func TestClassify_TierB_NoCandidates(t *testing.T) {
	restore := installFetchSeam(t, func(_ context.Context, _ string, _ map[string]string) (map[string][]gitutil.MergeCandidate, int, error) {
		return map[string][]gitutil.MergeCandidate{}, 0, nil
	})
	defer restore()

	v, err := Classify(context.Background(), Params{
		RepoDir:       "/nonexistent",
		RepoSlug:      "drn/argus",
		Branch:        "argus/gone-forever",
		DefaultRef:    "master",
		DefaultShort:  "master",
		TaskCreatedAt: mustParse("2026-06-01T00:00:00Z"),
	})
	testutil.NoError(t, err)
	testutil.False(t, v.Safe)
}

func TestClassify_TierB_FetchErrorIsNotConfirmed(t *testing.T) {
	restore := installFetchSeam(t, func(_ context.Context, _ string, _ map[string]string) (map[string][]gitutil.MergeCandidate, int, error) {
		return nil, 0, errBoom
	})
	defer restore()

	v, err := Classify(context.Background(), Params{
		RepoDir:       "/nonexistent",
		RepoSlug:      "drn/argus",
		Branch:        "argus/x",
		DefaultRef:    "master",
		DefaultShort:  "master",
		TaskCreatedAt: mustParse("2026-06-01T00:00:00Z"),
	})
	testutil.NoError(t, err) // classifier never surfaces a Go error for "couldn't confirm"
	testutil.False(t, v.Safe)
}

func mustParse(s string) time.Time {
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return tm
}

var errBoom = fmt.Errorf("boom: simulated transport failure")

// installFetchSeam swaps the package-level Tier B seam for the duration of a
// test, restoring the original on cleanup. Mirrors gitutil's own
// installGraphQLRunner-style seam pattern.
func installFetchSeam(t *testing.T, fn func(ctx context.Context, repo string, branches map[string]string) (map[string][]gitutil.MergeCandidate, int, error)) func() {
	t.Helper()
	orig := fetchMergeCandidates
	fetchMergeCandidates = fn
	return func() { fetchMergeCandidates = orig }
}
