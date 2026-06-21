package daemon

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/gitutil"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// seedTask inserts a task with the given id/branch/archived shape into db.
func seedTask(t *testing.T, database *db.DB, id, branch string, archived bool) {
	t.Helper()
	task := &model.Task{
		ID:       id,
		Name:     id,
		Status:   model.StatusInReview,
		Project:  "proj",
		Branch:   branch,
		Worktree: "/tmp/wt/" + id,
		Archived: archived,
	}
	if err := database.Add(task); err != nil {
		t.Fatalf("add %s: %v", id, err)
	}
}

// resolveAll is a prResolveRepo stub that routes every worktree to the same repo
// so tasks without a cached pr/url still group and get queried. These scenarios
// predate batching (#773); they assert the poller's eligibility/keep-stale/write
// behavior, which is now exercised through the batch seam (prBatchFetch) rather
// than the retired per-task gh path.
func resolveAll(repo string) func(context.Context, string) (string, bool) {
	return func(context.Context, string) (string, bool) { return repo, true }
}

func TestPollPR_SkipsArchivedAndBranchless(t *testing.T) {
	d, _ := testDaemon(t)
	seedTask(t, d.db, "normal", "argus/normal", false)
	seedTask(t, d.db, "archived", "argus/archived", true)
	seedTask(t, d.db, "branchless", "", false)

	var mu sync.Mutex
	fetched := map[string]bool{}
	d.prBatchFetch = func(_ context.Context, _ string, branches map[string]string) (map[string]gitutil.PRResult, error) {
		mu.Lock()
		out := map[string]gitutil.PRResult{}
		for branch := range branches {
			fetched[branch] = true
			out[branch] = gitutil.PRResult{State: model.PRApproved, URL: "https://example/pr/1"}
		}
		mu.Unlock()
		return out, nil
	}
	d.prResolveRepo = resolveAll("drn/argus")

	d.pollPRStatesOnce(context.Background())

	mu.Lock()
	defer mu.Unlock()
	testutil.Equal(t, fetched["argus/normal"], true)
	testutil.Equal(t, len(fetched), 1) // archived + branchless never fetched

	// Only the eligible task got a meta row.
	meta, err := d.db.ListMetaByNamespace("pr")
	testutil.NoError(t, err)
	testutil.Equal(t, len(meta), 1)
	testutil.Equal(t, meta["normal"]["state"], "approved")
	testutil.Equal(t, meta["normal"]["url"], "https://example/pr/1")
}

func TestPollPR_SkipsTerminalCachedState(t *testing.T) {
	d, _ := testDaemon(t)
	seedTask(t, d.db, "open", "argus/open", false)
	seedTask(t, d.db, "merged", "argus/merged", false)

	// "merged" already has a terminal cached state — directly seeded (this also
	// simulates a daemon restart: the value is in task_meta with no prior poll
	// this run). A terminal state must exclude the task from the eligible set.
	testutil.NoError(t, d.db.SetMetaBatch("merged", "pr", map[string]string{
		"state": model.PRMergedClosed.String(),
		"url":   "https://example/pr/merged",
	}))

	var mu sync.Mutex
	fetched := map[string]bool{}
	d.prBatchFetch = func(_ context.Context, _ string, branches map[string]string) (map[string]gitutil.PRResult, error) {
		mu.Lock()
		out := map[string]gitutil.PRResult{}
		for branch := range branches {
			fetched[branch] = true
			out[branch] = gitutil.PRResult{State: model.PRApproved, URL: "https://example/pr/open"}
		}
		mu.Unlock()
		return out, nil
	}
	d.prResolveRepo = resolveAll("drn/argus")

	d.pollPRStatesOnce(context.Background())

	mu.Lock()
	defer mu.Unlock()
	// Only the open task is polled; the terminal-state task is skipped.
	testutil.Equal(t, fetched["argus/open"], true)
	testutil.Equal(t, fetched["argus/merged"], false)
	testutil.Equal(t, len(fetched), 1)

	// The terminal task's cached value is untouched (never re-fetched/written).
	meta, err := d.db.ListMeta("merged", "pr")
	testutil.NoError(t, err)
	got := map[string]string{}
	for _, e := range meta {
		got[e.Key] = e.Value
	}
	testutil.Equal(t, got["state"], "merged-closed")
	testutil.Equal(t, got["url"], "https://example/pr/merged")
}

func TestPollPR_PollsNonTerminalCachedStates(t *testing.T) {
	d, _ := testDaemon(t)
	// Every non-terminal cached state, plus no-cache, must remain eligible.
	seedTask(t, d.db, "approved", "argus/approved", false)
	seedTask(t, d.db, "draft", "argus/draft", false)
	seedTask(t, d.db, "changes", "argus/changes", false)
	seedTask(t, d.db, "awaiting", "argus/awaiting", false)
	seedTask(t, d.db, "noneCached", "argus/none", false)
	seedTask(t, d.db, "unknownCached", "argus/unknown", false)
	seedTask(t, d.db, "nocache", "argus/nocache", false)

	for id, st := range map[string]model.PRState{
		"approved":      model.PRApproved,
		"draft":         model.PRDraft,
		"changes":       model.PRChangesRequested,
		"awaiting":      model.PRAwaitingReview,
		"noneCached":    model.PRNone,
		"unknownCached": model.PRUnknown,
	} {
		testutil.NoError(t, d.db.SetMetaBatch(id, "pr", map[string]string{"state": st.String()}))
	}

	var mu sync.Mutex
	fetched := map[string]bool{}
	d.prBatchFetch = func(_ context.Context, _ string, branches map[string]string) (map[string]gitutil.PRResult, error) {
		mu.Lock()
		out := map[string]gitutil.PRResult{}
		for branch := range branches {
			fetched[branch] = true
			out[branch] = gitutil.PRResult{State: model.PRApproved, URL: "u"}
		}
		mu.Unlock()
		return out, nil
	}
	d.prResolveRepo = resolveAll("drn/argus")

	d.pollPRStatesOnce(context.Background())

	mu.Lock()
	defer mu.Unlock()
	// All seven remain eligible — none is terminal.
	testutil.Equal(t, len(fetched), 7)
}

func TestPollPR_WritesStateAndURL(t *testing.T) {
	d, _ := testDaemon(t)
	seedTask(t, d.db, "t1", "argus/t1", false)

	d.prBatchFetch = func(_ context.Context, _ string, branches map[string]string) (map[string]gitutil.PRResult, error) {
		out := map[string]gitutil.PRResult{}
		for branch := range branches {
			out[branch] = gitutil.PRResult{State: model.PRChangesRequested, URL: "https://example/pr/9"}
		}
		return out, nil
	}
	d.prResolveRepo = resolveAll("drn/argus")
	d.pollPRStatesOnce(context.Background())

	meta, err := d.db.ListMeta("t1", "pr")
	testutil.NoError(t, err)
	got := map[string]string{}
	for _, e := range meta {
		got[e.Key] = e.Value
	}
	testutil.Equal(t, got["state"], "changes-requested")
	testutil.Equal(t, got["url"], "https://example/pr/9")
}

func TestPollPR_KeepsStaleOnError(t *testing.T) {
	d, _ := testDaemon(t)
	seedTask(t, d.db, "t1", "argus/t1", false)

	// Seed a prior good value.
	testutil.NoError(t, d.db.SetMetaBatch("t1", "pr", map[string]string{
		"state": "approved",
		"url":   "https://example/pr/prior",
	}))

	d.prBatchFetch = func(_ context.Context, _ string, _ map[string]string) (map[string]gitutil.PRResult, error) {
		return nil, errors.New("network timeout")
	}
	d.prResolveRepo = resolveAll("drn/argus")
	d.pollPRStatesOnce(context.Background())

	meta, err := d.db.ListMeta("t1", "pr")
	testutil.NoError(t, err)
	got := map[string]string{}
	for _, e := range meta {
		got[e.Key] = e.Value
	}
	// Prior value preserved — transient error must not clobber.
	testutil.Equal(t, got["state"], "approved")
	testutil.Equal(t, got["url"], "https://example/pr/prior")
}

func TestPollPR_PersistsPRNone(t *testing.T) {
	d, _ := testDaemon(t)
	seedTask(t, d.db, "t1", "argus/t1", false)

	// An unambiguous PRNone from a successful query is authoritative and written.
	d.prBatchFetch = func(_ context.Context, _ string, branches map[string]string) (map[string]gitutil.PRResult, error) {
		out := map[string]gitutil.PRResult{}
		for branch := range branches {
			out[branch] = gitutil.PRResult{State: model.PRNone}
		}
		return out, nil
	}
	d.prResolveRepo = resolveAll("drn/argus")
	d.pollPRStatesOnce(context.Background())

	meta, err := d.db.ListMeta("t1", "pr")
	testutil.NoError(t, err)
	got := map[string]string{}
	for _, e := range meta {
		got[e.Key] = e.Value
	}
	testutil.Equal(t, got["state"], "none")
}

// TestPollPR_GoroutineStopsOnShutdown verifies the poller goroutine
// (runPRPoller, the body Serve launches) terminates via d.done so daemon
// shutdown does not hang. We drive the goroutine directly — no socket, no
// Serve — and assert it returns promptly once d.done is closed. The poll
// interval is 60s so no tick fires; the only way runPRPoller returns is via
// the d.done branch of its select. This mirrors the lightweight pattern the
// MCP idle-sweep tests use (test the loop, not a real listener).
func TestPollPR_GoroutineStopsOnShutdown(t *testing.T) {
	d, _ := testDaemon(t)

	stopped := make(chan struct{})
	go func() {
		d.runPRPoller()
		close(stopped)
	}()

	// Closing d.done is exactly what Shutdown does for every gated goroutine.
	close(d.done)

	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("runPRPoller did not return after d.done closed (goroutine stuck)")
	}
}

// TestPollPR_InFlightFetchSeesCancellation verifies that the context handed to
// pollPRStatesOnce (the same one runPRPoller cancels on shutdown) propagates
// into the batch-fetch seam so an in-flight `gh api graphql` query aborts
// promptly instead of running to its timeout. Socketless — we drive
// pollPRStatesOnce directly with a cancelable context, mirroring what
// runPRPoller's d.done branch does.
func TestPollPR_InFlightFetchSeesCancellation(t *testing.T) {
	d, _ := testDaemon(t)
	seedTask(t, d.db, "t1", "argus/t1", false)

	entered := make(chan struct{})
	sawCancel := make(chan struct{})
	d.prBatchFetch = func(ctx context.Context, _ string, _ map[string]string) (map[string]gitutil.PRResult, error) {
		close(entered)
		select {
		case <-ctx.Done():
			close(sawCancel)
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
			t.Error("fetch ran to timeout — ctx cancellation did not propagate")
			return map[string]gitutil.PRResult{}, nil
		}
	}
	d.prResolveRepo = resolveAll("drn/argus")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		d.pollPRStatesOnce(ctx)
		close(done)
	}()

	<-entered
	cancel() // exactly what runPRPoller's d.done branch does on shutdown

	select {
	case <-sawCancel:
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight fetch never observed context cancellation")
	}
	<-done
}

func TestPollPR_ListTasksError(t *testing.T) {
	d, _ := testDaemon(t)
	seedTask(t, d.db, "t1", "argus/t1", false)

	var called bool
	d.prBatchFetch = func(_ context.Context, _ string, _ map[string]string) (map[string]gitutil.PRResult, error) {
		called = true
		return map[string]gitutil.PRResult{}, nil
	}
	d.prResolveRepo = resolveAll("drn/argus")
	// Close the DB so Tasks() errors — the poll must bail without fetching.
	testutil.NoError(t, d.db.Close())

	d.pollPRStatesOnce(context.Background())
	testutil.Equal(t, called, false)
}

func TestPollPR_NoEligibleTasks(t *testing.T) {
	d, _ := testDaemon(t)
	// Only ineligible tasks exist.
	seedTask(t, d.db, "archived", "argus/a", true)

	var called bool
	d.prBatchFetch = func(_ context.Context, _ string, _ map[string]string) (map[string]gitutil.PRResult, error) {
		called = true
		return map[string]gitutil.PRResult{}, nil
	}
	d.prResolveRepo = resolveAll("drn/argus")
	d.pollPRStatesOnce(context.Background())
	testutil.Equal(t, called, false)
}
