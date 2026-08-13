package gitutil

import (
	"context"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// graphQLMergeCandidates is the JSON GitHub returns for an aliased
// merge-candidate batch query (first: 5, ordered by createdAt desc). Covers,
// in one query:
//   - t1: a single merged PR into "master"
//   - t2: NO PR (empty nodes)
//   - t3: two candidates — a merged one into "master" and a merged one into
//     "some-other-branch" (only the master one should count as plausible)
const graphQLMergeCandidates = `{
  "data": {
    "rateLimit": {"cost": 1, "remaining": 4999},
    "repo": {
      "t1": {"nodes": [{"state": "MERGED", "baseRefName": "master", "mergedAt": "2026-06-16T07:42:20Z", "createdAt": "2026-06-16T07:00:00Z", "url": "https://github.com/drn/argus/pull/743"}]},
      "t2": {"nodes": []},
      "t3": {"nodes": [
        {"state": "MERGED", "baseRefName": "master", "mergedAt": "2026-07-01T00:00:00Z", "createdAt": "2026-06-30T00:00:00Z", "url": "https://github.com/drn/argus/pull/800"},
        {"state": "MERGED", "baseRefName": "some-other-branch", "mergedAt": "2026-06-01T00:00:00Z", "createdAt": "2026-05-30T00:00:00Z", "url": "https://github.com/drn/argus/pull/500"}
      ]}
    }
  }
}`

func TestFetchMergeCandidatesBatch_ParsesPerBranchResults(t *testing.T) {
	fake := &fakeGraphQLRunner{out: graphQLMergeCandidates, code: 0}
	installGraphQLRunner(t, fake.run)

	branches := map[string]string{
		"argus/one":  "t1",
		"argus/none": "t2",
		"argus/two":  "t3",
	}

	res, cost, err := FetchMergeCandidatesBatch(context.Background(), "drn/argus", branches)
	testutil.NoError(t, err)
	testutil.Equal(t, fake.calls, 1)
	testutil.Equal(t, cost, 1)

	testutil.Equal(t, len(res["argus/one"]), 1)
	testutil.Equal(t, res["argus/one"][0].State, "MERGED")
	testutil.Equal(t, res["argus/one"][0].BaseRefName, "master")
	testutil.Equal(t, res["argus/one"][0].URL, "https://github.com/drn/argus/pull/743")

	testutil.Equal(t, len(res["argus/none"]), 0)

	testutil.Equal(t, len(res["argus/two"]), 2)
}

func TestFetchMergeCandidatesBatch_OneQueryForNBranches(t *testing.T) {
	fake := &fakeGraphQLRunner{out: graphQLMergeCandidates, code: 0}
	installGraphQLRunner(t, fake.run)

	branches := map[string]string{"argus/one": "t1", "argus/none": "t2", "argus/two": "t3"}
	_, _, err := FetchMergeCandidatesBatch(context.Background(), "drn/argus", branches)
	testutil.NoError(t, err)
	testutil.Equal(t, fake.calls, 1)
}

func TestFetchMergeCandidatesBatch_QueryUsesFirst5OrderedByCreatedAtDesc(t *testing.T) {
	fake := &fakeGraphQLRunner{out: graphQLMergeCandidates, code: 0}
	installGraphQLRunner(t, fake.run)

	_, _, err := FetchMergeCandidatesBatch(context.Background(), "drn/argus", map[string]string{"argus/one": "t1"})
	testutil.NoError(t, err)
	testutil.Equal(t, len(fake.args), 1)
	// The query is written to a temp file (`-F query=@<path>`), so assert the
	// runner was invoked with that flag shape rather than inspecting file
	// contents here — the query-building shape itself is covered by
	// TestBuildMergeCandidateQuery below.
	found := false
	for _, a := range fake.args[0] {
		if a == "graphql" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected gh api graphql invocation, got args %v", fake.args[0])
	}
}

func TestBuildMergeCandidateQuery_Shape(t *testing.T) {
	q := buildMergeCandidateQuery("drn", "argus", map[string]string{"argus/a": "t1"})
	testutil.Contains(t, q, `repository(owner: "drn", name: "argus")`)
	testutil.Contains(t, q, `t1: pullRequests(headRefName: "argus/a", first: 5, orderBy: {field: CREATED_AT, direction: DESC})`)
	testutil.Contains(t, q, "state baseRefName mergedAt createdAt url")
}

func TestFetchMergeCandidatesBatch_QueryErrorReturnsError(t *testing.T) {
	fake := &fakeGraphQLRunner{out: "", code: 1, err: nil}
	installGraphQLRunner(t, fake.run)

	_, _, err := FetchMergeCandidatesBatch(context.Background(), "drn/argus", map[string]string{"argus/a": "t1"})
	testutil.Error(t, err)
}

func TestFetchMergeCandidatesBatch_InvalidRepoNoQuery(t *testing.T) {
	fake := &fakeGraphQLRunner{out: graphQLMergeCandidates, code: 0}
	installGraphQLRunner(t, fake.run)

	_, _, err := FetchMergeCandidatesBatch(context.Background(), "not-a-repo", map[string]string{"argus/a": "t1"})
	testutil.Error(t, err)
	testutil.Equal(t, fake.calls, 0)
}

func TestFetchMergeCandidatesBatch_EmptyBranchesNoQuery(t *testing.T) {
	fake := &fakeGraphQLRunner{out: graphQLMergeCandidates, code: 0}
	installGraphQLRunner(t, fake.run)

	res, cost, err := FetchMergeCandidatesBatch(context.Background(), "drn/argus", map[string]string{})
	testutil.NoError(t, err)
	testutil.Equal(t, fake.calls, 0)
	testutil.Equal(t, len(res), 0)
	testutil.Equal(t, cost, 0)
}

func TestFetchMergeCandidatesBatch_BadJSONReturnsError(t *testing.T) {
	fake := &fakeGraphQLRunner{out: "{not json", code: 0}
	installGraphQLRunner(t, fake.run)

	_, _, err := FetchMergeCandidatesBatch(context.Background(), "drn/argus", map[string]string{"argus/a": "t1"})
	testutil.Error(t, err)
}

func TestFetchMergeCandidatesBatch_UnknownAliasIgnored(t *testing.T) {
	const extraAlias = `{
  "data": {
    "rateLimit": {"cost": 2, "remaining": 4998},
    "repo": {
      "t1": {"nodes": [{"state": "MERGED", "baseRefName": "master", "mergedAt": "2026-06-16T07:42:20Z", "createdAt": "2026-06-16T07:00:00Z", "url": "https://github.com/drn/argus/pull/743"}]},
      "t99": {"nodes": []}
    }
  }
}`
	fake := &fakeGraphQLRunner{out: extraAlias, code: 0}
	installGraphQLRunner(t, fake.run)

	res, cost, err := FetchMergeCandidatesBatch(context.Background(), "drn/argus", map[string]string{"argus/a": "t1"})
	testutil.NoError(t, err)
	testutil.Equal(t, cost, 2)
	testutil.Equal(t, len(res), 1)
}

// Existing PR-badge batch behavior must be byte-for-byte unchanged by the
// shared-executor extraction — this re-runs the original acceptance case.
func TestFetchPRStatesBatch_StillWorksAfterSharedExecutorExtraction(t *testing.T) {
	fake := &fakeGraphQLRunner{out: graphQLSuccess, code: 0}
	installGraphQLRunner(t, fake.run)

	res, cost, err := FetchPRStatesBatch(context.Background(), "drn/argus", map[string]string{
		"argus/open": "t1", "argus/none": "t2", "argus/deleted": "t3",
	})
	testutil.NoError(t, err)
	testutil.Equal(t, fake.calls, 1)
	testutil.Equal(t, cost, 1)
	testutil.Equal(t, res["argus/open"].URL, "https://github.com/drn/argus/pull/11")
}
