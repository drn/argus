package daemon

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drn/argus/internal/db"
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

func TestPollPR_SkipsArchivedAndBranchless(t *testing.T) {
	d, _ := testDaemon(t)
	seedTask(t, d.db, "normal", "argus/normal", false)
	seedTask(t, d.db, "archived", "argus/archived", true)
	seedTask(t, d.db, "branchless", "", false)

	var mu sync.Mutex
	fetched := map[string]bool{}
	d.prFetch = func(_ context.Context, _, branch string) (model.PRState, string, error) {
		mu.Lock()
		fetched[branch] = true
		mu.Unlock()
		return model.PRApproved, "https://example/pr/1", nil
	}

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

func TestPollPR_WritesStateAndURL(t *testing.T) {
	d, _ := testDaemon(t)
	seedTask(t, d.db, "t1", "argus/t1", false)

	d.prFetch = func(_ context.Context, _, _ string) (model.PRState, string, error) {
		return model.PRChangesRequested, "https://example/pr/9", nil
	}
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

	d.prFetch = func(_ context.Context, _, _ string) (model.PRState, string, error) {
		return model.PRNone, "", errors.New("network timeout")
	}
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

	// An unambiguous PRNone with nil error is authoritative and is written.
	d.prFetch = func(_ context.Context, _, _ string) (model.PRState, string, error) {
		return model.PRNone, "", nil
	}
	d.pollPRStatesOnce(context.Background())

	meta, err := d.db.ListMeta("t1", "pr")
	testutil.NoError(t, err)
	got := map[string]string{}
	for _, e := range meta {
		got[e.Key] = e.Value
	}
	testutil.Equal(t, got["state"], "none")
}

// TestPollPR_GoroutineStopsOnShutdown verifies the poller goroutine started in
// Serve terminates via d.done so daemon shutdown does not hang. The poll
// interval is 60s, so we don't observe a tick — we assert Serve returns
// promptly after Shutdown, which can only happen if every d.done-gated
// goroutine (including the poller) has exited its select.
func TestPollPR_GoroutineStopsOnShutdown(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, sockPath := testDaemon(t)

	errCh := make(chan error, 1)
	go func() { errCh <- d.Serve(sockPath) }()
	waitForSocket(t, sockPath)

	d.Shutdown()
	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after Shutdown (a d.done goroutine is stuck)")
	}
}

func TestPollPR_ListTasksError(t *testing.T) {
	d, _ := testDaemon(t)
	seedTask(t, d.db, "t1", "argus/t1", false)

	var called bool
	d.prFetch = func(_ context.Context, _, _ string) (model.PRState, string, error) {
		called = true
		return model.PRApproved, "u", nil
	}
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
	d.prFetch = func(_ context.Context, _, _ string) (model.PRState, string, error) {
		called = true
		return model.PRApproved, "u", nil
	}
	d.pollPRStatesOnce(context.Background())
	testutil.Equal(t, called, false)
}

func TestPollPR_ConcurrencyCapRespected(t *testing.T) {
	d, _ := testDaemon(t)
	for i := 0; i < 12; i++ {
		seedTask(t, d.db, "t"+string(rune('a'+i)), "argus/b"+string(rune('a'+i)), false)
	}

	var inFlight, maxInFlight int32
	release := make(chan struct{})
	var once sync.Once
	started := make(chan struct{})
	var startedOnce sync.Once

	d.prFetch = func(_ context.Context, _, _ string) (model.PRState, string, error) {
		cur := atomic.AddInt32(&inFlight, 1)
		for {
			m := atomic.LoadInt32(&maxInFlight)
			if cur <= m || atomic.CompareAndSwapInt32(&maxInFlight, m, cur) {
				break
			}
		}
		// Signal once we have the cap-many goroutines parked, then block until
		// the test releases them so the high-water mark is observable.
		if cur >= prPollConcurrency {
			startedOnce.Do(func() { close(started) })
		}
		<-release
		atomic.AddInt32(&inFlight, -1)
		return model.PRApproved, "u", nil
	}

	done := make(chan struct{})
	go func() {
		d.pollPRStatesOnce(context.Background())
		close(done)
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("never reached concurrency cap")
	}
	// Let everything proceed.
	once.Do(func() { close(release) })
	<-done

	if got := atomic.LoadInt32(&maxInFlight); got > prPollConcurrency {
		t.Fatalf("max concurrent fetches %d exceeded cap %d", got, prPollConcurrency)
	}
}
