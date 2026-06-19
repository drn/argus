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

// A header over an orchestrator with no coordinator role has no status to step.
func TestOps_StepStatus_CoordinatorLessHeaderNoop(t *testing.T) {
	d := memDB(t)
	o := seedOrch(t, d, "o")
	ops := NewOps(d)
	err := ops.StepStatus(orchSel(o, "o"), +1)
	testutil.ErrorIs(t, err, errNoTarget)
}

// coordHeaderSel builds a HEADER selection (Role nil) whose orchestrator's
// folded coordinator role is role — what the rail hands the mutation layer when
// the cursor lands on an orchestrator header.
func coordHeaderSel(t *testing.T, d *db.DB, orchID int64, role *db.HeraRole, taskID string) Selection {
	t.Helper()
	rv := RoleView{RoleID: role.ID, OrchID: orchID, Name: role.Name, Kind: role.Kind, TaskID: taskID, Live: taskID != ""}
	if st, err := d.HeraRoleStatusFor(role.ID); err == nil {
		rv.Status = st.Status
		rv.HasStatus = true
	}
	return Selection{Orch: &OrchView{ID: orchID, Name: "o", Roles: []RoleView{rv}}}
}

// BUG-014: s/S cycle the coordinator's hera status from a header selection, and
// stepping a coordinator to done never rolls its task.
func TestOps_StepStatus_CoordinatorHeaderCycles(t *testing.T) {
	d := memDB(t)
	o := seedOrch(t, d, "o")
	coord := seedBoundRole(t, d, o, "coord", db.HeraKindCoordinator, "tc")
	testutil.NoError(t, d.UpsertHeraRoleStatus(coord.ID, db.HeraStatusDone))
	ops := NewOps(d)

	// S (revert) on the header: done → blocked (the rail ✓ clears).
	testutil.NoError(t, ops.StepStatus(coordHeaderSel(t, d, o, coord, "tc"), -1))
	st, _ := d.HeraRoleStatusFor(coord.ID)
	testutil.Equal(t, st.Status, db.HeraStatusBlocked)

	// s (advance) back: blocked → done.
	testutil.NoError(t, ops.StepStatus(coordHeaderSel(t, d, o, coord, "tc"), +1))
	st, _ = d.HeraRoleStatusFor(coord.ID)
	testutil.Equal(t, st.Status, db.HeraStatusDone)

	// Stepping a COORDINATOR to done must NOT roll its task to in_review — the
	// roll is worker-only.
	got, _ := d.Get("tc")
	testutil.Equal(t, got.Status, model.StatusInProgress)
}

// TestOps_NukeRole verifies the Tier-2 nuke: the role's live binding ends, the
// role row is marked NUKED (nuked_at + archived_at) so it leaves the rail, but
// the row is RETAINED (HeraRole(id) still returns it) — never a hard delete.
func TestOps_NukeRole(t *testing.T) {
	d := memDB(t)
	o := seedOrch(t, d, "o")
	role := seedBoundRole(t, d, o, "w", db.HeraKindWorker, "t1")
	ops := NewOps(d)

	rv := &RoleView{RoleID: role.ID, OrchID: o, Name: role.Name, Kind: db.HeraKindWorker, TaskID: "t1", Live: true}
	testutil.NoError(t, ops.NukeRole(rv))

	// Binding ended (no live binding for the role).
	_, err := d.HeraLiveBindingByRole(role.ID)
	testutil.ErrorIs(t, err, db.ErrHeraNotFound)
	// Row retained but marked nuked + archived; invisible to the rail-feeding list.
	got, err := d.HeraRole(role.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.NukedAt != nil, true)
	testutil.Equal(t, got.ArchivedAt != nil, true)
	list, err := d.ListHeraRoles(o, true)
	testutil.NoError(t, err)
	testutil.Equal(t, len(list), 0)
	// The argus task row is preserved (the App archives it separately).
	task, err := d.Get("t1")
	testutil.NoError(t, err)
	testutil.Equal(t, task != nil, true)
}

// TestOps_NukeOrchestrator marks the orchestrator nuked (retained, invisible).
func TestOps_NukeOrchestrator(t *testing.T) {
	d := memDB(t)
	o := seedOrch(t, d, "o")
	role := seedBoundRole(t, d, o, "w", db.HeraKindWorker, "t1")
	ops := NewOps(d)

	testutil.NoError(t, ops.NukeOrchestrator(o))

	got, err := d.HeraOrchestrator(o) // id lookup still returns it
	testutil.NoError(t, err)
	testutil.Equal(t, got.NukedAt != nil, true)
	list, err := d.ListHeraOrchestrators(true)
	testutil.NoError(t, err)
	testutil.Equal(t, len(list), 0)
	// The role row was NOT hard-deleted (FK cascade would be a delete) — id lookup
	// still returns it (its orchestrator is nuked, so the rail won't render it).
	gotRole, err := d.HeraRole(role.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, gotRole != nil, true)
}

// TestOps_MultiBindingIsolation verifies nuking role R in orchestrator A leaves
// the SAME task's role in orchestrator B live (distinct role rows).
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
	rvA := &RoleView{RoleID: roleA.ID, OrchID: a, Name: roleA.Name, Kind: db.HeraKindWorker, TaskID: "shared", Live: true}
	testutil.NoError(t, ops.NukeRole(rvA))

	// Role in A nuked (invisible to A's list); role in B (same task) untouched + live.
	listA, err := d.ListHeraRoles(a, true)
	testutil.NoError(t, err)
	testutil.Equal(t, len(listA), 0)
	gotB, err := d.HeraRole(roleB.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, gotB.OrchestratorID, b)
	_, err = d.HeraLiveBindingByRole(roleB.ID)
	testutil.NoError(t, err) // B's binding to the shared task survives
}

func TestOps_EmptySelectionNoTarget(t *testing.T) {
	d := memDB(t)
	ops := NewOps(d)
	testutil.ErrorIs(t, ops.ArchiveToggle(Selection{}), errNoTarget)
	testutil.ErrorIs(t, ops.PinToggle(Selection{}), errNoTarget)
	testutil.ErrorIs(t, ops.Rename(Selection{}, "x"), errNoTarget)
	testutil.ErrorIs(t, ops.NukeRole(nil), errNoTarget)
}
