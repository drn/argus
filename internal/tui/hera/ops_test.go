package hera

import (
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// selFor builds a Selection over a freshly-read RoleView for the given role id,
// resolving its orchestrator. Mirrors what the rail hands the mutation layer.
func roleSel(t *testing.T, d *db.DB, role *db.HeraRole, taskID string) Selection {
	t.Helper()
	rv := &RoleView{RoleID: role.ID, OrchID: role.OrchestratorID, Name: role.Name, Kind: role.Kind, TaskID: taskID, Live: taskID != ""}
	if st, err := d.HeraRoleStatusFor(role.ID); err == nil {
		rv.Status = st.Status
		rv.HasStatus = true
	}
	return Selection{Role: rv}
}

func orchSel(orchID int64, name string) Selection {
	return Selection{Orch: &OrchView{ID: orchID, Name: name}}
}

func TestOps_ArchiveToggle_Role(t *testing.T) {
	d := memDB(t)
	o := seedOrch(t, d, "o")
	role := seedBoundRole(t, d, o, "w", db.HeraKindWorker, "t1")
	ops := NewOps(d)

	// Active → archive.
	testutil.NoError(t, ops.ArchiveToggle(roleSel(t, d, role, "t1")))
	got, _ := d.HeraRole(role.ID)
	testutil.Equal(t, got.ArchivedAt != nil, true)

	// Archived → unarchive.
	testutil.NoError(t, ops.ArchiveToggle(roleSel(t, d, role, "t1")))
	got, _ = d.HeraRole(role.ID)
	testutil.Equal(t, got.ArchivedAt == nil, true)
}

func TestOps_ArchiveToggle_Orchestrator(t *testing.T) {
	d := memDB(t)
	o := seedOrch(t, d, "o")
	ops := NewOps(d)

	testutil.NoError(t, ops.ArchiveToggle(orchSel(o, "o")))
	got, _ := d.HeraOrchestrator(o)
	testutil.Equal(t, got.ArchivedAt != nil, true)

	testutil.NoError(t, ops.ArchiveToggle(orchSel(o, "o")))
	got, _ = d.HeraOrchestrator(o)
	testutil.Equal(t, got.ArchivedAt == nil, true)
}

func TestOps_PinToggle(t *testing.T) {
	d := memDB(t)
	o := seedOrch(t, d, "o")
	role := seedBoundRole(t, d, o, "w", db.HeraKindWorker, "t1")
	ops := NewOps(d)

	testutil.NoError(t, ops.PinToggle(roleSel(t, d, role, "t1")))
	got, _ := d.HeraRole(role.ID)
	testutil.Equal(t, got.PinnedAt != nil, true)

	testutil.NoError(t, ops.PinToggle(roleSel(t, d, role, "t1")))
	got, _ = d.HeraRole(role.ID)
	testutil.Equal(t, got.PinnedAt == nil, true)

	// Orchestrator pin/unpin.
	testutil.NoError(t, ops.PinToggle(orchSel(o, "o")))
	go1, _ := d.HeraOrchestrator(o)
	testutil.Equal(t, go1.PinnedAt != nil, true)
}

func TestOps_Rename(t *testing.T) {
	d := memDB(t)
	o := seedOrch(t, d, "o")
	role := seedBoundRole(t, d, o, "w", db.HeraKindWorker, "t1")
	ops := NewOps(d)

	testutil.NoError(t, ops.Rename(roleSel(t, d, role, "t1"), "renamed"))
	got, _ := d.HeraRole(role.ID)
	testutil.Equal(t, got.Name, "renamed")

	testutil.NoError(t, ops.Rename(orchSel(o, "o"), "orch2"))
	go1, _ := d.HeraOrchestrator(o)
	testutil.Equal(t, go1.Name, "orch2")
}

func TestOps_Rename_ConflictSurfaces(t *testing.T) {
	d := memDB(t)
	o := seedOrch(t, d, "o")
	seedBoundRole(t, d, o, "taken", db.HeraKindWorker, "")
	role := seedBoundRole(t, d, o, "w", db.HeraKindWorker, "")
	ops := NewOps(d)

	err := ops.Rename(Selection{Role: &RoleView{RoleID: role.ID, OrchID: o, Name: "w", Kind: db.HeraKindWorker}}, "taken")
	testutil.ErrorIs(t, err, db.ErrHeraNameConflict)
}

func TestOps_StepStatus_AdvanceRevert(t *testing.T) {
	d := memDB(t)
	o := seedOrch(t, d, "o")
	role := seedBoundRole(t, d, o, "w", db.HeraKindWorker, "t1")
	ops := NewOps(d)

	// idle (unset) → working on first advance.
	testutil.NoError(t, ops.StepStatus(roleSel(t, d, role, "t1"), +1))
	st, _ := d.HeraRoleStatusFor(role.ID)
	testutil.Equal(t, st.Status, db.HeraStatusWorking)

	// working → blocked.
	testutil.NoError(t, ops.StepStatus(roleSel(t, d, role, "t1"), +1))
	st, _ = d.HeraRoleStatusFor(role.ID)
	testutil.Equal(t, st.Status, db.HeraStatusBlocked)

	// revert blocked → working.
	testutil.NoError(t, ops.StepStatus(roleSel(t, d, role, "t1"), -1))
	st, _ = d.HeraRoleStatusFor(role.ID)
	testutil.Equal(t, st.Status, db.HeraStatusWorking)
}

func TestOps_StepStatus_WorkerDoneRollsToReview(t *testing.T) {
	d := memDB(t)
	o := seedOrch(t, d, "o")
	role := seedBoundRole(t, d, o, "w", db.HeraKindWorker, "t1")
	testutil.NoError(t, d.UpsertHeraRoleStatus(role.ID, db.HeraStatusBlocked))
	ops := NewOps(d)

	// blocked → done (worker) rolls the in_progress task to in_review.
	testutil.NoError(t, ops.StepStatus(roleSel(t, d, role, "t1"), +1))
	st, _ := d.HeraRoleStatusFor(role.ID)
	testutil.Equal(t, st.Status, db.HeraStatusDone)

	got, _ := d.Get("t1")
	testutil.Equal(t, got.Status, model.StatusInReview)
}

func TestOps_StepStatus_OrchHeaderNoop(t *testing.T) {
	d := memDB(t)
	o := seedOrch(t, d, "o")
	ops := NewOps(d)
	err := ops.StepStatus(orchSel(o, "o"), +1)
	testutil.ErrorIs(t, err, errNoTarget)
}

func TestOps_DeleteRole(t *testing.T) {
	d := memDB(t)
	o := seedOrch(t, d, "o")
	role := seedBoundRole(t, d, o, "w", db.HeraKindWorker, "")
	ops := NewOps(d)
	testutil.NoError(t, ops.DeleteRole(role.ID))
	_, err := d.HeraRole(role.ID)
	testutil.ErrorIs(t, err, db.ErrHeraNotFound)
}

func TestOps_DeleteOrchestrator_CascadesRolesPreservesTask(t *testing.T) {
	d := memDB(t)
	o := seedOrch(t, d, "o")
	role := seedBoundRole(t, d, o, "w", db.HeraKindWorker, "t1")
	ops := NewOps(d)

	testutil.NoError(t, ops.DeleteOrchestrator(o))

	_, err := d.HeraOrchestrator(o)
	testutil.ErrorIs(t, err, db.ErrHeraNotFound)
	_, err = d.HeraRole(role.ID)
	testutil.ErrorIs(t, err, db.ErrHeraNotFound)
	// The argus task row is preserved (only the hera grouping was removed).
	got, err := d.Get("t1")
	testutil.NoError(t, err)
	testutil.Equal(t, got != nil, true)
}

// TestOps_MultiBindingIsolation verifies deleting role R in orchestrator A
// leaves the SAME task's role in orchestrator B live (distinct role rows).
func TestOps_MultiBindingIsolation(t *testing.T) {
	d := memDB(t)
	a := seedOrch(t, d, "A")
	b := seedOrch(t, d, "B")

	// One task, two roles (worker in A, coordinator in B).
	roleA := seedBoundRole(t, d, a, "w", db.HeraKindWorker, "shared")
	roleB, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: b, Name: "c", Kind: db.HeraKindCoordinator, ArgusProject: "p"})
	testutil.NoError(t, err)
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: roleB.ID, ArgusTaskID: "shared", WorktreePath: "/wt/shared"})
	testutil.NoError(t, err)

	ops := NewOps(d)
	testutil.NoError(t, ops.DeleteRole(roleA.ID))

	// Role in A gone; role in B (same task) untouched.
	_, err = d.HeraRole(roleA.ID)
	testutil.ErrorIs(t, err, db.ErrHeraNotFound)
	gotB, err := d.HeraRole(roleB.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, gotB.OrchestratorID, b)
}

func TestOps_EmptySelectionNoTarget(t *testing.T) {
	d := memDB(t)
	ops := NewOps(d)
	testutil.ErrorIs(t, ops.ArchiveToggle(Selection{}), errNoTarget)
	testutil.ErrorIs(t, ops.PinToggle(Selection{}), errNoTarget)
	testutil.ErrorIs(t, ops.Rename(Selection{}, "x"), errNoTarget)
	testutil.ErrorIs(t, ops.RetireRole(nil, true), errNoTarget)
}

// TestOps_RetireRole_SoleBound verifies retire sets status done, rolls the
// worker task to in_review, ends the role's live binding, and archives the role.
func TestOps_RetireRole_SoleBound(t *testing.T) {
	d := memDB(t)
	o := seedOrch(t, d, "o")
	role := seedBoundRole(t, d, o, "w", db.HeraKindWorker, "t1")
	ops := NewOps(d)

	rv := &RoleView{RoleID: role.ID, OrchID: o, Name: role.Name, Kind: db.HeraKindWorker, TaskID: "t1", Live: true}
	testutil.NoError(t, ops.RetireRole(rv, true))

	// Status is done.
	st, err := d.HeraRoleStatusFor(role.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, st.Status, db.HeraStatusDone)
	// Binding ended (no live binding for the role).
	_, err = d.HeraLiveBindingByRole(role.ID)
	testutil.ErrorIs(t, err, db.ErrHeraNotFound)
	// Role archived.
	got, err := d.HeraRole(role.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.ArchivedAt != nil, true)
	// Worker task rolled to in_review.
	task, err := d.Get("t1")
	testutil.NoError(t, err)
	testutil.Equal(t, task.Status, model.StatusInReview)
}

// TestOps_RetireRole_MultiBoundDoesNotRoll verifies that with rollTask=false
// (multi-bound) the task status is NOT changed, only this role's binding ends
// and the role archives.
func TestOps_RetireRole_MultiBoundDoesNotRoll(t *testing.T) {
	d := memDB(t)
	o := seedOrch(t, d, "o")
	role := seedBoundRole(t, d, o, "w", db.HeraKindWorker, "t1")
	ops := NewOps(d)

	rv := &RoleView{RoleID: role.ID, OrchID: o, Name: role.Name, Kind: db.HeraKindWorker, TaskID: "t1", Live: true}
	testutil.NoError(t, ops.RetireRole(rv, false))

	// Task status unchanged (still in_progress from the seed).
	task, err := d.Get("t1")
	testutil.NoError(t, err)
	testutil.Equal(t, task.Status, model.StatusInProgress)
	// Role still archived + binding ended.
	got, err := d.HeraRole(role.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.ArchivedAt != nil, true)
	_, err = d.HeraLiveBindingByRole(role.ID)
	testutil.ErrorIs(t, err, db.ErrHeraNotFound)
}
