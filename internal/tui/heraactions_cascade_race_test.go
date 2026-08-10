package tui

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/hera"
)

// slowStopRunner wraps a real *agent.Runner but simulates a live session for
// every task (HasSession always true) whose Stop() takes `delay` to complete
// — modeling a daemon-connected TUI's blocking RPC round-trip (client.Stop →
// Daemon.StopSession → possibly proxied again to the session-supervisor) under
// load, rather than the in-process runner's near-instant SIGTERM. calls counts
// completed Stop() invocations so a test can wait for backgrounded stops to
// land without a fixed sleep.
type slowStopRunner struct {
	*agent.Runner
	delay time.Duration
	calls atomic.Int32
}

func (r *slowStopRunner) HasSession(taskID string) bool { return true }

func (r *slowStopRunner) Stop(taskID string) error {
	time.Sleep(r.delay)
	r.calls.Add(1)
	return nil
}

// TestHeraCascadeNuke_BulkDeleteDoesNotBlockCaller is the confirmed-mechanism
// repro for the reported crash: ground-truth log mining showed the TUI going
// completely silent (no panic anywhere, incl. the fd-2-redirected uxlog) for
// ~6.76s after a bulk Hera cascade delete (6 coordinators, ~40 roles, 6s
// window), then a restart. Neither hypothesized data race (concurrent
// RemoveWorktreeAndBranch goroutines vs a shared repo; Stop() vs a pane
// poller) reproduces under `go test -race` — see
// TestRemoveWorktreeAndBranch_ConcurrentSameRepo and
// TestSession_StopConcurrentWithPanePoll in internal/agent. What DOES
// reproduce: heraReclaimAndArchiveTask calls a.runner.Stop() SYNCHRONOUSLY on
// the tview main goroutine once per nuked task with a live session. In a
// daemon-connected TUI (confirmed by the incident's daemon.log/supervisor.log)
// that Stop() is a blocking RPC, not a cheap in-process SIGTERM — so a bulk
// cascade of N tasks blocks Draw/input/tick (all serialize through the same
// tview goroutine) for N * per-call-latency, with nothing to panic on. That
// is a freeze, not a crash, which is exactly why no panic text ever appears.
//
// This test proves the fix (backgrounding the Stop() call in
// heraReclaimAndArchiveTask, mirroring the existing worktree-removal
// goroutine): a bulk cascade over N tasks whose Stop() call is individually
// slow must still return to the caller near-instantly.
func TestHeraCascadeNuke_BulkDeleteDoesNotBlockCaller(t *testing.T) {
	d := testDB(t)
	t.Setenv("HOME", t.TempDir())
	runner := &slowStopRunner{Runner: agent.NewRunner(nil), delay: 15 * time.Millisecond}
	app := New(d, runner, false)

	orch := seedHeraOrch(t, d, "bulk")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	const n = 30
	for i := 0; i < n; i++ {
		seedHeraBoundRole(t, d, orch, fmt.Sprintf("w%d", i), db.HeraKindWorker, fmt.Sprintf("tw%d", i))
	}
	app.heraPage.Refresh()

	// add-merge-safety-review: the cascade confirm now opens only after an
	// off-UI-thread Tier-A classification pass completes (QueueUpdateDraw),
	// so a real running event loop is required here — a bare New() app with
	// no Run() loop would deadlock the classify goroutine's QueueUpdateDraw
	// forever, as this test used to invoke heraOpenDelete/heraConfirmDo
	// directly with no loop at all.
	_, stop := wireApp(t, app)
	defer stop()

	readUI(t, app.tapp, func() {
		app.heraOpenDelete(hera.Selection{Orch: &hera.OrchView{ID: orch, Name: "bulk"}})
	})
	waitForMode(t, app, modeHeraConfirm)

	start := time.Now()
	readUI(t, app.tapp, func() { app.heraConfirmDo() })
	elapsed := time.Since(start)

	// n workers + 1 coordinator = n+1 live sessions, each with a 15ms Stop().
	// Sequential-on-caller would take >= (n+1)*15ms == 465ms; backgrounded,
	// the caller returns immediately regardless of N.
	const sequentialFloor = (n + 1) * 15 * time.Millisecond
	if elapsed >= sequentialFloor {
		t.Fatalf("heraDoCascadeNuke blocked the caller for %v (>= the %v sequential-Stop floor) — "+
			"bulk cascade Stop() calls must be backgrounded, not run synchronously on the tview goroutine",
			elapsed, sequentialFloor)
	}

	// The stops still happen — just not on the caller's goroutine. Poll
	// (bounded) rather than a fixed sleep, since the whole point is that
	// completion is decoupled from heraConfirmDo's return.
	deadline := time.Now().Add(2 * time.Second)
	for runner.calls.Load() < n+1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	testutil.Equal(t, runner.calls.Load(), int32(n+1))
}
