package tui

import (
	"os"
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
)

// blockedSessionLog is a session-log tail that trips agent.DetectNeedsInput via
// its numbered-selection signature (a Claude permission prompt renders ❯ 1.).
const blockedSessionLog = "Do you want to proceed?\r\n❯ 1. Yes\r\n  2. No\r\n"

func forceDraw(t *testing.T, app *App) {
	t.Helper()
	drawn := make(chan struct{})
	app.tapp.QueueUpdateDraw(func() { close(drawn) })
	<-drawn
	syncUI(t, app.tapp)
}

// seedBlockedWorkerOrch seeds an orchestrator (optionally with a coordinator) +
// one worker bound to an in_progress task, writes the worker's on-disk session
// log with the permission-prompt marker, and returns the orchestrator + worker
// task ID.
func seedBlockedWorkerOrch(t *testing.T, d *db.DB, withCoord bool) (orchID int64, wkrTask string) {
	t.Helper()
	o, err := d.CreateHeraOrchestrator("orch", "")
	testutil.NoError(t, err)
	seed := func(name string, kind db.HeraRoleKind, task string) {
		role, rerr := d.CreateHeraRole(db.CreateHeraRoleInput{
			OrchestratorID: o.ID, Name: name, Kind: kind, ArgusProject: "p",
		})
		testutil.NoError(t, rerr)
		testutil.NoError(t, d.Add(&model.Task{ID: task, Name: name, Status: model.StatusInProgress, Project: "p"}))
		_, berr := d.CreateHeraBinding(db.CreateHeraBindingInput{
			RoleID: role.ID, ArgusTaskID: task, WorktreePath: "/wt/" + task,
		})
		testutil.NoError(t, berr)
	}
	if withCoord {
		seed("coord", db.HeraKindCoordinator, "coord-task")
	}
	wkrTask = "wkr-task"
	seed("wkr", db.HeraKindWorker, wkrTask)

	// The TUI's detector reads the disk log, not the ring (daemon-client mode).
	testutil.NoError(t, os.MkdirAll(agent.SessionsDir(), 0o755))
	testutil.NoError(t, os.WriteFile(agent.SessionLogPath(wkrTask), []byte(blockedSessionLog), 0o644))
	return o.ID, wkrTask
}

// driveNeedsInput runs the real needs-input pipeline on the UI thread exactly as
// the app tick does: detect (disk-log scan) → filter to in_progress → feed the
// hera page → rebuild.
func driveNeedsInput(t *testing.T, app *App, wkrTask string) {
	t.Helper()
	readUI(t, app.tapp, func() {
		app.needsInputIDs = app.detectNeedsInputSticky([]string{wkrTask}, []string{wkrTask}, nil)
		if len(app.needsInputIDs) == 0 {
			t.Fatalf("detection failed: %s not flagged needs-input from disk log", wkrTask)
		}
		workers, coordinators := app.readHeraRoles()
		heraManaged := mergeManagedFromMeta(workers, coordinators)
		app.heraPage.SetNeedsInput(needsInputForHeraRail(app.needsInputIDs, app.tasks, heraManaged))
		app.heraPage.Refresh()
	})
	forceDraw(t, app)
}

// TestBUG028_Integration_HeraRailShowsNeedsInputForBlockedWorker exercises the
// full end-to-end render path for the realistic orchestrator shape (coordinator
// + worker). The needs-input "(?)" glyph must appear somewhere on screen in the
// COLLAPSED default view AND on the worker's own row once expanded. Since
// remove-needs-input-rollup-glyph, the collapsed-view glyph is supplied by the
// worker's OWN row, peeked through the closed coordinator fold via the
// partial-fold-reveal mechanism — NOT by the coordinator header, which now shows
// only its own signal. The test guards against regression of the wiring +
// reveal + render chain (the header no longer carries this signal at all).
func TestBUG028_Integration_HeraRailShowsNeedsInputForBlockedWorker(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	_, wkrTask := seedBlockedWorkerOrch(t, d, true /* withCoord */)

	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.refreshTasks()

	sim, stop := wireApp(t, app)
	defer stop()
	sim.InjectKey(tcell.KeyRune, '2', 0)
	syncUI(t, app.tapp)

	driveNeedsInput(t, app, wkrTask)

	if app.header.ActiveTab() != widget.TabHera {
		t.Fatalf("setup: expected TabHera, got %v", app.header.ActiveTab())
	}
	// (1) Default collapsed view: the reveal mechanism peeks the blocked worker's
	// own row through the closed coordinator fold — the header itself carries no
	// rollup anymore (remove-needs-input-rollup-glyph).
	if !screenHasRune(sim, theme.IconNeedsInput) {
		t.Errorf("collapsed: Hera rail did not surface needs-input glyph %q via the revealed worker row", theme.IconNeedsInput)
	}
	// (2) Expanded view: the worker's own row renders the glyph.
	readUI(t, app.tapp, func() { app.heraPage.Rail().ToggleCollapse() })
	forceDraw(t, app)
	if !screenHasRune(sim, theme.IconNeedsInput) {
		t.Errorf("expanded: Hera rail worker row did not render needs-input glyph %q", theme.IconNeedsInput)
	}
}

// TestBUG028_Integration_CoordinatorlessHeaderSurfacesNeedsInput is the BUG-028
// render-seam test, updated for remove-needs-input-rollup-glyph: a COLLAPSED,
// coordinator-less orchestrator (its coordinator role nuked, say) has no
// coordinator glyph and, since this change, no header-level rollup fallback
// either — but the blocked worker's OWN row is still rendered, peeked through
// the closed fold via the partial-fold-reveal mechanism (gated on
// OrchView.SubtreeNeedsInput, which does not require a coordinator role to be
// present). So the glyph still appears on screen, now supplied entirely by the
// revealed worker row rather than the header.
func TestBUG028_Integration_CoordinatorlessHeaderSurfacesNeedsInput(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	_, wkrTask := seedBlockedWorkerOrch(t, d, false /* no coordinator */)

	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.refreshTasks()

	sim, stop := wireApp(t, app)
	defer stop()
	sim.InjectKey(tcell.KeyRune, '2', 0)
	syncUI(t, app.tapp)

	driveNeedsInput(t, app, wkrTask)

	// The orchestrator stays COLLAPSED (default) — the header renders no glyph at
	// all (no coordinator, and the fallback is removed); the reveal mechanism
	// still surfaces the blocked worker's own row underneath.
	if !screenHasRune(sim, theme.IconNeedsInput) {
		t.Errorf("collapsed coordinator-less orchestrator did not surface needs-input glyph %q via the revealed worker row", theme.IconNeedsInput)
	}
}

// TestBUG028_Integration_BlockedCoordinatorCompleteTaskSurfaces is the live
// bug-bash repro: a COORDINATOR whose bound task is `complete` (coordinators
// finish their task status early) but whose session is alive and blocked on a
// user prompt must surface "(?)" on its collapsed header. Both the app.go feed
// (needsInputForHeraRail admits coordinators regardless of status) and the model
// gate (non-workers surface regardless of task status) are exercised end to end.
func TestBUG028_Integration_BlockedCoordinatorCompleteTaskSurfaces(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	o, err := d.CreateHeraOrchestrator("orch", "")
	testutil.NoError(t, err)
	const coordTask = "coord-task"
	role, err := d.CreateHeraRole(db.CreateHeraRoleInput{
		OrchestratorID: o.ID, Name: "coord", Kind: db.HeraKindCoordinator, ArgusProject: "p",
	})
	testutil.NoError(t, err)
	testutil.NoError(t, d.Add(&model.Task{ID: coordTask, Name: "coord", Status: model.StatusComplete, Project: "p"}))
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{
		RoleID: role.ID, ArgusTaskID: coordTask, WorktreePath: "/wt/" + coordTask,
	})
	testutil.NoError(t, err)
	// Mark the coordinator a hera coordinator in task_meta so readHeraRoles admits
	// it through the app feed (needsInputForHeraRail), and write its blocked log.
	testutil.NoError(t, d.SetMeta(coordTask, db.HeraMetaNamespace, db.HeraMetaKeyRole, string(db.HeraKindCoordinator)))
	testutil.NoError(t, os.MkdirAll(agent.SessionsDir(), 0o755))
	testutil.NoError(t, os.WriteFile(agent.SessionLogPath(coordTask), []byte(blockedSessionLog), 0o644))

	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.refreshTasks()

	sim, stop := wireApp(t, app)
	defer stop()
	sim.InjectKey(tcell.KeyRune, '2', 0)
	syncUI(t, app.tapp)

	driveNeedsInput(t, app, coordTask)

	// Collapsed coordinator header must surface "(?)" even though its task is complete.
	if !screenHasRune(sim, theme.IconNeedsInput) {
		t.Errorf("BUG-028: collapsed coordinator header did not surface needs-input glyph %q while its task is complete", theme.IconNeedsInput)
	}
}

// TestBUGA_Integration_LiveInReviewWorkerAtPromptSurfaces is the BUG-A repro at
// the render seam: a WORKER whose task has rolled to in_review with
// meta:hera.ready_to_close=true (the #707 close-out state RollHeraWorkerToReview
// stamps) but whose session is ALIVE and blocked on a prompt must surface "(?)"
// on its row — NOT the ready_to_close review glyph. Exercises all three fix
// layers end to end: the app.go feed admits the in_review worker
// (needsInputForHeraRail / heraManaged), the model gate surfaces it (rv.Live),
// and the shared status-icon classifier ranks needs-input above ready_to_close.
func TestBUGA_Integration_LiveInReviewWorkerAtPromptSurfaces(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	o, err := d.CreateHeraOrchestrator("orch", "")
	testutil.NoError(t, err)
	const wkrTask = "wkr-task"
	role, err := d.CreateHeraRole(db.CreateHeraRoleInput{
		OrchestratorID: o.ID, Name: "wkr", Kind: db.HeraKindWorker, ArgusProject: "p",
	})
	testutil.NoError(t, err)
	// in_review + a still-LIVE binding (RollHeraWorkerToReview never ends it, #707).
	testutil.NoError(t, d.Add(&model.Task{ID: wkrTask, Name: "wkr", Status: model.StatusInReview, Project: "p"}))
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{
		RoleID: role.ID, ArgusTaskID: wkrTask, WorktreePath: "/wt/" + wkrTask,
	})
	testutil.NoError(t, err)
	// Hera worker meta + the ready_to_close stamp the done-roll sets, plus a blocked
	// session log (a permission prompt the worker raised AFTER reporting done).
	testutil.NoError(t, d.SetMeta(wkrTask, db.HeraMetaNamespace, db.HeraMetaKeyRole, string(db.HeraKindWorker)))
	testutil.NoError(t, d.SetMeta(wkrTask, db.HeraMetaNamespace, db.HeraMetaKeyReadyToClose, "true"))
	testutil.NoError(t, os.MkdirAll(agent.SessionsDir(), 0o755))
	testutil.NoError(t, os.WriteFile(agent.SessionLogPath(wkrTask), []byte(blockedSessionLog), 0o644))

	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.refreshTasks()

	sim, stop := wireApp(t, app)
	defer stop()
	sim.InjectKey(tcell.KeyRune, '2', 0)
	syncUI(t, app.tapp)

	driveNeedsInput(t, app, wkrTask)

	// Collapsed coordinator-less orchestrator: the reveal mechanism peeks the
	// live worker's own row through the closed fold (the header itself carries
	// no rollup, per remove-needs-input-rollup-glyph).
	if !screenHasRune(sim, theme.IconNeedsInput) {
		t.Errorf("BUG-A: live in_review worker at a prompt did not surface needs-input glyph %q (ready_to_close masked it?)", theme.IconNeedsInput)
	}
}
