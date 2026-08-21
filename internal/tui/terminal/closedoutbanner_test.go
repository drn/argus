package terminal

import (
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

// screenText flattens a SimulationScreen's cell grid into one string (rows
// joined by newline) for substring assertions.
func screenText(t *testing.T, sim tcell.SimulationScreen, w, h int) string {
	t.Helper()
	sim.Show()
	cells, _, _ := sim.GetContents()
	var b strings.Builder
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			c := cells[row*w+col]
			if len(c.Runes) > 0 {
				b.WriteRune(c.Runes[0])
			} else {
				b.WriteRune(' ')
			}
		}
		b.WriteRune('\n')
	}
	return b.String()
}

// drawOnSim draws tp onto a fresh w x h SimulationScreen and returns its
// flattened text.
func drawOnSim(t *testing.T, tp *TerminalPane, w, h int) string {
	t.Helper()
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	t.Cleanup(sim.Fini)
	sim.SetSize(w, h)
	tp.SetRect(0, 0, w, h)
	tp.Draw(sim)
	return screenText(t, sim, w, h)
}

// TestTerminalPane_ClosedOutBanner_ShowDismissShown pins the basic state
// machine: Show arms it, Dismiss clears it, Shown reports the current state,
// and each transition only fires OnBranchChange when the state actually
// changes (mirrors SetPending's contract).
func TestTerminalPane_ClosedOutBanner_ShowDismissShown(t *testing.T) {
	tp := NewTerminalPane()
	branchChanges := 0
	tp.OnBranchChange = func() { branchChanges++ }

	testutil.Equal(t, tp.ClosedOutBannerShown(), false)

	tp.ShowClosedOutBanner()
	testutil.Equal(t, tp.ClosedOutBannerShown(), true)
	testutil.Equal(t, branchChanges, 1)

	// Showing again while already shown is a no-op — no extra branch change.
	tp.ShowClosedOutBanner()
	testutil.Equal(t, tp.ClosedOutBannerShown(), true)
	testutil.Equal(t, branchChanges, 1)

	tp.DismissClosedOutBanner()
	testutil.Equal(t, tp.ClosedOutBannerShown(), false)
	testutil.Equal(t, branchChanges, 2)

	// Dismissing again while already dismissed is a no-op.
	tp.DismissClosedOutBanner()
	testutil.Equal(t, tp.ClosedOutBannerShown(), false)
	testutil.Equal(t, branchChanges, 2)

	// Toggling back re-arms it (heraReattach's third-Enter behavior).
	tp.ShowClosedOutBanner()
	testutil.Equal(t, tp.ClosedOutBannerShown(), true)
	testutil.Equal(t, branchChanges, 3)
}

// TestTerminalPane_ResetVT_ClearsClosedOutBanner pins the "resets per visit"
// requirement: ResetVT (fired on every hera pane rebind via panes.go's
// bindPane) clears an armed banner, so leaving and returning to the same
// closed-out task re-arms it fresh rather than carrying the flag forward.
func TestTerminalPane_ResetVT_ClearsClosedOutBanner(t *testing.T) {
	tp := NewTerminalPane()
	tp.ShowClosedOutBanner()
	testutil.Equal(t, tp.ClosedOutBannerShown(), true)

	tp.ResetVT()
	testutil.Equal(t, tp.ClosedOutBannerShown(), false)
}

// TestTerminalPane_Draw_ClosedOutBanner_OverridesPlaceholder proves the
// banner takes priority over the ordinary "Session not running" placeholder
// (the case where the closed-out task recorded no session log at all).
func TestTerminalPane_Draw_ClosedOutBanner_OverridesPlaceholder(t *testing.T) {
	tp := NewTerminalPane()
	// Mirrors panes.go's real bindPane sequence (SetTaskID -> ResetVT ->
	// SetSession), not just SetTaskID alone — see fix-closeout-replay-load-
	// order below for why that distinction matters.
	tp.SetTaskID("no-log-task") // no session log written — HasContent() stays false
	tp.ResetVT()
	tp.SetSession(nil)
	tp.ShowClosedOutBanner()

	got := drawOnSim(t, tp, 60, 14)
	testutil.Contains(t, got, "Task closed out")
	testutil.Contains(t, got, "hera_revive")
	if strings.Contains(got, "Session not running") {
		t.Errorf("banner should replace the placeholder, got:\n%s", got)
	}
}

// TestTerminalPane_Draw_ClosedOutBanner_OverridesReplay proves the banner
// takes priority even when the pane DOES have replay content loaded (the
// task genuinely produced output before closing out) — the whole point of
// the banner is to interrupt the passive auto-replay Draw() would otherwise
// show for any dead session with content.
func TestTerminalPane_Draw_ClosedOutBanner_OverridesReplay(t *testing.T) {
	setupTaskLog(t, "closed-out-with-log", "some prior agent output\r\n")
	tp := NewTerminalPane()
	// Real bindPane order — see the OverridesPlaceholder test above.
	tp.SetTaskID("closed-out-with-log")
	tp.ResetVT()
	tp.SetSession(nil)
	tp.ShowClosedOutBanner()

	got := drawOnSim(t, tp, 60, 14)
	testutil.Contains(t, got, "Task closed out")
	if strings.Contains(got, "some prior agent output") {
		t.Errorf("banner should replace replay content, got:\n%s", got)
	}
}

// TestTerminalPane_Draw_ClosedOutBannerDismissed_FallsThroughToReplay proves
// dismissal adds no new rendering path: once dismissed, Draw() shows exactly
// what an ordinary dead-session pane with recorded content shows — the
// existing replay mechanism, verbatim.
func TestTerminalPane_Draw_ClosedOutBannerDismissed_FallsThroughToReplay(t *testing.T) {
	setupTaskLog(t, "closed-out-dismissed", "replay me please\r\n")
	tp := NewTerminalPane()
	// Real bindPane order — see TestTerminalPane_Draw_ClosedOutBanner_
	// OverridesPlaceholder's comment. This is the exact sequence
	// fix-closeout-replay-load-order fixed: without it, ResetVT wiped
	// SetTaskID's eager load and this test would have caught the "falls back
	// to the placeholder instead of replay" live regression.
	tp.SetTaskID("closed-out-dismissed")
	tp.ResetVT()
	tp.SetSession(nil)
	tp.ShowClosedOutBanner()
	tp.DismissClosedOutBanner()

	got := waitForReplayText(t, tp, 60, 14, "replay me please")
	if strings.Contains(got, "Task closed out") {
		t.Errorf("banner should not show once dismissed, got:\n%s", got)
	}
}

// TestTerminalPane_Draw_ClosedOutBannerDismissed_NoContentFallsToPlaceholder
// covers the accepted edge case from design.md Decision 3: a closed-out task
// with no recorded session log falls through, once dismissed, to the
// ordinary "Session not running" placeholder rather than any bespoke
// "nothing was ever recorded" message.
func TestTerminalPane_Draw_ClosedOutBannerDismissed_NoContentFallsToPlaceholder(t *testing.T) {
	tp := NewTerminalPane()
	// Real bindPane order — see TestTerminalPane_Draw_ClosedOutBanner_
	// OverridesPlaceholder's comment.
	tp.SetTaskID("closed-out-no-log")
	tp.ResetVT()
	tp.SetSession(nil)
	tp.ShowClosedOutBanner()
	tp.DismissClosedOutBanner()

	got := drawOnSim(t, tp, 60, 14)
	testutil.Contains(t, got, "Session not running")
	if strings.Contains(got, "Task closed out") {
		t.Errorf("banner should not show once dismissed, got:\n%s", got)
	}
}

// TestTerminalPane_Draw_ClosedOutBannerNeverShowsOverLiveSession is
// defensive: heraReattach only ever arms the banner from the dead-session
// branch, but Draw() itself must never paint the banner over a session that
// is alive, in case some future caller arms it in a stale state.
func TestTerminalPane_Draw_ClosedOutBannerNeverShowsOverLiveSession(t *testing.T) {
	tp := NewTerminalPane()
	tp.SetSession(&mockAdapter{alive: true, output: []byte("live output")})
	tp.ShowClosedOutBanner()

	got := drawOnSim(t, tp, 60, 14)
	if strings.Contains(got, "Task closed out") {
		t.Errorf("banner must never show over a live session, got:\n%s", got)
	}
}

// TestTerminalPane_SetSession_LoadsReplayAfterBindPaneOrder is the fix for
// the live regression reported after add-hera-closeout-banner shipped:
// pressing Enter twice on a closed-out task fell back to the "Session not
// running" placeholder instead of the promised read-only replay.
//
// Root cause: EVERY real caller (bindPane, reconcileOne, the main agent
// view's onTaskSelect) runs SetTaskID -> ResetVT -> SetSession, in that
// order. SetTaskID's own eager loadSessionLog call only fires when the
// CURRENT (about-to-be-replaced) session is nil — but ResetVT, called right
// after, unconditionally wipes tp.replayData anyway, so that eager load was
// always thrown away before SetSession ever ran, on EVERY dead-session bind,
// not just a closed-out one. The fix moves the load into SetSession itself,
// which is the only step with the definitive answer (the session value it
// was just given).
func TestTerminalPane_SetSession_LoadsReplayAfterBindPaneOrder(t *testing.T) {
	setupTaskLog(t, "bindpane-order-fresh", "fresh pane replay\r\n")
	tp := NewTerminalPane()

	tp.SetTaskID("bindpane-order-fresh")
	tp.ResetVT()
	tp.SetSession(nil)

	if len(tp.replayData) == 0 {
		t.Fatal("replayData empty after the real bindPane sequence on a fresh pane")
	}
	testutil.Equal(t, tp.HasContent(), true)
}

// TestTerminalPane_SetSession_LoadsReplayAfterRebindFromLiveSession is the
// sibling case: a pane that was PREVIOUSLY bound to a different, live
// session must still load the new dead task's replay content correctly —
// SetTaskID's own check (`tp.Session() == nil`) reads the OLD session
// (non-nil, live) at the moment it runs, so without the fix this case failed
// even more directly than the fresh-pane case above.
func TestTerminalPane_SetSession_LoadsReplayAfterRebindFromLiveSession(t *testing.T) {
	setupTaskLog(t, "bindpane-order-rebind", "rebind replay\r\n")
	tp := NewTerminalPane()

	tp.SetTaskID("bindpane-order-live")
	tp.ResetVT()
	tp.SetSession(&mockAdapter{alive: true})

	tp.SetTaskID("bindpane-order-rebind")
	tp.ResetVT()
	tp.SetSession(nil)

	if len(tp.replayData) == 0 {
		t.Fatal("replayData empty after rebinding from a pane that previously held a live session")
	}
}

// waitForReplayText polls Draw() until the async replay rebuild completes
// and the expected text appears (asyncReplayRebuild runs on a background
// goroutine — see terminalpane.go), mirroring the deadline-poll pattern
// other replay tests in this package use.
func waitForReplayText(t *testing.T, tp *TerminalPane, w, h int, want string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		got = drawOnSim(t, tp, w, h)
		if strings.Contains(got, want) {
			return got
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("replay text %q never appeared, last screen:\n%s", want, got)
	return ""
}
