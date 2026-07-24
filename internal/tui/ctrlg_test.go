package tui

import (
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/hera"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
)

// seedNeedsInputWorker seeds an orchestrator with a bound coordinator + a
// bound worker, and returns the orchestrator id. The worker's argus task id
// is workerTaskID; its coordinator's is orchName+"-coord".
func seedNeedsInputWorker(t *testing.T, d *db.DB, orchName, workerTaskID string) int64 {
	t.Helper()
	orch := seedHeraOrch(t, d, orchName)
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, orchName+"-coord")
	seedHeraBoundRole(t, d, orch, "wkr", db.HeraKindWorker, workerTaskID)
	return orch
}

// markNeedsInput pushes the given task ids through HeraPage's SetNeedsInput +
// Refresh seam — the same manual seam bug028/bug060's integration tests use
// to bypass the disk-log-scanning detection pipeline, which isn't the concern
// under test here (Ctrl+G's dispatch + jump + cycle mechanics are).
func markNeedsInput(t *testing.T, app *App, taskIDs ...string) {
	t.Helper()
	readUI(t, app.tapp, func() {
		app.heraPage.SetNeedsInput(taskIDs)
		app.heraPage.Refresh()
	})
}

// TestSmoke_CtrlGJumpsFromPlainTaskList drives Ctrl+G through the real
// handleGlobalKey dispatch path from the plain Tasks tab (no Hera, no agent
// view): it must switch to the Hera tab and land directly on the role
// needing input, with no popup and no typing (add-hera-jump-question).
func TestSmoke_CtrlGJumpsFromPlainTaskList(t *testing.T) {
	d := testDB(t)
	seedNeedsInputWorker(t, d, "orch", "tw")
	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()
	markNeedsInput(t, app, "tw")

	readUI(t, app.tapp, func() {
		if app.mode != modeTaskList || app.header.ActiveTab() != widget.TabTasks {
			t.Fatalf("setup: expected modeTaskList/TabTasks, got mode=%v tab=%v", app.mode, app.header.ActiveTab())
		}
	})

	sim.InjectKey(tcell.KeyCtrlG, 0, 0)
	syncUI(t, app.tapp)

	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.header.ActiveTab(), widget.TabHera)
		testutil.Equal(t, app.mode, modeTaskList)
		testutil.Equal(t, app.heraPage.SelectionContext().TaskID(), "tw")
		testutil.Equal(t, app.heraPage.Machine().State(), hera.FocusAgent)
	})
}

// TestSmoke_CtrlGJumpsFromFullscreenAgentView confirms Ctrl+G's reach from
// modeAgent: it tears down the classic agent view (mirroring the switcher's
// own hera-managed landing) before switching into Hera and jumping.
func TestSmoke_CtrlGJumpsFromFullscreenAgentView(t *testing.T) {
	d := testDB(t)
	seedNeedsInputWorker(t, d, "orch", "tw")
	app := New(d, agent.NewRunner(nil), false)
	seedSwitcherTasks(t, app) // gives an unrelated "current" task to view in modeAgent
	sim, stop := wireApp(t, app)
	defer stop()
	markNeedsInput(t, app, "tw")

	readUI(t, app.tapp, func() {
		app.agentState.Reset("ts-cur", "current task")
		app.mode = modeAgent
		app.root.ResizeItem(app.header, 0, 0) // mirrors real agent-view entry (header hidden)
	})

	sim.InjectKey(tcell.KeyCtrlG, 0, 0)
	syncUI(t, app.tapp)

	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.mode, modeTaskList)
		testutil.Equal(t, app.header.ActiveTab(), widget.TabHera)
		testutil.Equal(t, app.heraPage.SelectionContext().TaskID(), "tw")
		_, _, headerW, headerH := app.header.GetRect()
		if headerW == 0 || headerH == 0 {
			t.Errorf("expected the tab header restored after leaving modeAgent, got %dx%d", headerW, headerH)
		}
	})
}

// TestSmoke_CtrlGJumpsFromHeraRailFocus confirms Ctrl+G works while the Hera
// rail itself already holds focus (no tab switch needed, just the jump).
func TestSmoke_CtrlGJumpsFromHeraRailFocus(t *testing.T) {
	d := testDB(t)
	seedNeedsInputWorker(t, d, "orch", "tw")
	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()
	markNeedsInput(t, app, "tw")

	sim.InjectKey(tcell.KeyRune, '2', 0) // Hera tab, rail focus
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.heraPage.Machine().State(), hera.FocusRail)
	})

	sim.InjectKey(tcell.KeyCtrlG, 0, 0)
	syncUI(t, app.tapp)

	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.heraPage.SelectionContext().TaskID(), "tw")
		testutil.Equal(t, app.heraPage.Machine().State(), hera.FocusAgent)
	})
}

// TestSmoke_CtrlGJumpsFromHeraPaneFocus is the pane-focus regression the task
// explicitly calls out: with a live Hera coordinator PANE (not the rail)
// holding keyboard focus, Ctrl+G must still fire the jump — proving the byte
// is intercepted at handleGlobalKey's unconditional dispatch and never leaks
// through to the focused pane's PTY (the same class of guaranteed reach as
// Ctrl+J/Ctrl+K).
func TestSmoke_CtrlGJumpsFromHeraPaneFocus(t *testing.T) {
	d := testDB(t)
	seedNeedsInputWorker(t, d, "orch", "tw")
	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()
	markNeedsInput(t, app, "tw")

	sim.InjectKey(tcell.KeyRune, '2', 0) // Hera tab
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		app.heraPage.Machine().Advance() // rail → coordinator pane
		testutil.Equal(t, app.heraPage.Machine().State(), hera.FocusCoord)
		testutil.Equal(t, app.heraPaneFocused(), true)
	})

	sim.InjectKey(tcell.KeyCtrlG, 0, 0)
	syncUI(t, app.tapp)

	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.heraPage.SelectionContext().TaskID(), "tw")
		testutil.Equal(t, app.heraPage.Machine().State(), hera.FocusAgent)
	})
}

// TestSmoke_CtrlGCyclesThroughMultipleNeedsInputRolesWithoutRepeating drives
// two real Ctrl+G presses end-to-end and confirms they land on two DIFFERENT
// roles in turn (not the same one twice) — the "pressing it again cycles to
// the next (?)" contract, exercised through the actual key dispatch rather
// than the internal/tui/hera package's unit-level Rail tests.
func TestSmoke_CtrlGCyclesThroughMultipleNeedsInputRolesWithoutRepeating(t *testing.T) {
	d := testDB(t)
	orch := seedHeraOrch(t, d, "orch")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	seedHeraBoundRole(t, d, orch, "wkr-a", db.HeraKindWorker, "ta")
	seedHeraBoundRole(t, d, orch, "wkr-b", db.HeraKindWorker, "tb")
	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()
	markNeedsInput(t, app, "ta", "tb")

	sim.InjectKey(tcell.KeyCtrlG, 0, 0)
	syncUI(t, app.tapp)
	var first string
	readUI(t, app.tapp, func() { first = app.heraPage.SelectionContext().TaskID() })
	if first != "ta" && first != "tb" {
		t.Fatalf("expected the first jump to land on ta or tb, got %q", first)
	}

	sim.InjectKey(tcell.KeyCtrlG, 0, 0)
	syncUI(t, app.tapp)
	var second string
	readUI(t, app.tapp, func() { second = app.heraPage.SelectionContext().TaskID() })
	if second != "ta" && second != "tb" {
		t.Fatalf("expected the second jump to land on ta or tb, got %q", second)
	}
	if second == first {
		t.Fatalf("expected the second Ctrl+G press to advance to the OTHER needs-input role, got %q both times", first)
	}

	// A third press wraps back around to the first.
	sim.InjectKey(tcell.KeyCtrlG, 0, 0)
	syncUI(t, app.tapp)
	var third string
	readUI(t, app.tapp, func() { third = app.heraPage.SelectionContext().TaskID() })
	testutil.Equal(t, third, first)
}

// TestSmoke_CtrlGNoopFlashesNoticeWhenNothingNeedsInput confirms the safe
// no-op path: with no role needing input, Ctrl+G doesn't crash and flashes a
// notice rather than silently doing nothing.
func TestSmoke_CtrlGNoopFlashesNoticeWhenNothingNeedsInput(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()

	sim.InjectKey(tcell.KeyCtrlG, 0, 0)
	syncUI(t, app.tapp)

	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.header.Notice(), "No role needs input")
	})
}

// TestSmoke_CtrlGNoopFromAgentViewDoesNotTearDown pins a bug caught in code
// review: an earlier version of jumpToNextNeedsInput tore down the fullscreen
// agent view and switched to the Hera tab BEFORE learning there was no
// needs-input candidate at all, yanking the operator out of their agent view
// for a pure no-op. This confirms the fix peeks first (the non-mutating
// Rail.NextNeedsInputTaskID) and does nothing else when nothing qualifies:
// the session stays attached, the tab never switches, and the header (hidden
// by the agent view's zen layout) stays hidden.
func TestSmoke_CtrlGNoopFromAgentViewDoesNotTearDown(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	seedSwitcherTasks(t, app)
	sim, stop := wireApp(t, app)
	defer stop()

	curTask, err := d.Get("ts-cur")
	testutil.NoError(t, err)
	curTask.Backend = "test"
	cfg := config.DefaultConfig()
	cfg.Backends["test"] = config.Backend{Command: "sleep 30"}
	sess, err := runner.Start(curTask, cfg, 24, 80, false)
	testutil.NoError(t, err)
	defer runner.Stop(curTask.ID) //nolint:errcheck

	readUI(t, app.tapp, func() {
		app.mode = modeAgent
		app.agentState.Reset(curTask.ID, curTask.Name)
		app.agentPane.SetSession(sess)
		app.worktreeDir = curTask.Worktree
		app.root.ResizeItem(app.header, 0, 0) // mirrors real agent-view entry (header hidden)
	})

	sim.InjectKey(tcell.KeyCtrlG, 0, 0)
	syncUI(t, app.tapp)

	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.mode, modeAgent)
		testutil.Equal(t, app.header.ActiveTab(), widget.TabTasks) // never switched to Hera
		testutil.Equal(t, app.header.Notice(), "No role needs input")
		if app.agentPane.Session() == nil {
			t.Fatal("expected the agent session to remain attached — no teardown should have run")
		}
		_, _, headerW, headerH := app.header.GetRect()
		if headerW != 0 && headerH != 0 {
			t.Errorf("expected the tab header to stay hidden (still in modeAgent, no teardown), got %dx%d", headerW, headerH)
		}
	})
}

// --- add-ctrlg-excursion: ctrl+g count==0 restore + ctrl+b manual restore ---

// TestSmoke_CtrlGRestoresRailWhenClear drives the count==0 restore branch
// end-to-end: an excursion snapshot armed while a role needed input survives
// the problem resolving on its own (no auto-discharge), and the NEXT ctrl+g
// press — now with nothing left to jump to — restores it instead of just
// flashing the old "No role needs input" notice.
func TestSmoke_CtrlGRestoresRailWhenClear(t *testing.T) {
	d := testDB(t)
	seedNeedsInputWorker(t, d, "orch", "tw")
	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()
	markNeedsInput(t, app, "tw") // 0 -> 1 transition: arms an excursion snapshot

	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.heraPage.Rail().HasExcursionSnapshot(), true)
	})

	markNeedsInput(t, app) // the problem resolves on its own (count back to 0)

	sim.InjectKey(tcell.KeyCtrlG, 0, 0)
	syncUI(t, app.tapp)

	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.header.Notice(), "Rail restored")
		testutil.Equal(t, app.heraPage.Rail().HasExcursionSnapshot(), false)
		// A restore is a background rail-state fix, not a "come look at this"
		// jump — it must never switch tabs or leave the plain task list.
		testutil.Equal(t, app.header.ActiveTab(), widget.TabTasks)
		testutil.Equal(t, app.mode, modeTaskList)
	})
}

// TestSmoke_CtrlBRestoresRailManually confirms ctrl+b works at any time
// regardless of the remaining needs-input count (unlike ctrl+g, which only
// restores once the count is back to 0) and — like the count==0 restore
// above — never tears down a live fullscreen agent view or switches tabs. A
// subsequent ctrl+g still reaches the still-outstanding problem afterward: a
// manual restore discharges only the fold snapshot, never the candidate ring.
func TestSmoke_CtrlBRestoresRailManually(t *testing.T) {
	d := testDB(t)
	seedNeedsInputWorker(t, d, "orch", "tw")
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	seedSwitcherTasks(t, app) // gives an unrelated "current" task to view in modeAgent
	sim, stop := wireApp(t, app)
	defer stop()
	markNeedsInput(t, app, "tw") // arms an excursion; the role still needs input (count stays >=1)

	curTask, err := d.Get("ts-cur")
	testutil.NoError(t, err)
	curTask.Backend = "test"
	cfg := config.DefaultConfig()
	cfg.Backends["test"] = config.Backend{Command: "sleep 30"}
	sess, err := runner.Start(curTask, cfg, 24, 80, false)
	testutil.NoError(t, err)
	defer runner.Stop(curTask.ID) //nolint:errcheck

	readUI(t, app.tapp, func() {
		app.mode = modeAgent
		app.agentState.Reset(curTask.ID, curTask.Name)
		app.agentPane.SetSession(sess)
		app.worktreeDir = curTask.Worktree
		app.root.ResizeItem(app.header, 0, 0) // mirrors real agent-view entry (header hidden)
	})

	sim.InjectKey(tcell.KeyCtrlB, 0, 0)
	syncUI(t, app.tapp)

	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.header.Notice(), "Rail restored")
		testutil.Equal(t, app.heraPage.Rail().HasExcursionSnapshot(), false)
		testutil.Equal(t, app.mode, modeAgent) // never torn down
		if app.agentPane.Session() == nil {
			t.Fatal("expected the agent session to remain attached — ctrl+b must not touch the agent view")
		}
		_, _, headerW, headerH := app.header.GetRect()
		if headerW != 0 && headerH != 0 {
			t.Errorf("expected the tab header to stay hidden (still in modeAgent), got %dx%d", headerW, headerH)
		}
	})

	// The underlying problem is still outstanding — ctrl+g must still reach it.
	sim.InjectKey(tcell.KeyCtrlG, 0, 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.heraPage.SelectionContext().TaskID(), "tw")
	})
}

// TestSmoke_CtrlBNoopWhenNothingHeld confirms ctrl+b is a silent no-op — no
// flash, no navigation — when no excursion snapshot is currently held (never
// opened, or already discharged).
func TestSmoke_CtrlBNoopWhenNothingHeld(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()

	sim.InjectKey(tcell.KeyCtrlB, 0, 0)
	syncUI(t, app.tapp)

	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.header.Notice(), "")
		testutil.Equal(t, app.mode, modeTaskList)
		testutil.Equal(t, app.header.ActiveTab(), widget.TabTasks)
	})
}
