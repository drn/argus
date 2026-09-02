package hera

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/terminal"
)

// BUG-082 end to end through the Hera split view.
//
// A pane whose session handle is dead while the agent process is still running
// (BUG-013: the daemon tore the stream down on a StreamLost relay or a bounce)
// renders exclusively through the replay path, because Draw only follows the
// live path for an ALIVE session. renderLive is the sole creator of the live
// emulator, so that pane never gets one — and the alternate-screen scroll guard
// used to read only the live emulator. Wheel-up on such a pane therefore
// entered scroll mode against a full-screen agent's recording, whose maxScroll
// is 0, and the next paint clamped the offset back to the tail: one notch up,
// snap to the bottom, on every notch, until the TUI was restarted (which
// re-dials a live stream and restores the live emulator).
//
// This is the split-view-specific exposure Aaron reported: the classic agent
// view is only ever entered on a task the user attached to (fresh live handle),
// whereas Hera panes rebind on every rail hop and keep whatever handle the
// runner hands back.

// seedAltScreenLog writes a full-screen agent's recording (enter the alternate
// screen once, then repaint in place forever, never emitting a line feed) to
// taskID's session log under a redirected HOME. Such a recording carries no
// linear scrollback at all, so its replay build's maxScroll is always 0.
func seedAltScreenLog(t *testing.T, taskID string) []byte {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".argus", "sessions")
	testutil.NoError(t, os.MkdirAll(dir, 0o755))

	var b strings.Builder
	b.WriteString("\x1b[?1049h\x1b[2J\x1b[H")
	for frame := range 40 {
		for row := 1; row <= 24; row++ {
			fmt.Fprintf(&b, "\x1b[%d;1Hframe %d row %d", row, frame, row)
		}
	}
	raw := []byte(b.String())
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, taskID+".log"), raw, 0o600))
	return raw
}

// replayPathPage wires a Hera page whose selected worker renders through the
// replay path, in either of the two ways that happens:
//
//   - deadHandle: the resolver returns a handle that is present but no longer
//     alive (the BUG-013 state — the daemon tore the stream down while the
//     agent process runs on). This is the exposure Aaron hit: the pane looks
//     live to the operator but is !Alive() to Draw.
//   - no handle at all: the resolver misses entirely (a finished worker the
//     rail cursor lands on). This second exposure only became REACHABLE once
//     fix-closeout-replay-load-order moved the eager loadSessionLog into
//     SetSession — before that, ResetVT wiped the replayData SetTaskID had
//     just loaded, HasContent() stayed false, and Draw short-circuited to the
//     "Session not running" placeholder without ever replaying.
func replayPathPage(t *testing.T, taskID string, deadHandle bool) (*HeraPage, *terminal.TerminalPane, tcell.SimulationScreen) {
	t.Helper()
	raw := seedAltScreenLog(t, taskID)

	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, taskID)

	sessions := map[string]*fakeSession{}
	if deadHandle {
		sessions[taskID] = &fakeSession{id: taskID, alive: false, output: raw}
	}

	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(sessions))
	p.Refresh()
	testutil.Equal(t, selectRoleByName(p, "wkr"), true)

	pane := p.AgentPane()
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	sim.SetSize(160, 40)
	p.SetRect(0, 0, 160, 40)

	// Draw until the background replay rebuild has landed — the steady state
	// the operator is looking at before they reach for the wheel. Readiness is
	// judged by the recording actually being ON SCREEN, deliberately NOT by
	// InAltScreen(): that predicate is the thing under test, and gating the
	// setup on it would let a trivially-always-true implementation pass.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		p.Draw(sim)
		if strings.Contains(simScreenText(sim), "frame 39 row 1") {
			return p, pane, sim
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("pane never painted its reconstructed recording")
	return nil, nil, nil
}

func TestPanes_BUG082_ReplayPaneWheelDoesNotSnapBack(t *testing.T) {
	for _, tc := range []struct {
		name       string
		deadHandle bool
	}{
		{"dead session handle", true},
		{"no session handle", false},
	} {
		t.Run(tc.name, func(t *testing.T) { assertNoWheelSnapBack(t, tc.deadHandle) })
	}
}

func assertNoWheelSnapBack(t *testing.T, deadHandle bool) {
	t.Helper()
	p, pane, sim := replayPathPage(t, "t-replay", deadHandle)

	// Three mouse-wheel notches over the agent region, each followed by the
	// redraw that used to clamp the offset straight back to 0.
	wheelX := p.agentX + 2
	for notch := 1; notch <= 3; notch++ {
		ev := tcell.NewEventMouse(wheelX, 10, tcell.WheelUp, tcell.ModNone)
		p.MouseHandler()(tview.MouseScrollUp, ev, func(tview.Primitive) {})
		if got := pane.ScrollOffset(); got != 0 {
			t.Fatalf("notch %d: wheel entered scroll mode (offset=%d) on a recording with no "+
				"scrollback — the next paint clamps it back to 0, which is the visible snap-back", notch, got)
		}
		p.Draw(sim)
		testutil.Equal(t, pane.ScrollOffset(), 0)
	}
}

func TestPanes_BUG082_ReplayPanePgUpSurfacesAccurateHint(t *testing.T) {
	p, pane, _ := replayPathPage(t, "t-replay", true)
	var info string
	p.OnInfo = func(msg string) { info = msg }

	p.forwardKey(pane, tcell.NewEventKey(tcell.KeyPgUp, 0, tcell.ModNone))

	testutil.Equal(t, pane.ScrollOffset(), 0)
	if info == "" {
		t.Fatal("PgUp on a pane with no scrollback surfaced no affordance")
	}
	if strings.Contains(info, "scroll within the agent") {
		t.Errorf("hint %q points at an agent this pane cannot reach — its session handle is dead", info)
	}
}
