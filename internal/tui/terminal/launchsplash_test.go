package terminal

import (
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

func splashPane(status string) *TerminalPane {
	tp := NewTerminalPane()
	tp.EnableLaunchSplash(true)
	tp.SetLaunchTaskStatus(status)
	return tp
}

func TestLaunchSplash_PendingNoSession_PreparingWorktree(t *testing.T) {
	tp := splashPane("pending")
	out := drawOnSim(t, tp, 80, 24)
	testutil.Contains(t, out, "P R E P A R I N G")
	testutil.Contains(t, out, "██") // gradient LAUNCH wordmark
}

func TestLaunchSplash_AliveNoOutput_StartingAgent(t *testing.T) {
	tp := splashPane("in_progress")
	tp.SetSession(&mockAdapter{alive: true})
	out := drawOnSim(t, tp, 80, 24)
	testutil.Contains(t, out, "S T A R T I N G")
}

func TestLaunchSplash_FirstOutputDismisses(t *testing.T) {
	tp := splashPane("in_progress")
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
	tp := splashPane("in_progress")
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
	tp := splashPane("pending")
	tp.ShowClosedOutBanner()
	out := drawOnSim(t, tp, 80, 24)
	if strings.Contains(out, "P R E P A R I N G") {
		t.Fatalf("closed-out banner must outrank splash:\n%s", out)
	}
}

func TestLaunchSplash_RerenderingOutranks(t *testing.T) {
	tp := splashPane("in_progress")
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

	tp := splashPane("in_progress")
	tp.SetSession(&mockAdapter{alive: true})
	testutil.Contains(t, drawOnSim(t, tp, 80, 24), "S T A R T I N G")
	now = now.Add(launchSplashTimeout + time.Second)
	if strings.Contains(drawOnSim(t, tp, 80, 24), "S T A R T I N G") {
		t.Fatal("splash must give up after the cap")
	}
}

func TestLaunchSplash_ShortRectFallsBackToStatusLine(t *testing.T) {
	tp := splashPane("pending")
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

	tp := splashPane("in_progress")
	tp.SetSession(&mockAdapter{alive: true})
	drawOnSim(t, tp, 80, 24)
	now = now.Add(launchSplashTimeout + time.Second)
	tp.SetSession(&mockAdapter{alive: true}) // revive: brand-new session
	testutil.Contains(t, drawOnSim(t, tp, 80, 24), "S T A R T I N G")
}
