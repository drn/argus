package terminal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/testutil"
)

func splashPane(t *testing.T, status string) *TerminalPane {
	t.Helper()
	t.Setenv("HOME", t.TempDir()) // session-log lookups must never touch the real ~/.argus
	tp := NewTerminalPane()
	tp.EnableLaunchSplash(true)
	tp.SetLaunchTaskStatus(status)
	return tp
}

func TestLaunchSplash_PendingNoSession_PreparingWorktree(t *testing.T) {
	tp := splashPane(t, "pending")
	out := drawOnSim(t, tp, 80, 24)
	testutil.Contains(t, out, "P R E P A R I N G")
	testutil.Contains(t, out, "██") // gradient LAUNCH wordmark
}

func TestLaunchSplash_AliveNoOutput_StartingAgent(t *testing.T) {
	tp := splashPane(t, "in_progress")
	tp.SetSession(&mockAdapter{alive: true})
	out := drawOnSim(t, tp, 80, 24)
	testutil.Contains(t, out, "S T A R T I N G")
}

func TestLaunchSplash_FirstOutputDismisses(t *testing.T) {
	tp := splashPane(t, "in_progress")
	m := &mockAdapter{alive: true}
	tp.SetSession(m)
	testutil.Contains(t, drawOnSim(t, tp, 80, 24), "S T A R T I N G")
	m.totalWritten = 5
	m.output = []byte("hello-live")
	out := drawOnSim(t, tp, 80, 24)
	if strings.Contains(out, "S T A R T I N G") {
		t.Fatalf("splash must end at first output:\n%s", out)
	}
}

func TestLaunchSplash_DeadInProgressSessionStaysPlaceholder(t *testing.T) {
	// A dead session on a task that is not pending is "press Enter", never a launch.
	tp := splashPane(t, "in_progress")
	tp.SetTaskID("t1")
	out := drawOnSim(t, tp, 80, 24)
	if strings.Contains(out, "P R E P A R I N G") {
		t.Fatalf("no splash for non-launching task:\n%s", out)
	}
	testutil.Contains(t, out, "Session not running")
}

func TestLaunchSplash_DisabledByDefault(t *testing.T) {
	tp := NewTerminalPane()
	tp.SetLaunchTaskStatus("pending")
	out := drawOnSim(t, tp, 80, 24)
	if strings.Contains(out, "P R E P A R I N G") {
		t.Fatalf("splash must be opt-in:\n%s", out)
	}
}

func TestLaunchSplash_ClosedOutBannerOutranks(t *testing.T) {
	tp := splashPane(t, "pending")
	tp.ShowClosedOutBanner()
	out := drawOnSim(t, tp, 80, 24)
	if strings.Contains(out, "P R E P A R I N G") {
		t.Fatalf("closed-out banner must outrank splash:\n%s", out)
	}
}

func TestLaunchSplash_RerenderingOutranks(t *testing.T) {
	tp := splashPane(t, "in_progress")
	tp.SetSession(&mockAdapter{alive: true})
	tp.mu.Lock()
	tp.rerendering = true
	tp.mu.Unlock()
	out := drawOnSim(t, tp, 80, 24)
	if strings.Contains(out, "S T A R T I N G") {
		t.Fatalf("re-rendering must outrank splash:\n%s", out)
	}
}

func TestLaunchSplash_TimeoutFallsThrough(t *testing.T) {
	now := time.Now()
	old := launchNow
	launchNow = func() time.Time { return now }
	t.Cleanup(func() { launchNow = old })

	tp := splashPane(t, "in_progress")
	tp.SetSession(&mockAdapter{alive: true})
	testutil.Contains(t, drawOnSim(t, tp, 80, 24), "S T A R T I N G")
	now = now.Add(launchSplashTimeout + time.Second)
	if strings.Contains(drawOnSim(t, tp, 80, 24), "S T A R T I N G") {
		t.Fatal("splash must give up after the cap")
	}
}

func TestLaunchSplash_ShortRectFallsBackToStatusLine(t *testing.T) {
	tp := splashPane(t, "pending")
	out := drawOnSim(t, tp, 60, 8)
	testutil.Contains(t, out, "P R E P A R I N G")
	if strings.Contains(out, "██") {
		t.Fatalf("wordmark must not draw when the rect is too short:\n%s", out)
	}
}

func TestLaunchSplash_NewSessionRestartsCapClock(t *testing.T) {
	now := time.Now()
	old := launchNow
	launchNow = func() time.Time { return now }
	t.Cleanup(func() { launchNow = old })

	tp := splashPane(t, "in_progress")
	tp.SetSession(&mockAdapter{alive: true})
	drawOnSim(t, tp, 80, 24)
	now = now.Add(launchSplashTimeout + time.Second)
	tp.SetSession(&mockAdapter{alive: true}) // revive: brand-new session
	testutil.Contains(t, drawOnSim(t, tp, 80, 24), "S T A R T I N G")
}

// writeTaskLog records prior output for taskID, the "history" signal that
// keeps the splash off (it is for a task's first launch only).
func writeTaskLog(t *testing.T, taskID string) {
	t.Helper()
	path := agent.SessionLogPath(taskID)
	testutil.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	testutil.NoError(t, os.WriteFile(path, []byte("earlier output\n"), 0o644))
}

func notSplash(t *testing.T, out string) {
	t.Helper()
	if strings.Contains(out, "P R E P A R I N G") || strings.Contains(out, "S T A R T I N G") {
		t.Fatalf("splash must not show:\n%s", out)
	}
}

func TestLaunchSplash_DeadSessionHandleNeverStarting(t *testing.T) {
	tp := splashPane(t, "in_progress")
	tp.SetTaskID("t1")
	tp.SetSession(&mockAdapter{alive: false})
	notSplash(t, drawOnSim(t, tp, 80, 24))
}

func TestLaunchSplash_TimedOutRearmsOnNilToNilRebind(t *testing.T) {
	now := time.Now()
	old := launchNow
	launchNow = func() time.Time { return now }
	t.Cleanup(func() { launchNow = old })

	tp := splashPane(t, "pending")
	tp.SetTaskID("a")
	testutil.Contains(t, drawOnSim(t, tp, 80, 24), "P R E P A R I N G")
	now = now.Add(launchSplashTimeout + time.Second)
	notSplash(t, drawOnSim(t, tp, 80, 24))
	// nil->nil rebind to another role: SetSession early-returns, so ResetVT is
	// the only thing that re-arms the splash.
	tp.SetTaskID("b")
	tp.ResetVT()
	tp.SetSession(nil)
	testutil.Contains(t, drawOnSim(t, tp, 80, 24), "P R E P A R I N G")
}

func TestLaunchSplash_NarrowRectFallsBackToStatusLine(t *testing.T) {
	tp := splashPane(t, "pending")
	out := drawOnSim(t, tp, 40, 24) // taller than the banner, narrower than the wordmark
	testutil.Contains(t, out, "P R E P A R I N G")
	if strings.Contains(out, "██") {
		t.Fatalf("right-clipped wordmark must not draw:\n%s", out)
	}
}

// Q1: a size-drift kill+resume / revive of a task with prior output swaps in a
// fresh zero-byte session once Re-rendering clears — no STARTING flash.
func TestLaunchSplash_KickOfTaskWithOutputShowsNoSplash(t *testing.T) {
	tp := splashPane(t, "in_progress")
	tp.SetTaskID("t1")
	writeTaskLog(t, "t1")
	tp.SetSession(&mockAdapter{alive: true, totalWritten: 5})
	notSplash(t, drawOnSim(t, tp, 80, 24)) // output observed → latched
	// Production respawn: NewSession O_TRUNCs the log, handle restarts at 0.
	testutil.NoError(t, os.Truncate(agent.SessionLogPath("t1"), 0))
	tp.ResetVT()
	tp.SetSession(&mockAdapter{alive: true})
	notSplash(t, drawOnSim(t, tp, 80, 24))
}

func TestLaunchSplash_HistoryLatchSurvivesRoundTripToOtherTask(t *testing.T) {
	tp := splashPane(t, "in_progress")
	tp.SetTaskID("t1")
	tp.SetSession(&mockAdapter{alive: true, totalWritten: 5})
	notSplash(t, drawOnSim(t, tp, 80, 24))
	tp.SetTaskID("t2")
	tp.ResetVT()
	tp.SetSession(&mockAdapter{alive: true, totalWritten: 3})
	notSplash(t, drawOnSim(t, tp, 80, 24))
	tp.SetTaskID("t1")
	tp.ResetVT()
	tp.SetSession(&mockAdapter{alive: true})
	notSplash(t, drawOnSim(t, tp, 80, 24))
}

// Q2: a fresh zero-byte RemoteSession handle for an established agent.
func TestLaunchSplash_FreshHandleForEstablishedTaskShowsNoStarting(t *testing.T) {
	tp := splashPane(t, "in_progress")
	tp.SetTaskID("t1")
	writeTaskLog(t, "t1")
	tp.SetSession(&mockAdapter{alive: true})
	notSplash(t, drawOnSim(t, tp, 80, 24))
}

// Q3: a failed revive reverts the task to pending, over the worker's history.
func TestLaunchSplash_PendingTaskWithOutputShowsNoPreparing(t *testing.T) {
	tp := splashPane(t, "pending")
	tp.SetTaskID("t1")
	writeTaskLog(t, "t1")
	notSplash(t, drawOnSim(t, tp, 80, 24))
}
