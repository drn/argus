package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	d.prBatchFetch = func(_ context.Context, _ string, branches map[string]string) (map[string]gitutil.PRResult, int, error) {
		mu.Lock()
		out := map[string]gitutil.PRResult{}
		for branch := range branches {
			fetched[branch] = true
			out[branch] = gitutil.PRResult{State: model.PRApproved, URL: "https://example/pr/1"}
		}
		mu.Unlock()
		return out, 1, nil
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
	d.prBatchFetch = func(_ context.Context, _ string, branches map[string]string) (map[string]gitutil.PRResult, int, error) {
		mu.Lock()
		out := map[string]gitutil.PRResult{}
		for branch := range branches {
			fetched[branch] = true
			out[branch] = gitutil.PRResult{State: model.PRApproved, URL: "https://example/pr/open"}
		}
		mu.Unlock()
		return out, 1, nil
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
	d.prBatchFetch = func(_ context.Context, _ string, branches map[string]string) (map[string]gitutil.PRResult, int, error) {
		mu.Lock()
		out := map[string]gitutil.PRResult{}
		for branch := range branches {
			fetched[branch] = true
			out[branch] = gitutil.PRResult{State: model.PRApproved, URL: "u"}
		}
		mu.Unlock()
		return out, 1, nil
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

	d.prBatchFetch = func(_ context.Context, _ string, branches map[string]string) (map[string]gitutil.PRResult, int, error) {
		out := map[string]gitutil.PRResult{}
		for branch := range branches {
			out[branch] = gitutil.PRResult{State: model.PRChangesRequested, URL: "https://example/pr/9"}
		}
		return out, 1, nil
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

	d.prBatchFetch = func(_ context.Context, _ string, _ map[string]string) (map[string]gitutil.PRResult, int, error) {
		return nil, 0, errors.New("network timeout")
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
	d.prBatchFetch = func(_ context.Context, _ string, branches map[string]string) (map[string]gitutil.PRResult, int, error) {
		out := map[string]gitutil.PRResult{}
		for branch := range branches {
			out[branch] = gitutil.PRResult{State: model.PRNone}
		}
		return out, 1, nil
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
	d.prBatchFetch = func(ctx context.Context, _ string, _ map[string]string) (map[string]gitutil.PRResult, int, error) {
		close(entered)
		select {
		case <-ctx.Done():
			close(sawCancel)
			return nil, 0, ctx.Err()
		case <-time.After(5 * time.Second):
			t.Error("fetch ran to timeout — ctx cancellation did not propagate")
			return map[string]gitutil.PRResult{}, 0, nil
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
	d.prBatchFetch = func(_ context.Context, _ string, _ map[string]string) (map[string]gitutil.PRResult, int, error) {
		called = true
		return map[string]gitutil.PRResult{}, 0, nil
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
	d.prBatchFetch = func(_ context.Context, _ string, _ map[string]string) (map[string]gitutil.PRResult, int, error) {
		called = true
		return map[string]gitutil.PRResult{}, 0, nil
	}
	d.prResolveRepo = resolveAll("drn/argus")
	d.pollPRStatesOnce(context.Background())
	testutil.Equal(t, called, false)
}

func TestPRPollCadenceStride(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	aged := func(age time.Duration) *model.Task { return &model.Task{CreatedAt: now.Add(-age)} }

	cases := []struct {
		name  string
		task  *model.Task
		state model.PRState
		want  int
	}{
		{"within 1h, no PR", aged(30 * time.Minute), model.PRNone, 1},
		{"1-24h, no PR", aged(5 * time.Hour), model.PRNone, 5},
		{"1-7d, no PR", aged(3 * 24 * time.Hour), model.PRNone, 15},
		{"over 7d, no PR", aged(10 * 24 * time.Hour), model.PRNone, 30},
		{"over 7d, unknown", aged(10 * 24 * time.Hour), model.PRUnknown, 30},
		{"open PR floors a dormant task to hot: awaiting-review", aged(30 * 24 * time.Hour), model.PRAwaitingReview, 1},
		{"open PR floors a dormant task to hot: draft", aged(30 * 24 * time.Hour), model.PRDraft, 1},
		{"open PR floors a dormant task to hot: approved", aged(30 * 24 * time.Hour), model.PRApproved, 1},
		{"open PR floors a dormant task to hot: changes-requested", aged(30 * 24 * time.Hour), model.PRChangesRequested, 1},
		{
			"most-recent lifecycle ts wins (fresh ended_at beats old created_at)",
			&model.Task{CreatedAt: now.Add(-10 * 24 * time.Hour), EndedAt: now.Add(-20 * time.Minute)},
			model.PRNone, 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testutil.Equal(t, prPollCadenceStride(tc.task, tc.state, now), tc.want)
		})
	}
}

func TestPRCadenceSelects(t *testing.T) {
	// Stride 1 is due every cycle.
	for c := uint64(0); c < 5; c++ {
		testutil.Equal(t, prCadenceSelects(c, "x", 1), true)
	}
	// Stride 30: due on exactly one of any 30 consecutive cycles.
	got := 0
	for c := uint64(0); c < 30; c++ {
		if prCadenceSelects(c, "task-abc", 30) {
			got++
		}
	}
	testutil.Equal(t, got, 1)
}

func TestPRCadenceSpread(t *testing.T) {
	// Distinct task ids in the same tier land on different cycles (phased by
	// id hash) rather than all firing on the same cycle.
	firstFire := func(id string) uint64 {
		for c := uint64(0); c < 30; c++ {
			if prCadenceSelects(c, id, 30) {
				return c
			}
		}
		return 999
	}
	offsets := map[uint64]bool{}
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		offsets[firstFire(id)] = true
	}
	// At least two distinct fire-cycles → the tier is spread, not bunched.
	testutil.Equal(t, len(offsets) > 1, true)
}

func TestPollPR_DefersDormantPRlessTask(t *testing.T) {
	d, _ := testDaemon(t)
	// Dormant (>7d), no PR → frozen tier (stride 30).
	old := time.Now().Add(-10 * 24 * time.Hour)
	task := &model.Task{
		ID: "dormant", Name: "dormant", Status: model.StatusInReview,
		Project: "p", Branch: "argus/dormant", Worktree: "/tmp/wt/dormant", CreatedAt: old,
	}
	testutil.NoError(t, d.db.Add(task))
	d.prResolveRepo = resolveAll("drn/argus")

	var calls int
	d.prBatchFetch = func(_ context.Context, _ string, branches map[string]string) (map[string]gitutil.PRResult, int, error) {
		calls++
		out := map[string]gitutil.PRResult{}
		for b := range branches {
			out[b] = gitutil.PRResult{State: model.PRNone}
		}
		return out, 1, nil
	}

	// Pick a cycle where the frozen task is NOT due: no fetch, cache untouched.
	for c := uint64(0); c < 30; c++ {
		if !prCadenceSelects(c, "dormant", 30) {
			d.pollCycle = c
			break
		}
	}
	d.pollPRStatesOnce(context.Background())
	testutil.Equal(t, calls, 0)
	meta, err := d.db.ListMetaByNamespace("pr")
	testutil.NoError(t, err)
	testutil.Equal(t, len(meta), 0)

	// On a cycle where it IS due, it gets queried.
	for c := uint64(0); c < 30; c++ {
		if prCadenceSelects(c, "dormant", 30) {
			d.pollCycle = c
			break
		}
	}
	d.pollPRStatesOnce(context.Background())
	testutil.Equal(t, calls, 1)
}

func TestPollPR_OpenPROverridesDormancy(t *testing.T) {
	d, _ := testDaemon(t)
	// Very old task, but it has an open PR → polled every cycle regardless.
	old := time.Now().Add(-30 * 24 * time.Hour)
	task := &model.Task{
		ID: "oldpr", Name: "oldpr", Status: model.StatusInReview,
		Project: "p", Branch: "argus/oldpr", Worktree: "/tmp/wt/oldpr", CreatedAt: old,
	}
	testutil.NoError(t, d.db.Add(task))
	testutil.NoError(t, d.db.SetMetaBatch("oldpr", "pr", map[string]string{
		"state": "awaiting-review", "url": "https://example/pr/9",
	}))
	d.prResolveRepo = resolveAll("drn/argus")

	var calls int
	d.prBatchFetch = func(_ context.Context, _ string, branches map[string]string) (map[string]gitutil.PRResult, int, error) {
		calls++
		out := map[string]gitutil.PRResult{}
		for b := range branches {
			out[b] = gitutil.PRResult{State: model.PRApproved, URL: "u"}
		}
		return out, 1, nil
	}

	// An arbitrary cycle where a frozen-tier task would NOT be due — the open PR
	// floor still forces a poll.
	for c := uint64(0); c < 30; c++ {
		if !prCadenceSelects(c, "oldpr", 30) {
			d.pollCycle = c
			break
		}
	}
	d.pollPRStatesOnce(context.Background())
	testutil.Equal(t, calls, 1)
}

func TestPollPR_PausedByKillSwitch(t *testing.T) {
	d, _ := testDaemon(t)
	seedTask(t, d.db, "live", "argus/live", false)

	// A present sentinel file pauses the poller before any gh query.
	flag := filepath.Join(t.TempDir(), prPollDisableFlag)
	if err := os.WriteFile(flag, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	d.prDisableFlagPath = flag
	d.prResolveRepo = resolveAll("drn/argus")

	var called bool
	d.prBatchFetch = func(_ context.Context, _ string, _ map[string]string) (map[string]gitutil.PRResult, int, error) {
		called = true
		return map[string]gitutil.PRResult{}, 0, nil
	}

	d.pollPRStatesOnce(context.Background())

	// Paused: zero gh queries and nothing written.
	testutil.Equal(t, called, false)
	meta, err := d.db.ListMetaByNamespace("pr")
	testutil.NoError(t, err)
	testutil.Equal(t, len(meta), 0)

	// Removing the sentinel resumes polling on the next cycle.
	if err := os.Remove(flag); err != nil {
		t.Fatal(err)
	}
	d.prBatchFetch = func(_ context.Context, _ string, branches map[string]string) (map[string]gitutil.PRResult, int, error) {
		out := map[string]gitutil.PRResult{}
		for b := range branches {
			out[b] = gitutil.PRResult{State: model.PRApproved, URL: "https://example/pr/1"}
		}
		return out, 1, nil
	}
	d.pollPRStatesOnce(context.Background())

	meta, err = d.db.ListMetaByNamespace("pr")
	testutil.NoError(t, err)
	testutil.Equal(t, meta["live"]["state"], "approved")
}

// --- PR-merge nudges the task's Hera coordinator (add-hera-accept-lifecycle) ---

// seedHeraWorkerWithCoordinator creates an orchestrator with a coordinator
// role (its own task) and a worker role bound to taskID (which the caller
// must have already added via seedTask, at worktree "/tmp/wt/"+taskID),
// returning both role rows so tests can assert on their inboxes.
func seedHeraWorkerWithCoordinator(t *testing.T, database *db.DB, orchName, taskID string) (workerRole, coordRole *db.HeraRole) {
	t.Helper()
	orch, err := database.CreateHeraOrchestrator(orchName, "")
	testutil.NoError(t, err)

	coordTask := &model.Task{Name: orchName + "-coord", Status: model.StatusInProgress, Project: "p"}
	testutil.NoError(t, database.Add(coordTask))
	coordRole, _, err = database.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.HeraKindCoordinator, ArgusProject: "p",
	}, coordTask.ID, "/wt/"+coordTask.ID)
	testutil.NoError(t, err)

	workerRole, _, err = database.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "worker", Kind: db.HeraKindWorker, ArgusProject: "p",
	}, taskID, "/tmp/wt/"+taskID)
	testutil.NoError(t, err)
	return workerRole, coordRole
}

func TestPollPR_MergedTransitionNudgesCoordinator(t *testing.T) {
	d, _ := testDaemon(t)
	seedTask(t, d.db, "worker-task", "argus/worker", false)
	_, coordRole := seedHeraWorkerWithCoordinator(t, d.db, "O", "worker-task")

	d.prBatchFetch = func(_ context.Context, _ string, branches map[string]string) (map[string]gitutil.PRResult, int, error) {
		out := map[string]gitutil.PRResult{}
		for branch := range branches {
			out[branch] = gitutil.PRResult{State: model.PRMergedClosed, URL: "https://example/pr/1", Merged: true}
		}
		return out, 1, nil
	}
	d.prResolveRepo = resolveAll("drn/argus")
	d.pollPRStatesOnce(context.Background())

	msgs, err := d.db.HeraInbox(coordRole.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(msgs), 1)
	testutil.Contains(t, msgs[0].Body, "worker-task")
	testutil.Contains(t, msgs[0].Body, "https://example/pr/1")

	// Never a status or hera role-status change (seedTask seeds StatusInReview).
	got, err := d.db.Get("worker-task")
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, model.StatusInReview)
}

func TestPollPR_NonHeraTaskNeverNudges(t *testing.T) {
	d, _ := testDaemon(t)
	seedTask(t, d.db, "plain-task", "argus/plain", false)

	d.prBatchFetch = func(_ context.Context, _ string, branches map[string]string) (map[string]gitutil.PRResult, int, error) {
		out := map[string]gitutil.PRResult{}
		for branch := range branches {
			out[branch] = gitutil.PRResult{State: model.PRMergedClosed, URL: "https://example/pr/1", Merged: true}
		}
		return out, 1, nil
	}
	d.prResolveRepo = resolveAll("drn/argus")

	// Must not panic or error even though the task never held a Hera role.
	d.pollPRStatesOnce(context.Background())

	meta, err := d.db.ListMeta("plain-task", "pr")
	testutil.NoError(t, err)
	got := map[string]string{}
	for _, e := range meta {
		got[e.Key] = e.Value
	}
	testutil.Equal(t, got["state"], "merged-closed")
}

func TestPollPR_UnmergedCloseNeverNudges(t *testing.T) {
	d, _ := testDaemon(t)
	seedTask(t, d.db, "worker-task", "argus/worker", false)
	_, coordRole := seedHeraWorkerWithCoordinator(t, d.db, "O", "worker-task")

	d.prBatchFetch = func(_ context.Context, _ string, branches map[string]string) (map[string]gitutil.PRResult, int, error) {
		out := map[string]gitutil.PRResult{}
		for branch := range branches {
			// State collapses to merged-closed too, but Merged is false.
			out[branch] = gitutil.PRResult{State: model.PRMergedClosed, URL: "https://example/pr/1", Merged: false}
		}
		return out, 1, nil
	}
	d.prResolveRepo = resolveAll("drn/argus")
	d.pollPRStatesOnce(context.Background())

	msgs, err := d.db.HeraInbox(coordRole.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(msgs), 0)
}

func TestPollPR_NoResolvableCoordinatorIsSilentNoOp(t *testing.T) {
	d, _ := testDaemon(t)
	seedTask(t, d.db, "worker-task", "argus/worker", false)

	orch, err := d.db.CreateHeraOrchestrator("O", "")
	testutil.NoError(t, err)
	_, _, err = d.db.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "worker", Kind: db.HeraKindWorker, ArgusProject: "p",
	}, "worker-task", "/tmp/wt/worker-task")
	testutil.NoError(t, err)
	// No coordinator role in this orchestrator at all.

	d.prBatchFetch = func(_ context.Context, _ string, branches map[string]string) (map[string]gitutil.PRResult, int, error) {
		out := map[string]gitutil.PRResult{}
		for branch := range branches {
			out[branch] = gitutil.PRResult{State: model.PRMergedClosed, URL: "https://example/pr/1", Merged: true}
		}
		return out, 1, nil
	}
	d.prResolveRepo = resolveAll("drn/argus")

	// Must not panic or error with no coordinator to notify.
	d.pollPRStatesOnce(context.Background())
}

func TestPollPR_CoordinatorsOwnMergedPRDoesNotSelfNudge(t *testing.T) {
	d, _ := testDaemon(t)
	seedTask(t, d.db, "coord-task", "argus/coord", false)

	orch, err := d.db.CreateHeraOrchestrator("O", "")
	testutil.NoError(t, err)
	coordRole, _, err := d.db.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.HeraKindCoordinator, ArgusProject: "p",
	}, "coord-task", "/tmp/wt/coord-task")
	testutil.NoError(t, err)

	d.prBatchFetch = func(_ context.Context, _ string, branches map[string]string) (map[string]gitutil.PRResult, int, error) {
		out := map[string]gitutil.PRResult{}
		for branch := range branches {
			out[branch] = gitutil.PRResult{State: model.PRMergedClosed, URL: "https://example/pr/1", Merged: true}
		}
		return out, 1, nil
	}
	d.prResolveRepo = resolveAll("drn/argus")
	d.pollPRStatesOnce(context.Background())

	msgs, err := d.db.HeraInbox(coordRole.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(msgs), 0)
}

func TestPollPR_MergeNudgeNeverFiresTwice(t *testing.T) {
	d, _ := testDaemon(t)
	seedTask(t, d.db, "worker-task", "argus/worker", false)
	_, coordRole := seedHeraWorkerWithCoordinator(t, d.db, "O", "worker-task")

	var calls int
	d.prBatchFetch = func(_ context.Context, _ string, branches map[string]string) (map[string]gitutil.PRResult, int, error) {
		calls++
		out := map[string]gitutil.PRResult{}
		for branch := range branches {
			out[branch] = gitutil.PRResult{State: model.PRMergedClosed, URL: "https://example/pr/1", Merged: true}
		}
		return out, 1, nil
	}
	d.prResolveRepo = resolveAll("drn/argus")

	d.pollPRStatesOnce(context.Background())
	d.pollPRStatesOnce(context.Background()) // second cycle: already terminal, excluded entirely

	testutil.Equal(t, calls, 1) // the terminal-state skip means only the first cycle ever fetched
	msgs, err := d.db.HeraInbox(coordRole.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(msgs), 1)
}
