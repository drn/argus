package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/drn/argus/internal/gitutil"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/uxlog"
)

// initTestUxlog redirects uxlog to a temp file and returns a reader for the log
// contents. Cleanup (which resets the global sink) is registered automatically.
// Mirrors internal/gitutil/pr_test.go:initTestUxlog.
func initTestUxlog(t *testing.T) func() string {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "ux.log")
	if err := uxlog.Init(logPath); err != nil {
		t.Fatalf("uxlog.Init: %v", err)
	}
	t.Cleanup(uxlog.Close)
	return func() string {
		b, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatalf("read log: %v", err)
		}
		return string(b)
	}
}

// Stage 1 (Prove-It) tests for the BATCHED pollPRStatesOnce path. They drive
// the daemon through new seams that do not exist yet:
//
//   - d.prBatchFetch: func(ctx, repo string, branches map[string]string)
//     (map[string]gitutil.PRResult, error) — one call per repo group.
//   - d.prResolveRepo: func(ctx, worktree string) (repo string, ok bool) —
//     worktree-default fallback when a task has no cached pr/url.
//   - d.prAliasCap: int — per-query alias cap (chunking trigger); tests set it
//     small to force chunking without thousands of tasks.
//
// These compile-fail until Stage 3 wires the seams onto *Daemon, proving the gap.

// seedURL stamps a cached pr/url (and optional state) so repo resolution picks
// it up and grouping routes the task to the right repo.
func seedURL(t *testing.T, d *Daemon, id, url string) {
	t.Helper()
	testutil.NoError(t, d.db.SetMetaBatch(id, "pr", map[string]string{"url": url}))
}

// Scenario: One query per repo, not per task.
func TestPollPRBatch_OneCallPerRepoGroup(t *testing.T) {
	d, _ := testDaemon(t)
	seedTask(t, d.db, "a", "argus/a", false)
	seedTask(t, d.db, "b", "argus/b", false)
	seedTask(t, d.db, "c", "feat/c", false)
	// a,b cached on drn/argus; c cached on anutron/gmail-mcp.
	seedURL(t, d, "a", "https://github.com/drn/argus/pull/1")
	seedURL(t, d, "b", "https://github.com/drn/argus/pull/2")
	seedURL(t, d, "c", "https://github.com/anutron/gmail-mcp/pull/3")

	var mu sync.Mutex
	callsPerRepo := map[string]int{}
	d.prBatchFetch = func(_ context.Context, repo string, branches map[string]string) (map[string]gitutil.PRResult, error) {
		mu.Lock()
		callsPerRepo[repo]++
		mu.Unlock()
		out := map[string]gitutil.PRResult{}
		for branch := range branches {
			out[branch] = gitutil.PRResult{State: model.PRApproved, URL: "u"}
		}
		return out, nil
	}
	d.prResolveRepo = func(_ context.Context, _ string) (string, bool) { return "", false }

	d.pollPRStatesOnce(context.Background())

	mu.Lock()
	defer mu.Unlock()
	// Two tasks share drn/argus → ONE call, not two. One call for the other repo.
	testutil.Equal(t, callsPerRepo["drn/argus"], 1)
	testutil.Equal(t, callsPerRepo["anutron/gmail-mcp"], 1)
	testutil.Equal(t, len(callsPerRepo), 2)
}

// Scenario: Falls back to worktree default repo when no cached url.
func TestPollPRBatch_FallsBackToWorktreeDefaultRepo(t *testing.T) {
	d, _ := testDaemon(t)
	seedTask(t, d.db, "a", "argus/a", false) // no cached url

	var seenRepo string
	d.prBatchFetch = func(_ context.Context, repo string, branches map[string]string) (map[string]gitutil.PRResult, error) {
		seenRepo = repo
		out := map[string]gitutil.PRResult{}
		for branch := range branches {
			out[branch] = gitutil.PRResult{State: model.PRNone}
		}
		return out, nil
	}
	d.prResolveRepo = func(_ context.Context, worktree string) (string, bool) {
		return "drn/argus", true
	}

	d.pollPRStatesOnce(context.Background())
	testutil.Equal(t, seenRepo, "drn/argus")
}

// Scenario: Skips tasks with a terminal cached PR state — excluded BEFORE grouping.
func TestPollPRBatch_TerminalExcludedBeforeGrouping(t *testing.T) {
	d, _ := testDaemon(t)
	seedTask(t, d.db, "open", "argus/open", false)
	seedTask(t, d.db, "merged", "argus/merged", false)
	testutil.NoError(t, d.db.SetMetaBatch("merged", "pr", map[string]string{
		"state": model.PRMergedClosed.String(),
		"url":   "https://github.com/drn/argus/pull/9",
	}))
	seedURL(t, d, "open", "https://github.com/drn/argus/pull/8")

	var mu sync.Mutex
	queried := map[string]bool{}
	d.prBatchFetch = func(_ context.Context, _ string, branches map[string]string) (map[string]gitutil.PRResult, error) {
		mu.Lock()
		for branch := range branches {
			queried[branch] = true
		}
		mu.Unlock()
		out := map[string]gitutil.PRResult{}
		for branch := range branches {
			out[branch] = gitutil.PRResult{State: model.PRApproved, URL: "u"}
		}
		return out, nil
	}
	d.prResolveRepo = func(_ context.Context, _ string) (string, bool) { return "", false }

	d.pollPRStatesOnce(context.Background())

	mu.Lock()
	defer mu.Unlock()
	testutil.Equal(t, queried["argus/open"], true)
	testutil.Equal(t, queried["argus/merged"], false) // terminal never queried

	// Terminal task's cached value is untouched.
	meta, err := d.db.ListMeta("merged", "pr")
	testutil.NoError(t, err)
	got := map[string]string{}
	for _, e := range meta {
		got[e.Key] = e.Value
	}
	testutil.Equal(t, got["state"], "merged-closed")
}

// Scenario: Preserves cache on transient failure — a group-query error leaves
// every cached value in that group unchanged.
func TestPollPRBatch_KeepStaleOnGroupError(t *testing.T) {
	d, _ := testDaemon(t)
	seedTask(t, d.db, "a", "argus/a", false)
	seedTask(t, d.db, "b", "argus/b", false)
	// Prior good values on both.
	testutil.NoError(t, d.db.SetMetaBatch("a", "pr", map[string]string{
		"state": "approved", "url": "https://github.com/drn/argus/pull/1",
	}))
	testutil.NoError(t, d.db.SetMetaBatch("b", "pr", map[string]string{
		"state": "changes-requested", "url": "https://github.com/drn/argus/pull/2",
	}))

	d.prBatchFetch = func(_ context.Context, _ string, _ map[string]string) (map[string]gitutil.PRResult, error) {
		return nil, errors.New("network timeout")
	}
	d.prResolveRepo = func(_ context.Context, _ string) (string, bool) { return "", false }

	d.pollPRStatesOnce(context.Background())

	for id, wantState := range map[string]string{"a": "approved", "b": "changes-requested"} {
		meta, err := d.db.ListMeta(id, "pr")
		testutil.NoError(t, err)
		got := map[string]string{}
		for _, e := range meta {
			got[e.Key] = e.Value
		}
		testutil.Equal(t, got["state"], wantState)
	}
}

// Scenario: Persists successful result — including writing `none`.
func TestPollPRBatch_WritesResultsInclNone(t *testing.T) {
	d, _ := testDaemon(t)
	seedTask(t, d.db, "a", "argus/a", false)
	seedTask(t, d.db, "b", "argus/b", false)
	seedURL(t, d, "a", "https://github.com/drn/argus/pull/1")
	seedURL(t, d, "b", "https://github.com/drn/argus/pull/2")

	d.prBatchFetch = func(_ context.Context, _ string, branches map[string]string) (map[string]gitutil.PRResult, error) {
		out := map[string]gitutil.PRResult{}
		for branch := range branches {
			switch branch {
			case "argus/a":
				out[branch] = gitutil.PRResult{State: model.PRChangesRequested, URL: "https://github.com/drn/argus/pull/1"}
			case "argus/b":
				out[branch] = gitutil.PRResult{State: model.PRNone} // authoritative none
			}
		}
		return out, nil
	}
	d.prResolveRepo = func(_ context.Context, _ string) (string, bool) { return "", false }

	d.pollPRStatesOnce(context.Background())

	metaA, err := d.db.ListMeta("a", "pr")
	testutil.NoError(t, err)
	gotA := map[string]string{}
	for _, e := range metaA {
		gotA[e.Key] = e.Value
	}
	testutil.Equal(t, gotA["state"], "changes-requested")
	testutil.Equal(t, gotA["url"], "https://github.com/drn/argus/pull/1")

	metaB, err := d.db.ListMeta("b", "pr")
	testutil.NoError(t, err)
	gotB := map[string]string{}
	for _, e := range metaB {
		gotB[e.Key] = e.Value
	}
	testutil.Equal(t, gotB["state"], "none")
}

// Scenario: Chunks oversized repo groups — when a single repo group exceeds the
// alias cap, the poller issues multiple sequential queries each within the cap.
func TestPollPRBatch_ChunksOversizedGroup(t *testing.T) {
	d, _ := testDaemon(t)
	const n = 5
	for i := 0; i < n; i++ {
		id := "t" + string(rune('a'+i))
		seedTask(t, d.db, id, "argus/"+id, false)
		seedURL(t, d, id, "https://github.com/drn/argus/pull/"+string(rune('1'+i)))
	}
	d.prAliasCap = 2 // 5 branches / cap 2 → 3 chunks

	var mu sync.Mutex
	var calls int
	var maxBranchesPerCall int
	d.prBatchFetch = func(_ context.Context, _ string, branches map[string]string) (map[string]gitutil.PRResult, error) {
		mu.Lock()
		calls++
		if len(branches) > maxBranchesPerCall {
			maxBranchesPerCall = len(branches)
		}
		mu.Unlock()
		out := map[string]gitutil.PRResult{}
		for branch := range branches {
			out[branch] = gitutil.PRResult{State: model.PRApproved, URL: "u"}
		}
		return out, nil
	}
	d.prResolveRepo = func(_ context.Context, _ string) (string, bool) { return "", false }

	d.pollPRStatesOnce(context.Background())

	mu.Lock()
	defer mu.Unlock()
	// 5 branches at cap 2 → 3 sequential queries, none exceeding the cap.
	testutil.Equal(t, calls, 3)
	if maxBranchesPerCall > d.prAliasCap {
		t.Fatalf("a chunk had %d branches, exceeding cap %d", maxBranchesPerCall, d.prAliasCap)
	}
}

// Scenario: Logs per-cycle poll summary.
//
// WHEN a poll cycle completes THEN the system emits a single
//
//	[pr] poll: eligible=… skipped=… written=… errored=…
//
// uxlog summary line with counts that reflect the cycle. The cycle mixes a
// terminal-cached task (skipped), two tasks whose repo group fetches cleanly
// (written), and one task whose repo group errors (errored, cache preserved):
//
//	eligible=3 skipped=1 written=2 errored=1
func TestPollPRBatch_LogsPerCycleSummary(t *testing.T) {
	readLog := initTestUxlog(t)
	d, _ := testDaemon(t)

	// Terminal task → excluded from the eligible set → counted as skipped.
	seedTask(t, d.db, "merged", "argus/merged", false)
	testutil.NoError(t, d.db.SetMetaBatch("merged", "pr", map[string]string{
		"state": model.PRMergedClosed.String(),
		"url":   "https://github.com/drn/argus/pull/9",
	}))

	// Two eligible tasks on drn/argus → group resolves cleanly → both written.
	seedTask(t, d.db, "a", "argus/a", false)
	seedTask(t, d.db, "b", "argus/b", false)
	seedURL(t, d, "a", "https://github.com/drn/argus/pull/1")
	seedURL(t, d, "b", "https://github.com/drn/argus/pull/2")

	// One eligible task on a repo whose group fetch errors → counted as errored.
	seedTask(t, d.db, "c", "feat/c", false)
	seedURL(t, d, "c", "https://github.com/anutron/gmail-mcp/pull/3")

	d.prBatchFetch = func(_ context.Context, repo string, branches map[string]string) (map[string]gitutil.PRResult, error) {
		if repo == "anutron/gmail-mcp" {
			return nil, errors.New("network timeout")
		}
		out := map[string]gitutil.PRResult{}
		for branch := range branches {
			out[branch] = gitutil.PRResult{State: model.PRApproved, URL: "u"}
		}
		return out, nil
	}
	d.prResolveRepo = func(_ context.Context, _ string) (string, bool) { return "", false }

	d.pollPRStatesOnce(context.Background())

	testutil.Contains(t, readLog(), "[pr] poll: eligible=3 skipped=1 written=2 errored=1")
}
