package tui

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/modal"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
)

// heraTabCursorOnWorker switches to the Hera tab and moves the cursor onto the
// worker role. After the coordinator fold the rows are orch header=0 (the
// coordinator) and worker=1, so a single Down lands on the worker.
// On first run the orchestrator starts collapsed — this helper expands it first.
func heraTabCursorOnWorker(t *testing.T, app *App, sim tcell.SimulationScreen) {
	t.Helper()
	sim.InjectKey(tcell.KeyRune, '2', 0)
	syncUI(t, app.tapp)
	// Expand the orchestrator if it started collapsed (first-run default).
	// After the expand the header is still row 0, and the single worker is row 1.
	var collapsed bool
	readUI(t, app.tapp, func() {
		if o := app.heraPage.Rail().SelectedOrch(); o != nil {
			collapsed = app.heraPage.Rail().OrchCollapsed(o.ID)
		}
	})
	if collapsed {
		sim.InjectKey(tcell.KeyRune, ' ', 0) // expand
		syncUI(t, app.tapp)
	}
	sim.InjectKey(tcell.KeyRune, 'j', 0) // → worker (coord folded into header)
	syncUI(t, app.tapp)
}

func TestSmoke_HeraPinKeyTogglesStore(t *testing.T) {
	d := testDB(t)
	orch := seedHeraOrch(t, d, "orch")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	role := seedHeraBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "tw")

	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()
	heraTabCursorOnWorker(t, app, sim)

	sim.InjectKey(tcell.KeyRune, 'P', 0) // pin
	syncUI(t, app.tapp)
	got, _ := d.HeraRole(role.ID)
	testutil.Equal(t, got.PinnedAt != nil, true)
}

func TestSmoke_HeraStatusKeyAdvances(t *testing.T) {
	d := testDB(t)
	orch := seedHeraOrch(t, d, "orch")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	role := seedHeraBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "tw")

	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()
	heraTabCursorOnWorker(t, app, sim)

	sim.InjectKey(tcell.KeyRune, 's', 0) // advance idle→working
	syncUI(t, app.tapp)
	st, err := d.HeraRoleStatusFor(role.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, st.Status, db.HeraStatusWorking)
}

// TestSmoke_HeraHideNoConfirm pins BUG-022: `a` HIDE on a live worker is
// IMMEDIATE (no confirm modal) and archives the role as Tier-1 (NOT nuked), so it
// stays reversible. (The reversible un-hide toggle is unit-tested in
// TestHeraActions_HideBranches.)
func TestSmoke_HeraHideNoConfirm(t *testing.T) {
	d := testDB(t)
	orch := seedHeraOrch(t, d, "orch")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	role := seedHeraBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "tw")

	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()
	heraTabCursorOnWorker(t, app, sim)

	// `a` on a live worker hides it IMMEDIATELY — no confirm modal.
	sim.InjectKey(tcell.KeyRune, 'a', 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { testutil.Equal(t, app.mode, modeTaskList) })
	got, _ := d.HeraRole(role.ID)
	testutil.Equal(t, got.ArchivedAt != nil, true) // hidden (archived)
	testutil.Equal(t, got.NukedAt == nil, true)    // NOT nuked — reversible
}

func TestSmoke_HeraRenameModalInputAndPaste(t *testing.T) {
	d := testDB(t)
	orch := seedHeraOrch(t, d, "orch")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	role := seedHeraBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "tw")

	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()
	heraTabCursorOnWorker(t, app, sim)

	// `r` opens the input modal.
	sim.InjectKey(tcell.KeyRune, 'r', 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { testutil.Equal(t, app.mode, modeHeraInput) })

	// Clear the pre-filled name, then PASTE a new one (exercises PasteHandler).
	sim.InjectKey(tcell.KeyCtrlU, 0, 0) // delete-to-start
	syncUI(t, app.tapp)
	testutil.NoError(t, sim.PostEvent(tcell.NewEventPaste(true)))
	sim.InjectKey(tcell.KeyRune, 'n', 0)
	sim.InjectKey(tcell.KeyRune, 'u', 0)
	testutil.NoError(t, sim.PostEvent(tcell.NewEventPaste(false)))
	syncUI(t, app.tapp)
	sim.InjectKey(tcell.KeyEnter, 0, 0) // submit
	syncUI(t, app.tapp)

	got, _ := d.HeraRole(role.ID)
	testutil.Equal(t, got.Name, "nu")
	readUI(t, app.tapp, func() { testutil.Equal(t, app.mode, modeTaskList) })
}

func TestSmoke_HeraDeleteRoleConfirmCancelKeepsIt(t *testing.T) {
	d := testDB(t)
	orch := seedHeraOrch(t, d, "orch")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	role := seedHeraBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "tw")

	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()
	heraTabCursorOnWorker(t, app, sim)

	sim.InjectKey(tcell.KeyCtrlD, 0, 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { testutil.Equal(t, app.mode, modeHeraConfirm) })

	sim.InjectKey(tcell.KeyEscape, 0, 0) // cancel
	syncUI(t, app.tapp)
	_, err := d.HeraRole(role.ID)
	testutil.NoError(t, err) // still present
}

// TestSmoke_HeraDeleteOrchestratorCascadesReclaimsTask pins BUG-022: Ctrl+D on an
// orchestrator HEADER runs the full-subtree NUKE, which marks the orchestrator +
// role rows NUKED (NO hard deletes) and reclaims the sole-bound task's worktree
// (archiving the task row). Nothing is removed from the DB.
func TestSmoke_HeraDeleteOrchestratorCascadesReclaimsTask(t *testing.T) {
	d := testDB(t)
	t.Setenv("HOME", t.TempDir())
	orch := seedHeraOrch(t, d, "orch")
	role := seedHeraBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "tw")

	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()
	// Cursor on the orchestrator header (row 0).
	sim.InjectKey(tcell.KeyRune, '2', 0)
	syncUI(t, app.tapp)

	sim.InjectKey(tcell.KeyCtrlD, 0, 0)
	syncUI(t, app.tapp)
	sim.InjectKey(tcell.KeyRune, 'y', 0) // confirm
	syncUI(t, app.tapp)

	// Orchestrator + role NUKED (still present), not hard-deleted.
	gotOrch, err := d.HeraOrchestrator(orch)
	testutil.NoError(t, err)
	testutil.Equal(t, gotOrch.NukedAt != nil, true)
	gotRole, err := d.HeraRole(role.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, gotRole.NukedAt != nil, true)
	// Argus task ARCHIVED (sole-bound → worktree reclaimed, row kept).
	task, err := d.Get("tw")
	testutil.NoError(t, err)
	testutil.Equal(t, task.Archived, true)
}

// TestSmoke_HeraDeleteRoleMultiBindingIsolation is the locked must-have:
// deleting role R in orchestrator A leaves the SAME task's role in orchestrator
// B live (the task is preserved because it is bound elsewhere).
func TestSmoke_HeraDeleteRoleMultiBindingIsolation(t *testing.T) {
	d := testDB(t)
	orchA := seedHeraOrch(t, d, "orch-a")
	orchB := seedHeraOrch(t, d, "orch-b")
	const shared = "shared"
	// Both bindings are WORKER-kind (neither orchestrator's coordinator), so the
	// shared task is NOT a bridge — no nesting — and Ctrl+D on orch-a's worker row
	// is the conservative single-role delete (the cascade only fires on a bridging
	// row). The shared task is preserved because it stays bound under orch-b.
	rA, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: orchA, Name: "wkr", Kind: db.HeraKindWorker, ArgusProject: "p"})
	testutil.NoError(t, err)
	rB, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: orchB, Name: "wkr-b", Kind: db.HeraKindWorker, ArgusProject: "p"})
	testutil.NoError(t, err)
	testutil.NoError(t, d.Add(&model.Task{ID: shared, Name: shared, Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}))
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: rA.ID, ArgusTaskID: shared, WorktreePath: "/a"})
	testutil.NoError(t, err)
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: rB.ID, ArgusTaskID: shared, WorktreePath: "/b"})
	testutil.NoError(t, err)

	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()
	sim.InjectKey(tcell.KeyRune, '2', 0)
	syncUI(t, app.tapp)
	// Expand orch-a if it started collapsed (first-run default).
	var collapsed bool
	readUI(t, app.tapp, func() {
		if o := app.heraPage.Rail().SelectedOrch(); o != nil {
			collapsed = app.heraPage.Rail().OrchCollapsed(o.ID)
		}
	})
	if collapsed {
		sim.InjectKey(tcell.KeyRune, ' ', 0) // expand orch-a
		syncUI(t, app.tapp)
	}
	// orch-a is first (alpha order); its worker role is row 1.
	sim.InjectKey(tcell.KeyRune, 'j', 0)
	syncUI(t, app.tapp)

	sim.InjectKey(tcell.KeyCtrlD, 0, 0)
	syncUI(t, app.tapp)
	sim.InjectKey(tcell.KeyRune, 'y', 0) // confirm
	syncUI(t, app.tapp)

	// Role in A NUKED (not hard-deleted) + binding ended; role in B (same task)
	// fully intact; multi-bound task preserved (not archived — bound under B).
	gotA, err := d.HeraRole(rA.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, gotA.NukedAt != nil, true)
	_, err = d.HeraLiveBindingByRole(rA.ID)
	testutil.ErrorIs(t, err, db.ErrHeraNotFound)
	gotB, err := d.HeraRole(rB.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, gotB.OrchestratorID, orchB)
	testutil.Equal(t, gotB.NukedAt == nil, true)
	gotTask, err := d.Get(shared)
	testutil.NoError(t, err)
	testutil.Equal(t, gotTask.Archived, false)
}

// TestSmoke_HeraCascadeDeleteSubtree drives Ctrl+D on a bridging worker row: the
// confirm modal warns about the cascade and, on confirm, the nested child
// orchestrator + its roles are NUKED and its sole-bound agent task ARCHIVED (no
// hard deletes, worktree reclaimed) while the multi-bound bridge task is preserved.
func TestSmoke_HeraCascadeDeleteSubtree(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	parent := seedHeraOrch(t, d, "orch-a") // alpha-first → root
	child := seedHeraOrch(t, d, "orch-c")
	const shared = "shared" // worker in parent AND coordinator of child (the bridge)
	const childWorkerTask = "twc"

	pWorker, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: parent, Name: "w", Kind: db.HeraKindWorker, ArgusProject: "p"})
	testutil.NoError(t, err)
	cCoord, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: child, Name: "coord", Kind: db.HeraKindCoordinator, ArgusProject: "p"})
	testutil.NoError(t, err)
	cWorker, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: child, Name: "wc", Kind: db.HeraKindWorker, ArgusProject: "p"})
	testutil.NoError(t, err)
	testutil.NoError(t, d.Add(&model.Task{ID: shared, Name: shared, Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}))
	testutil.NoError(t, d.Add(&model.Task{ID: childWorkerTask, Name: childWorkerTask, Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}))
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: pWorker.ID, ArgusTaskID: shared, WorktreePath: "/p"})
	testutil.NoError(t, err)
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: cCoord.ID, ArgusTaskID: shared, WorktreePath: "/c"})
	testutil.NoError(t, err)
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: cWorker.ID, ArgusTaskID: childWorkerTask, WorktreePath: "/cw"})
	testutil.NoError(t, err)

	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()
	sim.InjectKey(tcell.KeyRune, '2', 0)
	syncUI(t, app.tapp)
	// Expand orch-a if collapsed (first-run default) so its bridge worker is visible.
	var collapsed bool
	readUI(t, app.tapp, func() {
		if o := app.heraPage.Rail().SelectedOrch(); o != nil {
			collapsed = app.heraPage.Rail().OrchCollapsed(o.ID)
		}
	})
	if collapsed {
		sim.InjectKey(tcell.KeyRune, ' ', 0)
		syncUI(t, app.tapp)
	}
	// orch-c nests under orch-a's "w" row (row 1, the bridge).
	sim.InjectKey(tcell.KeyRune, 'j', 0)
	syncUI(t, app.tapp)

	sim.InjectKey(tcell.KeyCtrlD, 0, 0)
	syncUI(t, app.tapp)
	// The confirm modal must spell out the cascade (remove from rail, reclaim worktrees).
	readUI(t, app.tapp, func() {
		testutil.Contains(t, app.heraConfirmModal.Message(), "removes")
		testutil.Contains(t, app.heraConfirmModal.Message(), "reclaims")
	})
	sim.InjectKey(tcell.KeyRune, 'y', 0) // confirm
	syncUI(t, app.tapp)

	// Child orchestrator NUKED (still present), not hard-deleted; its sole-bound
	// worker task archived (worktree reclaimed); the multi-bound bridge task
	// preserved (still bound under the parent — not archived).
	gotChild, err := d.HeraOrchestrator(child)
	testutil.NoError(t, err)
	testutil.Equal(t, gotChild.NukedAt != nil, true)
	gotShared, err := d.Get(shared)
	testutil.NoError(t, err)
	testutil.Equal(t, gotShared.Archived, false)
	gotCW, err := d.Get(childWorkerTask) // archived, not deleted → row survives
	testutil.NoError(t, err)
	testutil.Equal(t, gotCW.Archived, true)
}

// TestSmoke_HeraCascadeDeleteDepth2Count pins the accurate worktree count for a
// 2-level subtree: an INTERNAL bridge task (bound under two subtree
// orchestrators) is reclaimed + archived and MUST be counted, while a task bound
// under a non-subtree parent is preserved and excluded. Guards against the count
// undercounting internal-bridge worktrees.
func TestSmoke_HeraCascadeDeleteDepth2Count(t *testing.T) {
	d := testDB(t)
	t.Setenv("HOME", t.TempDir())
	a := seedHeraOrch(t, d, "orch-a") // root (alpha-first)
	c := seedHeraOrch(t, d, "orch-c") // nested under a (bridge task tc)
	g := seedHeraOrch(t, d, "orch-g") // nested under c (bridge task tg)

	mk := func(orch int64, name string, kind db.HeraRoleKind, task string) {
		r, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: orch, Name: name, Kind: kind, ArgusProject: "p"})
		testutil.NoError(t, err)
		if _, err := d.Get(task); err != nil {
			testutil.NoError(t, d.Add(&model.Task{ID: task, Name: task, Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}))
		}
		_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: r.ID, ArgusTaskID: task, WorktreePath: "/" + name})
		testutil.NoError(t, err)
	}
	mk(a, "coordA", db.HeraKindCoordinator, "ta")
	mk(a, "workerR", db.HeraKindWorker, "tc") // bridges orch-c (tc is c's coord)
	mk(c, "coordC", db.HeraKindCoordinator, "tc")
	mk(c, "workerC", db.HeraKindWorker, "tg") // bridges orch-g (tg is g's coord) — INTERNAL bridge
	mk(g, "coordG", db.HeraKindCoordinator, "tg")
	mk(g, "workerG", db.HeraKindWorker, "twg") // sole-bound leaf

	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()
	sim.InjectKey(tcell.KeyRune, '2', 0)
	syncUI(t, app.tapp)
	// Expand orch-a if collapsed (first-run default) so its bridge worker is visible.
	var collapsed bool
	readUI(t, app.tapp, func() {
		if o := app.heraPage.Rail().SelectedOrch(); o != nil {
			collapsed = app.heraPage.Rail().OrchCollapsed(o.ID)
		}
	})
	if collapsed {
		sim.InjectKey(tcell.KeyRune, ' ', 0)
		syncUI(t, app.tapp)
	}
	// Row 1 is orch-a's workerR (bridges orch-c); cascade subtree = {orch-c, orch-g}.
	sim.InjectKey(tcell.KeyRune, 'j', 0)
	syncUI(t, app.tapp)
	sim.InjectKey(tcell.KeyCtrlD, 0, 0)
	syncUI(t, app.tapp)

	// tg (internal bridge c↔g) + twg (leaf) = 2 worktrees; tc preserved (bound
	// under orch-a, outside the subtree). The count must NOT undercount tg.
	readUI(t, app.tapp, func() {
		testutil.Contains(t, app.heraConfirmModal.Message(), "2 worktree(s)")
	})
	sim.InjectKey(tcell.KeyRune, 'y', 0)
	syncUI(t, app.tapp)

	// orch-c + orch-g NUKED (still present), not hard-deleted.
	gotC, err := d.HeraOrchestrator(c)
	testutil.NoError(t, err)
	testutil.Equal(t, gotC.NukedAt != nil, true)
	gotG, err := d.HeraOrchestrator(g)
	testutil.NoError(t, err)
	testutil.Equal(t, gotG.NukedAt != nil, true)
	gotTg, err := d.Get("tg") // internal bridge archived (worktree reclaimed)
	testutil.NoError(t, err)
	testutil.Equal(t, gotTg.Archived, true)
	gotTwg, err := d.Get("twg") // leaf archived
	testutil.NoError(t, err)
	testutil.Equal(t, gotTwg.Archived, true)
	gotTc, err := d.Get("tc")
	testutil.NoError(t, err)
	testutil.Equal(t, gotTc.Archived, false) // bound under orch-a → preserved
}

// TestSmoke_HeraTabKeysDoNotBreakTabSwitchOrQuit audits the key-collision
// surface: 1/2/3 still switch tabs while the Hera tab is focused.
func TestSmoke_HeraTabKeysDoNotBreakTabSwitch(t *testing.T) {
	d := testDB(t)
	orch := seedHeraOrch(t, d, "orch")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")

	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()

	sim.InjectKey(tcell.KeyRune, '2', 0) // → Hera
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { testutil.Equal(t, app.header.ActiveTab(), widget.TabHera) })

	sim.InjectKey(tcell.KeyRune, '1', 0) // → Tasks
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { testutil.Equal(t, app.header.ActiveTab(), widget.TabTasks) })

	sim.InjectKey(tcell.KeyRune, '2', 0) // → Hera
	syncUI(t, app.tapp)
	sim.InjectKey(tcell.KeyRune, '3', 0) // → Settings
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { testutil.Equal(t, app.header.ActiveTab(), widget.TabSettings) })
}

// TestSmoke_HeraRemoteModeMutationKeysInert proves the Hera-tab mutation keys
// are no-ops in remote mode (no local *db.DB → heraOps nil, callbacks unwired).
func TestSmoke_HeraRemoteModeMutationKeysInert(t *testing.T) {
	app := New(stubStore{}, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()

	sim.InjectKey(tcell.KeyRune, '2', 0)
	syncUI(t, app.tapp)
	// Any mutation key must not panic and must not leave a modal open.
	sim.InjectKey(tcell.KeyRune, 'a', 0)
	sim.InjectKey(tcell.KeyRune, 'P', 0)
	sim.InjectKey(tcell.KeyCtrlD, 0, 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.mode, modeTaskList)
		testutil.Equal(t, app.heraOps == nil, true)
	})
}

// TestSmoke_HelpListsHeraKeys verifies the help overlay registers the Hera
// section so the bindings are discoverable.
func TestSmoke_HelpListsHeraKeys(t *testing.T) {
	found := false
	for _, sec := range modal.HelpSections {
		if sec.Title == "Hera View (rail)" {
			found = true
		}
	}
	testutil.Equal(t, found, true)
}

// TestSmoke_HeraDetailsTreeMode drives the flow end-to-end through the real
// event loop: Hera tab → coordinator selected → Tab into the Details region
// (which stacks the roster over the orchestration-tree graph) → the embedded
// tree is projected with nodes. Proves the global key routing, focus ladder, and
// tree projection compose without any toggle. The tree edges come from the role
// hierarchy (coordinator → worker), NOT depends_on.
func TestSmoke_HeraDetailsTreeMode(t *testing.T) {
	d := testDB(t)
	orch := seedHeraOrch(t, d, "orch")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	seedHeraBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "tw")

	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()

	// The cursor lands on the orch header, which IS the coordinator (folded in),
	// so no extra navigation is needed to select the coordinator.
	sim.InjectKey(tcell.KeyRune, '2', 0) // → Hera tab (cursor lands on orch header)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { testutil.Equal(t, app.heraPage.SelectionContext().IsCoordinator(), true) })

	// Tab into the Details region (rail → coord → agent). The tree is stacked
	// under the roster and already projected — no toggle.
	sim.InjectKey(tcell.KeyTab, 0, 0)
	syncUI(t, app.tapp)
	sim.InjectKey(tcell.KeyTab, 0, 0)
	syncUI(t, app.tapp)
	// The embedded tree has nodes (cursor non-empty — lands on the coordinator).
	readUI(t, app.tapp, func() { testutil.Equal(t, app.heraPage.DAG().CurrentTask() != "", true) })
}
