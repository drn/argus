package hera

import (
	"errors"
	"testing"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/widget"
)

// memDB opens an in-memory db.DB for hera-store seeding. NEVER touches
// ~/.argus or the live daemon.
func memDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// orchView is a tiny builder for a single orchestrator: the first (coord, task)
// pair is the coordinator, the rest are live workers. (Relocated from the
// retired tree_test.go; still used by the filter/model in-memory tests.)
func orchView(id int64, name, coordTask string, workers ...struct{ name, task string }) OrchView {
	o := OrchView{ID: id, Name: name}
	if coordTask != "" {
		o.Roles = append(o.Roles, RoleView{
			Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: coordTask,
			TaskStatus: "in_progress", SessionRunning: true,
		})
	}
	for _, w := range workers {
		o.Roles = append(o.Roles, RoleView{
			Name: w.name, Kind: db.HeraKindWorker, Live: true, TaskID: w.task,
			TaskStatus: "in_progress", SessionRunning: true,
		})
	}
	return o
}

func wk(name, task string) struct{ name, task string } {
	return struct{ name, task string }{name, task}
}

// seedOrch creates an active orchestrator and returns its id.
func seedOrch(t *testing.T, d *db.DB, name string) int64 {
	t.Helper()
	o, err := d.CreateHeraOrchestrator(name, "")
	testutil.NoError(t, err)
	return o.ID
}

// seedRole creates a role + binds it to taskID (Add'ing the task first so
// task_meta FK constraints are satisfied). Returns the role.
func seedBoundRole(t *testing.T, d *db.DB, orchID int64, name string, kind db.HeraRoleKind, taskID string) *db.HeraRole {
	t.Helper()
	role, err := d.CreateHeraRole(db.CreateHeraRoleInput{
		OrchestratorID: orchID, Name: name, Kind: kind, ArgusProject: "p",
	})
	testutil.NoError(t, err)
	if taskID != "" {
		testutil.NoError(t, d.Add(&model.Task{ID: taskID, Name: taskID, Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}))
		_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{
			RoleID: role.ID, ArgusTaskID: taskID, WorktreePath: "/wt/" + taskID,
		})
		testutil.NoError(t, err)
	}
	return role
}

func TestBuildModel_NilReaderEmpty(t *testing.T) {
	m, err := BuildModel(nil, nil, nil, nil, nil)
	testutil.NoError(t, err)
	testutil.Equal(t, m.IsEmpty(), true)
}

// TestBuildModel_PopulatesKanbanStatus pins add-hera-kanban-status: OrchView
// carries the orchestrator's kanban_status, defaulting to active and
// reflecting an explicit value.
func TestBuildModel_PopulatesKanbanStatus(t *testing.T) {
	d := memDB(t)
	def := seedOrch(t, d, "kb-default")
	blocked := seedOrch(t, d, "kb-blocked")
	testutil.NoError(t, d.SetHeraOrchestratorKanbanStatus(blocked, db.HeraKanbanBlocked))

	m, err := BuildModel(d, nil, nil, nil, nil)
	testutil.NoError(t, err)
	testutil.Equal(t, m.OrchByID(def).KanbanStatus, db.HeraKanbanActive)
	testutil.Equal(t, m.OrchByID(blocked).KanbanStatus, db.HeraKanbanBlocked)
}

func TestBuildModel_PartitionsSections(t *testing.T) {
	d := memDB(t)
	active := seedOrch(t, d, "active-orch")
	seedBoundRole(t, d, active, "coord", db.HeraKindCoordinator, "t-active")

	pinnedID := seedOrch(t, d, "pinned-orch")
	testutil.NoError(t, d.PinHeraOrchestrator(pinnedID))

	archID := seedOrch(t, d, "arch-orch")
	testutil.NoError(t, d.ArchiveHeraOrchestrator(archID))

	m, err := BuildModel(d, nil, nil, nil, nil)
	testutil.NoError(t, err)
	testutil.Equal(t, len(m.Active), 1)
	testutil.Equal(t, len(m.Pinned), 1)
	testutil.Equal(t, len(m.Archived), 1)
	testutil.Equal(t, m.Active[0].Name, "active-orch")
	testutil.Equal(t, m.Pinned[0].Name, "pinned-orch")
	testutil.Equal(t, m.Archived[0].Name, "arch-orch")
}

// TestBuildModel_FiltersNuked pins BUG-022 Tier-2: a NUKED orchestrator and a
// NUKED role are invisible to every rail section (a HIDDEN/archived row is NOT —
// it still surfaces, in the Archived section / its coordinator's expando).
func TestBuildModel_FiltersNuked(t *testing.T) {
	d := memDB(t)

	keep := seedOrch(t, d, "keep")
	coord := seedBoundRole(t, d, keep, "coord", db.HeraKindCoordinator, "t-coord")
	w1 := seedBoundRole(t, d, keep, "w1", db.HeraKindWorker, "t-w1")
	wNuke := seedBoundRole(t, d, keep, "w-nuke", db.HeraKindWorker, "t-wn")
	_ = coord

	// A whole nuked orchestrator.
	gone := seedOrch(t, d, "gone")
	seedBoundRole(t, d, gone, "coord", db.HeraKindCoordinator, "t-gone")

	testutil.NoError(t, d.NukeHeraRole(wNuke.ID))
	testutil.NoError(t, d.NukeHeraOrchestrator(gone))

	m, err := BuildModel(d, nil, nil, nil, nil)
	testutil.NoError(t, err)

	// The nuked orchestrator is in no section.
	testutil.Nil(t, m.OrchByID(gone))
	for _, sec := range [][]OrchView{m.Pinned, m.Active, m.Archived} {
		for _, o := range sec {
			if o.ID == gone {
				t.Fatal("nuked orchestrator should not render in any section")
			}
		}
	}

	// The keep orchestrator still has its coordinator + w1, but NOT w-nuke.
	kv := m.OrchByID(keep)
	if kv == nil {
		t.Fatal("keep orchestrator missing")
	}
	for _, r := range kv.Roles {
		if r.RoleID == wNuke.ID {
			t.Fatal("nuked role should not render")
		}
	}
	// w1 survives.
	found := false
	for _, r := range kv.Roles {
		if r.RoleID == w1.ID {
			found = true
		}
	}
	testutil.Equal(t, found, true)
}

// The locked must-have: a single argus task bound under TWO orchestrators
// surfaces under EACH of them in the model (via two distinct roles).
func TestBuildModel_MultiBindingFanOut(t *testing.T) {
	d := memDB(t)
	orchA := seedOrch(t, d, "orch-a")
	orchB := seedOrch(t, d, "orch-b")

	// One task, bound as a worker in A and a coordinator in B.
	const sharedTask = "shared-task"
	roleA, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: orchA, Name: "wkr", Kind: db.HeraKindWorker, ArgusProject: "p"})
	testutil.NoError(t, err)
	roleB, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: orchB, Name: "coord", Kind: db.HeraKindCoordinator, ArgusProject: "p"})
	testutil.NoError(t, err)
	testutil.NoError(t, d.Add(&model.Task{ID: sharedTask, Name: sharedTask, Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}))
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: roleA.ID, ArgusTaskID: sharedTask, WorktreePath: "/wt/a"})
	testutil.NoError(t, err)
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: roleB.ID, ArgusTaskID: sharedTask, WorktreePath: "/wt/b"})
	testutil.NoError(t, err)

	m, err := BuildModel(d, nil, nil, nil, nil)
	testutil.NoError(t, err)
	testutil.Equal(t, len(m.Active), 2)

	// The shared task appears once under each orchestrator's roles.
	count := 0
	for _, o := range m.Active {
		for _, r := range o.Roles {
			if r.TaskID == sharedTask {
				count++
				testutil.Equal(t, r.Live, true)
			}
		}
	}
	testutil.Equal(t, count, 2)
}

func TestBuildModel_FreelanceHoisted(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "free", db.HeraKindFreelance, "t-free")

	m, err := BuildModel(d, nil, nil, nil, nil)
	testutil.NoError(t, err)
	// Coordinator stays under the orchestrator; freelance hoists out.
	testutil.Equal(t, len(m.Active[0].Roles), 1)
	testutil.Equal(t, m.Active[0].Roles[0].Kind, db.HeraKindCoordinator)
	testutil.Equal(t, len(m.Freelance), 1)
	testutil.Equal(t, m.Freelance[0].Name, "free")
}

func TestBuildModel_ReadyToCloseAndStatus(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	role := seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-rc")
	// Stamp ready_to_close on the bound task + a working status on the role.
	testutil.NoError(t, d.SetMeta("t-rc", db.HeraMetaNamespace, db.HeraMetaKeyReadyToClose, "true"))
	testutil.NoError(t, d.UpsertHeraRoleStatus(role.ID, db.HeraStatusWorking))

	m, err := BuildModel(d, nil, nil, nil, nil)
	testutil.NoError(t, err)
	rv := m.Active[0].Roles[0]
	testutil.Equal(t, rv.ReadyToClose, true)
	testutil.Equal(t, rv.HasStatus, true)
	testutil.Equal(t, rv.Status, db.HeraStatusWorking)
}

// TestBuildModel_ContextSize pins RoleView.ContextSize's population from
// task_meta(hera, context_size) — the same stamp `argus coord-hook` now
// writes for coordinator, worker, and freelance roles alike
// (add-worker-context-indicator). Mirrors TestBuildModel_ReadyToCloseAndStatus's
// shape: a pure read off the already-fetched heraMeta map, no I/O of its own.
func TestBuildModel_ContextSize(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-ctx")
	testutil.NoError(t, d.SetMeta("t-ctx", db.HeraMetaNamespace, db.HeraMetaKeyContextSize, "48213"))

	m, err := BuildModel(d, nil, nil, nil, nil)
	testutil.NoError(t, err)
	rv := m.Active[0].Roles[0]
	testutil.Equal(t, rv.ContextSize, 48213)
}

// TestBuildModel_ContextSize_AbsentOrMalformed pins the fail-open default: no
// stamped value, or a value that fails to parse as an int, both resolve to 0
// rather than erroring buildRoleView or the whole model build.
func TestBuildModel_ContextSize_AbsentOrMalformed(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		d := memDB(t)
		orch := seedOrch(t, d, "orch")
		seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-ctx-absent")

		m, err := BuildModel(d, nil, nil, nil, nil)
		testutil.NoError(t, err)
		testutil.Equal(t, m.Active[0].Roles[0].ContextSize, 0)
	})

	t.Run("malformed", func(t *testing.T) {
		d := memDB(t)
		orch := seedOrch(t, d, "orch")
		seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-ctx-bad")
		testutil.NoError(t, d.SetMeta("t-ctx-bad", db.HeraMetaNamespace, db.HeraMetaKeyContextSize, "not-a-number"))

		m, err := BuildModel(d, nil, nil, nil, nil)
		testutil.NoError(t, err)
		testutil.Equal(t, m.Active[0].Roles[0].ContextSize, 0)
	})
}

func TestBuildModel_BridgeTaskID(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")

	t.Run("live role: bridge equals live task", func(t *testing.T) {
		role := seedBoundRole(t, d, orch, "live", db.HeraKindWorker, "t-live")
		_ = role
		m, err := BuildModel(d, nil, nil, nil, nil)
		testutil.NoError(t, err)
		var rv *RoleView
		for i := range m.Active[0].Roles {
			if m.Active[0].Roles[i].Name == "live" {
				rv = &m.Active[0].Roles[i]
			}
		}
		testutil.Equal(t, rv != nil, true)
		testutil.Equal(t, rv.BridgeTaskID, "t-live")
		testutil.Equal(t, rv.LinkEndReason, "")
	})

	t.Run("ended role: bridge is latest task + end_reason, not live", func(t *testing.T) {
		role := seedBoundRole(t, d, orch, "ended", db.HeraKindWorker, "t-ended")
		bnd, err := d.HeraLiveBindingByRole(role.ID)
		testutil.NoError(t, err)
		testutil.NoError(t, d.EndHeraBinding(bnd.ID, db.HeraEndReasonUserDeleted))

		m, err := BuildModel(d, nil, nil, nil, nil)
		testutil.NoError(t, err)
		var rv *RoleView
		for i := range m.Active[0].Roles {
			if m.Active[0].Roles[i].Name == "ended" {
				rv = &m.Active[0].Roles[i]
			}
		}
		testutil.Equal(t, rv != nil, true)
		testutil.Equal(t, rv.Live, false)
		testutil.Equal(t, rv.TaskID, "")              // no live binding
		testutil.Equal(t, rv.BridgeTaskID, "t-ended") // latest binding still bridges
		testutil.Equal(t, rv.LinkEndReason, db.HeraEndReasonUserDeleted)
	})
}

func TestOrchView_CoordBridgeTaskID(t *testing.T) {
	t.Run("live coordinator returns its task", func(t *testing.T) {
		o := OrchView{Roles: []RoleView{
			{Kind: db.HeraKindCoordinator, Live: true, TaskID: "tc", BridgeTaskID: "tc"},
		}}
		testutil.Equal(t, o.CoordBridgeTaskID(), "tc")
	})
	t.Run("ended-non-teardown coordinator still bridges via BridgeTaskID", func(t *testing.T) {
		o := OrchView{Roles: []RoleView{
			{Kind: db.HeraKindCoordinator, Live: false, TaskID: "", BridgeTaskID: "tc", LinkEndReason: "argus_deleted"},
		}}
		testutil.Equal(t, o.CoordBridgeTaskID(), "tc")
	})
	t.Run("no coordinator role yields empty", func(t *testing.T) {
		o := OrchView{Roles: []RoleView{
			{Kind: db.HeraKindWorker, Live: true, TaskID: "tw", BridgeTaskID: "tw"},
		}}
		testutil.Equal(t, o.CoordBridgeTaskID(), "")
	})
	t.Run("first coordinator wins", func(t *testing.T) {
		o := OrchView{Roles: []RoleView{
			{Kind: db.HeraKindCoordinator, Live: true, TaskID: "t1", BridgeTaskID: "t1"},
			{Kind: db.HeraKindCoordinator, Live: true, TaskID: "t2", BridgeTaskID: "t2"},
		}}
		testutil.Equal(t, o.CoordBridgeTaskID(), "t1")
	})
}

func TestSelection_FocusTaskID(t *testing.T) {
	coordOrch := &OrchView{Roles: []RoleView{
		{Kind: db.HeraKindCoordinator, Live: true, TaskID: "tc"},
	}}
	t.Run("worker selection returns the worker task", func(t *testing.T) {
		s := Selection{Role: &RoleView{Kind: db.HeraKindWorker, TaskID: "tw"}, Orch: coordOrch}
		testutil.Equal(t, s.FocusTaskID(), "tw")
	})
	t.Run("coordinator header (no role) returns the coordinator task", func(t *testing.T) {
		s := Selection{Role: nil, Orch: coordOrch}
		testutil.Equal(t, s.IsCoordinator(), true)
		testutil.Equal(t, s.FocusTaskID(), "tc")
	})
	t.Run("coordinator-less header returns empty", func(t *testing.T) {
		s := Selection{Role: nil, Orch: &OrchView{Roles: []RoleView{
			{Kind: db.HeraKindWorker, Live: true, TaskID: "tw"},
		}}}
		testutil.Equal(t, s.FocusTaskID(), "")
	})
	t.Run("empty selection returns empty", func(t *testing.T) {
		testutil.Equal(t, Selection{}.FocusTaskID(), "")
	})
}

// coordOf builds an orchestrator whose coordinator role has an explicit RoleID
// and bridge task (the coord-of-both fixtures need distinct coordinator role ids
// to exercise the earliest-id=parent rule).
func coordOf(id int64, name string, coordRoleID int64, coordTask string, workers ...RoleView) OrchView {
	o := OrchView{ID: id, Name: name, Roles: []RoleView{
		{RoleID: coordRoleID, OrchID: id, Name: "coord", Kind: db.HeraKindCoordinator,
			Live: true, TaskID: coordTask, BridgeTaskID: coordTask},
	}}
	for i := range workers {
		workers[i].OrchID = id
		o.Roles = append(o.Roles, workers[i])
	}
	return o
}

// TestModel_CoordSpawnedSubteamBridge covers the real under-nesting bug: one
// coordinator task coordinates BOTH a parent and a child orchestrator (the
// coordinator-spawned sub-team shape that hera_new_orchestrator creates). The
// in-memory bridge must nest the LATER-coordinator-role-id orchestrator under
// the earlier one, matching db.SubtreeOrchIDs (whose parent-side join matches
// ANY non-teardown binding — coordinator OR worker).
func TestModel_CoordSpawnedSubteamBridge(t *testing.T) {
	// Task T coordinates P (coord role 100) and Q (coord role 200). P is the
	// earlier-id parent; Q nests under P.
	p := coordOf(1, "P", 100, "T",
		RoleView{RoleID: 101, Name: "pw", Kind: db.HeraKindWorker, Live: true, TaskID: "tpw", BridgeTaskID: "tpw"})
	q := coordOf(2, "Q", 200, "T",
		RoleView{RoleID: 201, Name: "qw", Kind: db.HeraKindWorker, Live: true, TaskID: "tqw", BridgeTaskID: "tqw"})
	m := Model{Active: []OrchView{p, q}}

	t.Run("consumed set nests the later-id orchestrator only", func(t *testing.T) {
		consumed := m.consumedSet(m.bridgeIndex())
		testutil.Equal(t, consumed[2], true)  // Q nested under P
		testutil.Equal(t, consumed[1], false) // P stays a root
	})

	t.Run("coordBridgeChildren is asymmetric by coordinator role id", func(t *testing.T) {
		pc := m.coordBridgeChildren(&m.Active[0])
		testutil.Equal(t, len(pc), 1)
		testutil.Equal(t, pc[0].Name, "Q")
		// Q is the later id, so it parents nothing (no A↔B cycle).
		testutil.Equal(t, len(m.coordBridgeChildren(&m.Active[1])), 0)
	})

	t.Run("coordBridgeParentOf direction", func(t *testing.T) {
		testutil.Equal(t, coordBridgeParentOf(&m.Active[0], &m.Active[1]), true)
		testutil.Equal(t, coordBridgeParentOf(&m.Active[1], &m.Active[0]), false)
	})
}

// TestCoordBridge_UnifiedResolution: in the defensive multi-coordinator case
// (first coord role unbound, a later one bound), coordBridgeParentOf must key off
// the SAME coordinator role that CoordBridgeTaskID/bridgeIndex use — the first
// with a non-empty bridge task — so the worker path and coord path never resolve
// different coordinator tasks/ids.
func TestCoordBridge_UnifiedResolution(t *testing.T) {
	// P: first coord role (id 90) is UNBOUND; second coord role (id 100) carries
	// task T. Q: coord role 200 carries T. P must parent Q off role 100 (< 200).
	p := OrchView{ID: 1, Name: "P", Roles: []RoleView{
		{RoleID: 90, Name: "coord-dead", Kind: db.HeraKindCoordinator}, // no binding
		{RoleID: 100, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "T", BridgeTaskID: "T"},
	}}
	q := coordOf(2, "Q", 200, "T")
	task, role := p.coordBridge()
	testutil.Equal(t, task, "T")
	testutil.Equal(t, role, int64(100)) // the BOUND coord, not the dead first one
	testutil.Equal(t, p.CoordBridgeTaskID(), "T")
	m := Model{Active: []OrchView{p, q}}
	testutil.Equal(t, coordBridgeParentOf(&m.Active[0], &m.Active[1]), true)
	testutil.Equal(t, coordBridgeParentOf(&m.Active[1], &m.Active[0]), false)
}

// TestModel_CoordBridgeNoFalsePositives: orchestrators with DIFFERENT coordinator
// tasks never coord-bridge each other (only the shared-coordinator shape nests).
func TestModel_CoordBridgeNoFalsePositives(t *testing.T) {
	a := coordOf(1, "A", 100, "ta")
	b := coordOf(2, "B", 200, "tb")
	m := Model{Active: []OrchView{a, b}}
	testutil.Equal(t, coordBridgeParentOf(&m.Active[0], &m.Active[1]), false)
	testutil.Equal(t, len(m.coordBridgeChildren(&m.Active[0])), 0)
	testutil.Equal(t, len(m.consumedSet(m.bridgeIndex())), 0)
}

// TestModel_SubtreeAgentCount_IncludesArchivedRegardlessOfLiveness pins the
// rail header badge fix: archiving a role (the `a` hide key) only stamps
// hera_roles.archived_at — it never ends the role's binding — so a role
// tucked into the coordinator's greyed-out Archive bucket keeps Live=true
// forever. The badge must count it anyway (a Live-gated count silently
// double-reports it as still "current"); Live plays no part in the count at
// all, archived or not.
func TestModel_SubtreeAgentCount_IncludesArchivedRegardlessOfLiveness(t *testing.T) {
	o := orchView(1, "orch", "tc", wk("current", "t1"))
	o.Roles = append(o.Roles,
		RoleView{Name: "hidden-not-torn-down", Kind: db.HeraKindWorker, Live: true, Archived: true, TaskID: "t2"},
		RoleView{Name: "hidden-binding-ended", Kind: db.HeraKindWorker, Live: false, Archived: true, TaskID: "t3"},
	)
	m := Model{Active: []OrchView{o}}

	// coord excluded, all 3 workers counted regardless of Live/Archived.
	testutil.Equal(t, m.SubtreeAgentCount(1), 3)
}

// TestModel_SubtreeAgentCount_RecursesIntoNestedSubcoordAndItsArchive pins the
// other half of the fix: the badge must recurse into bridged sub-coordinators
// (a nested sub-team spawned from a worker row) rather than stopping at the
// orchestrator's own direct roles, and it must count that nested subtree's
// own archived workers too — while never double-counting the bridging worker
// row in the parent against the SAME agent's coordinator role in the child.
func TestModel_SubtreeAgentCount_RecursesIntoNestedSubcoordAndItsArchive(t *testing.T) {
	// R(coord tr, worker bridge→tc) → C(coord tc, worker wc, archived worker).
	r := orchView(1, "R", "tr", wk("bridge", "tc"))
	c := orchView(2, "C", "tc", wk("wc", "twc"))
	c.Roles = append(c.Roles, RoleView{Name: "hidden", Kind: db.HeraKindWorker, Live: true, Archived: true, TaskID: "t-hidden"})
	m := Model{Active: []OrchView{r, c}}

	// C alone: its own coord excluded, wc + hidden counted = 2.
	testutil.Equal(t, m.SubtreeAgentCount(2), 2)
	// R's subtree: R's coord excluded, "bridge" counted once (not also C's
	// coord, the same underlying task) + C's wc + hidden = 3.
	testutil.Equal(t, m.SubtreeAgentCount(1), 3)
}

// TestBuildModel_PopulatesDetailsFields proves the additive coordinator-Details
// projection inputs (orch + role creation, the live binding's worktree + start,
// the role-status update time, and the bound task name) flow into the model.
func TestBuildModel_PopulatesDetailsFields(t *testing.T) {
	d := memDB(t)
	orchID := seedOrch(t, d, "orch")
	role := seedBoundRole(t, d, orchID, "coord", db.HeraKindCoordinator, "t-c")
	testutil.NoError(t, d.UpsertHeraRoleStatus(role.ID, db.HeraStatusWorking))

	m, err := BuildModel(d, nil, nil, nil, nil)
	testutil.NoError(t, err)
	ov := m.Active[0]
	testutil.Equal(t, ov.CreatedAt.IsZero(), false)
	rv := ov.Roles[0]
	testutil.Equal(t, rv.CreatedAt.IsZero(), false)
	testutil.Equal(t, rv.ArgusProject, "p")
	testutil.Equal(t, rv.WorktreePath, "/wt/t-c")
	testutil.Equal(t, rv.BindingStartedAt.IsZero(), false)
	testutil.Equal(t, rv.StatusUpdatedAt.IsZero(), false)
	testutil.Equal(t, rv.TaskName, "t-c")

	// Derived metadata over the real projection: repos-in-scope is the role's
	// project and last activity is at/after the orchestrator creation.
	meta := deriveCoordMeta(&ov)
	testutil.DeepEqual(t, meta.Repos, []string{"p"})
	testutil.Equal(t, meta.AgentName, "t-c")
	testutil.Equal(t, meta.Worktree, "/wt/t-c")
	testutil.Equal(t, meta.LastActivity.Before(ov.CreatedAt), false)
}

// coordSubtreeNI returns the SubtreeNeedsInput flag of the orchestrator's
// folded coordinator role (the glyph the rail header projects). Fails the test
// if the orchestrator or its coordinator is missing.
func coordSubtreeNI(t *testing.T, m *Model, orchID int64) bool {
	t.Helper()
	o := m.OrchByID(orchID)
	if o == nil {
		t.Fatalf("orchestrator %d not found", orchID)
	}
	c := o.CoordRole()
	if c == nil {
		t.Fatalf("orchestrator %d has no coordinator role", orchID)
	}
	return c.SubtreeNeedsInput
}

// roleByName returns a pointer to the named role within an orchestrator.
func roleByName(t *testing.T, m *Model, orchID int64, name string) *RoleView {
	t.Helper()
	o := m.OrchByID(orchID)
	if o == nil {
		t.Fatalf("orchestrator %d not found", orchID)
	}
	for i := range o.Roles {
		if o.Roles[i].Name == name {
			return &o.Roles[i]
		}
	}
	t.Fatalf("role %q not found under orchestrator %d", name, orchID)
	return nil
}

// TestRollupNeedsInput_BubblesToParentAndRoot is the BUG-018 headline: a leaf
// worker two bridge levels down (R → C → G) that needs input makes its own row,
// every intervening sub-coordinator (the bridging worker rows AND the child
// coordinators), AND the root coordinator all report needs-input — and the whole
// chain clears when the descendant resolves.
func TestRollupNeedsInput_BubblesToParentAndRoot(t *testing.T) {
	// R(coord tr, worker w→tc) → C(coord tc, worker wc→tg) → G(coord tg, worker wg→twg).
	r := orchView(1, "R", "tr", wk("w", "tc"))
	c := orchView(2, "C", "tc", wk("wc", "tg"))
	g := orchView(3, "G", "tg", wk("wg", "twg"))
	m := Model{Active: []OrchView{r, c, g}}

	// The deepest leaf needs input.
	roleByName(t, &m, 3, "wg").NeedsInput = true
	m.rollupNeedsInput()

	// Every coordinator in the chain, root included, rolls it up.
	testutil.Equal(t, coordSubtreeNI(t, &m, 3), true) // G (parent of the leaf)
	testutil.Equal(t, coordSubtreeNI(t, &m, 2), true) // C (sub-coordinator)
	testutil.Equal(t, coordSubtreeNI(t, &m, 1), true) // R (ROOT)
	// The bridging worker rows (each IS a nested sub-coordinator) roll up too.
	testutil.Equal(t, roleByName(t, &m, 1, "w").SubtreeNeedsInput, true)
	testutil.Equal(t, roleByName(t, &m, 2, "wc").SubtreeNeedsInput, true)
	// The leaf shows it on itself.
	testutil.Equal(t, roleByName(t, &m, 3, "wg").SubtreeNeedsInput, true)

	// Resolve the leaf → the whole chain clears.
	roleByName(t, &m, 3, "wg").NeedsInput = false
	m.rollupNeedsInput()
	testutil.Equal(t, coordSubtreeNI(t, &m, 1), false)
	testutil.Equal(t, coordSubtreeNI(t, &m, 2), false)
	testutil.Equal(t, coordSubtreeNI(t, &m, 3), false)
	testutil.Equal(t, roleByName(t, &m, 1, "w").SubtreeNeedsInput, false)
}

// TestRollupNeedsInput_BlockedStatusCounts proves the role's self-asserted hera
// `blocked` status is a needs-input source for the rollup too (not only the
// authoritative NeedsInput flag), so a "blocked" worker bubbles up identically.
func TestRollupNeedsInput_BlockedStatusCounts(t *testing.T) {
	r := orchView(1, "R", "tr", wk("w", "tc"))
	c := orchView(2, "C", "tc", wk("wc", "twc"))
	m := Model{Active: []OrchView{r, c}}
	wc := roleByName(t, &m, 2, "wc")
	wc.HasStatus = true
	wc.Status = db.HeraStatusBlocked
	m.rollupNeedsInput()
	testutil.Equal(t, coordSubtreeNI(t, &m, 2), true)
	testutil.Equal(t, coordSubtreeNI(t, &m, 1), true)
}

// TestRollupNeedsInput_BlockedClearsToRoot proves the source-2 (hera `blocked`
// status) CLEAR path propagates: a deep worker stepped OFF `blocked` clears the
// "(?)" on every ancestor coordinator, transitively to the root (BUG-023). This
// mirrors the SET in TestRollupNeedsInput_BlockedStatusCounts, in reverse.
func TestRollupNeedsInput_BlockedClearsToRoot(t *testing.T) {
	// R(coord tr, worker w→tc) → C(coord tc, worker wc→twc).
	r := orchView(1, "R", "tr", wk("w", "tc"))
	c := orchView(2, "C", "tc", wk("wc", "twc"))
	m := Model{Active: []OrchView{r, c}}
	wc := roleByName(t, &m, 2, "wc")
	wc.HasStatus = true
	wc.Status = db.HeraStatusBlocked
	m.rollupNeedsInput()
	testutil.Equal(t, coordSubtreeNI(t, &m, 1), true) // ROOT shows "(?)"
	testutil.Equal(t, coordSubtreeNI(t, &m, 2), true)

	// Step the worker OFF blocked (→ working, as `S` revert does). The rollup
	// recomputes and the "(?)" clears on the sub-coordinator AND the root.
	wc.Status = db.HeraStatusWorking
	m.rollupNeedsInput()
	testutil.Equal(t, coordSubtreeNI(t, &m, 1), false)
	testutil.Equal(t, coordSubtreeNI(t, &m, 2), false)
	testutil.Equal(t, roleByName(t, &m, 1, "w").SubtreeNeedsInput, false)
}

// TestRollupNeedsInput_NoFalsePositive: with no needs-input role anywhere, no
// coordinator rolls up "(?)".
func TestRollupNeedsInput_NoFalsePositive(t *testing.T) {
	r := orchView(1, "R", "tr", wk("w", "tc"))
	c := orchView(2, "C", "tc", wk("wc", "twc"))
	m := Model{Active: []OrchView{r, c}}
	m.rollupNeedsInput()
	testutil.Equal(t, coordSubtreeNI(t, &m, 1), false)
	testutil.Equal(t, coordSubtreeNI(t, &m, 2), false)
	testutil.Equal(t, roleByName(t, &m, 1, "w").SubtreeNeedsInput, false)
}

// TestRollupNeedsInput_CoordSpawnedSubteam: a coordinator-spawned sub-team
// (shared coord task, child nests via coordBridgeChildren, NOT a worker bridge)
// also propagates needs-input across the bridge to the parent coordinator.
func TestRollupNeedsInput_CoordSpawnedSubteam(t *testing.T) {
	// Task T coordinates P (coord role 100) and S (coord role 200). S nests under P.
	p := coordOf(1, "P", 100, "T",
		RoleView{RoleID: 101, Name: "pw", Kind: db.HeraKindWorker, Live: true, TaskID: "tpw", BridgeTaskID: "tpw"})
	s := coordOf(2, "S", 200, "T",
		RoleView{RoleID: 201, Name: "sw", Kind: db.HeraKindWorker, Live: true, TaskID: "tsw", BridgeTaskID: "tsw"})
	m := Model{Active: []OrchView{p, s}}

	roleByName(t, &m, 2, "sw").NeedsInput = true
	m.rollupNeedsInput()
	testutil.Equal(t, coordSubtreeNI(t, &m, 2), true) // S (the sub-team)
	testutil.Equal(t, coordSubtreeNI(t, &m, 1), true) // P (the parent) rolls it up
}

// TestRollupNeedsInput_CycleSafe: a bridge cycle (A↔B) terminates and still
// reports needs-input for the reachable members.
func TestRollupNeedsInput_CycleSafe(t *testing.T) {
	// A(coord ta, worker wa→tb) and B(coord tb, worker wb→ta) bridge each other.
	a := orchView(1, "A", "ta", wk("wa", "tb"))
	b := orchView(2, "B", "tb", wk("wb", "ta"))
	m := Model{Active: []OrchView{a, b}}
	roleByName(t, &m, 1, "wa").NeedsInput = true
	m.rollupNeedsInput() // must not hang
	testutil.Equal(t, coordSubtreeNI(t, &m, 1), true)
	testutil.Equal(t, coordSubtreeNI(t, &m, 2), true)
}

// TestRollupNeedsInput_ArchivedLeafExcludedFromParent: a leaf worker directly
// under a coordinator is blocked, but once its role is ARCHIVED (`a`) its own
// needs-input signal SHALL NOT count toward its parent coordinator's rollup
// (exclude-archived-from-needs-input-rollup). The archive is a reversible "hide"
// that keeps the binding live, so the signal is still genuinely true — it just
// stops propagating up.
func TestRollupNeedsInput_ArchivedLeafExcludedFromParent(t *testing.T) {
	r := orchView(1, "R", "tr", wk("w", "tw"))
	m := Model{Active: []OrchView{r}}
	w := roleByName(t, &m, 1, "w")
	w.NeedsInput = true
	w.Archived = true
	m.rollupNeedsInput()
	// The archived leaf no longer flags its parent coordinator.
	testutil.Equal(t, coordSubtreeNI(t, &m, 1), false)
}

// TestRollupNeedsInput_ArchivedLeafExcludedAcrossMultipleBridgeLevels reuses the
// R → C → G bridge chain from TestRollupNeedsInput_BubblesToParentAndRoot: the
// deepest leaf is blocked and would bubble to the root, but archiving that leaf's
// role stops it at every level — its parent G, the intervening sub-coordinators C
// and R, and the bridging worker rows that represent them all report false.
func TestRollupNeedsInput_ArchivedLeafExcludedAcrossMultipleBridgeLevels(t *testing.T) {
	// R(coord tr, worker w→tc) → C(coord tc, worker wc→tg) → G(coord tg, worker wg→twg).
	r := orchView(1, "R", "tr", wk("w", "tc"))
	c := orchView(2, "C", "tc", wk("wc", "tg"))
	g := orchView(3, "G", "tg", wk("wg", "twg"))
	m := Model{Active: []OrchView{r, c, g}}

	// The deepest leaf needs input, then is archived to dismiss it.
	wg := roleByName(t, &m, 3, "wg")
	wg.NeedsInput = true
	wg.Archived = true
	m.rollupNeedsInput()

	// Excluded at every coordinator level, root included.
	testutil.Equal(t, coordSubtreeNI(t, &m, 3), false) // G (immediate parent)
	testutil.Equal(t, coordSubtreeNI(t, &m, 2), false) // C (sub-coordinator)
	testutil.Equal(t, coordSubtreeNI(t, &m, 1), false) // R (ROOT)
	// And the intervening bridging worker rows (each a nested sub-coordinator).
	testutil.Equal(t, roleByName(t, &m, 1, "w").SubtreeNeedsInput, false)
	testutil.Equal(t, roleByName(t, &m, 2, "wc").SubtreeNeedsInput, false)
}

// TestRollupNeedsInput_ArchivedBridgingRowHidesSubtree is the reported case that
// a post-filter over BridgeSubtree's flattened output would MISS: the bridging
// worker row is itself NOT blocked, but a worker inside its bridged child
// orchestrator IS. Archiving the BRIDGING ROW (its child orchestrator's own
// archived_at stays unset) must stop that whole hidden subtree from bubbling up
// to the parent and any further ancestor — while the child orchestrator, reached
// as its own root, still surfaces its genuine signal.
func TestRollupNeedsInput_ArchivedBridgingRowHidesSubtree(t *testing.T) {
	// R(coord tr, worker w→tc) → C(coord tc, worker wc→tg) → G(coord tg, worker wg→twg).
	r := orchView(1, "R", "tr", wk("w", "tc"))
	c := orchView(2, "C", "tc", wk("wc", "tg"))
	g := orchView(3, "G", "tg", wk("wg", "twg"))
	m := Model{Active: []OrchView{r, c, g}}

	// The deepest leaf genuinely needs input.
	roleByName(t, &m, 3, "wg").NeedsInput = true
	// Archive the bridging row in C that represents nested sub-coordinator G.
	// G itself (and G's roles) are NOT archived.
	roleByName(t, &m, 2, "wc").Archived = true
	m.rollupNeedsInput()

	// G is unarchived and its worker is genuinely blocked, so G still surfaces on
	// its own header (archiving stops propagation to ancestors, not the node's own
	// rollup over its own children).
	testutil.Equal(t, coordSubtreeNI(t, &m, 3), true)
	// But the archived bridging row hides the subtree from C (parent) and R (ancestor).
	testutil.Equal(t, coordSubtreeNI(t, &m, 2), false)
	testutil.Equal(t, coordSubtreeNI(t, &m, 1), false)
}

// TestRollupNeedsInput_ArchivedOrchestratorExcludedViaWorkerBridge: a whole child
// orchestrator archived via its OWN header (OrchView.Archived), reached from a
// LIVE ancestor through a worker-bridge, must be excluded from that ancestor's
// rollup even though it contains a genuinely blocked worker. ArchiveHeraOrchestrator
// stamps only the orchestrator's archived_at, so the child's roles stay unarchived —
// the exclusion keys on the containing orchestrator's Archived flag.
func TestRollupNeedsInput_ArchivedOrchestratorExcludedViaWorkerBridge(t *testing.T) {
	r := orchView(1, "R", "tr", wk("w", "tc"))
	c := orchView(2, "C", "tc", wk("wc", "twc"))
	c.Archived = true // whole sub-orchestrator archived via its header
	m := Model{Active: []OrchView{r}, Archived: []OrchView{c}}

	roleByName(t, &m, 2, "wc").NeedsInput = true
	m.rollupNeedsInput()

	// The live parent excludes the archived worker-bridged child's subtree.
	testutil.Equal(t, coordSubtreeNI(t, &m, 1), false)
}

// TestRollupNeedsInput_ArchivedRoleStillShowsOwnGlyph pins Goal #3: archiving a
// blocked role removes it from ancestor rollups but NEVER blanks its OWN row —
// the archived leaf keeps rendering its own "(?)" glyph in place, exactly as
// today, while its parent coordinator stops counting it.
func TestRollupNeedsInput_ArchivedRoleStillShowsOwnGlyph(t *testing.T) {
	r := orchView(1, "R", "tr", wk("w", "tw"))
	m := Model{Active: []OrchView{r}}
	w := roleByName(t, &m, 1, "w")
	w.NeedsInput = true
	w.Archived = true
	m.rollupNeedsInput()

	// The archived row itself still shows its own needs-input glyph (unchanged).
	testutil.Equal(t, w.needsInputOwn(), true)
	testutil.Equal(t, w.ShowsNeedsInput(), true)
	testutil.Equal(t, roleByName(t, &m, 1, "w").SubtreeNeedsInput, true)
	// But it is excluded from its parent coordinator's rollup.
	testutil.Equal(t, coordSubtreeNI(t, &m, 1), false)
}

// TestRollupNeedsInput_ArchivedCycleSafe extends the A↔B bridge cycle from
// TestRollupNeedsInput_CycleSafe with one cyclic member archived: A's worker is
// genuinely blocked, but B's bridging row back into A is archived. The rollup must
// still terminate (visited-set cycle guard) and report correctly — A's own signal
// surfaces, while B no longer bubbles A's signal across the archived back-edge.
func TestRollupNeedsInput_ArchivedCycleSafe(t *testing.T) {
	// A(coord ta, worker wa→tb) and B(coord tb, worker wb→ta) bridge each other.
	a := orchView(1, "A", "ta", wk("wa", "tb"))
	b := orchView(2, "B", "tb", wk("wb", "ta"))
	m := Model{Active: []OrchView{a, b}}
	roleByName(t, &m, 1, "wa").NeedsInput = true
	// Archive B's bridging row back into A (one cyclic member archived).
	roleByName(t, &m, 2, "wb").Archived = true
	m.rollupNeedsInput() // must not hang

	// A's genuine, non-archived signal still surfaces.
	testutil.Equal(t, coordSubtreeNI(t, &m, 1), true)
	// B no longer rolls up A's signal across the archived back-edge.
	testutil.Equal(t, coordSubtreeNI(t, &m, 2), false)
}

// TestBuildModel_NeedsInputStamped proves the end-to-end seam: the authoritative
// per-task needs-input set threaded into BuildModel stamps the live role's own
// flag and the rollup reaches its coordinator.
func TestBuildModel_NeedsInputStamped(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")

	m, err := BuildModel(d, map[string]bool{"t-wkr": true}, nil, nil, nil)
	testutil.NoError(t, err)
	wkr := roleByName(t, &m, orch, "wkr")
	testutil.Equal(t, wkr.NeedsInput, true)
	testutil.Equal(t, wkr.SubtreeNeedsInput, true)
	// The coordinator (folded header) rolls it up.
	testutil.Equal(t, coordSubtreeNI(t, &m, orch), true)

	// Without the set, nothing flags.
	m2, err := BuildModel(d, nil, nil, nil, nil)
	testutil.NoError(t, err)
	testutil.Equal(t, roleByName(t, &m2, orch, "wkr").NeedsInput, false)
	testutil.Equal(t, coordSubtreeNI(t, &m2, orch), false)
}

// TestBuildModel_SessionIdleSuppressesSpinner is the BUG-036 headline at the
// BuildModel seam: a live in_progress role normally counts as active (spins), but
// when the App's content-aware idle set marks its bound session idle (a parked
// fullscreen agent), the role is NOT active and renders a static glyph instead.
func TestBuildModel_SessionIdleSuppressesSpinner(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")

	running := map[string]bool{"t-coord": true, "t-wkr": true}

	// No content-idle: a live in_progress worker with a running session spins.
	m, err := BuildModel(d, nil, nil, running, nil)
	testutil.NoError(t, err)
	wkr := roleByName(t, &m, orch, "wkr")
	testutil.Equal(t, wkr.SessionIdle, false)
	testutil.Equal(t, wkr.IsActive(), true)

	// Content-idle for the worker's task → SessionIdle stamped, IsActive false.
	m2, err := BuildModel(d, nil, map[string]bool{"t-wkr": true}, running, nil)
	testutil.NoError(t, err)
	wkr2 := roleByName(t, &m2, orch, "wkr")
	testutil.Equal(t, wkr2.SessionIdle, true)
	testutil.Equal(t, wkr2.IsActive(), false)
	// The status glyph is no longer the animated spinner frame.
	glyph, _ := statusIcon(wkr2, false, 0)
	if glyph == widget.SpinnerFrame(0) {
		t.Error("content-idle role should not render the active spinner glyph")
	}
}

// TestBuildModel_LiveWorkerInReviewSpins is the BUG-C headline at the BuildModel
// seam and the display sibling of BUG-A: a WORKER whose task has rolled to
// in_review (per #707 a hera worker deliberately sits in in_review while its
// session lingers alive for coordinator close-out) but whose session is STILL
// LIVE and actively producing output MUST animate the working spinner (IsActive),
// not fall through to the static review glyph. The rail spinner is gated on
// liveness + content-idle, NOT bound-task status.
func TestBuildModel_LiveWorkerInReviewSpins(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")
	// Roll the worker's task to in_review, keeping its binding live (#707).
	testutil.NoError(t, d.SetStatus("t-wkr", model.StatusInReview))

	running := map[string]bool{"t-coord": true, "t-wkr": true}

	// Live in_review worker with a RUNNING session, NOT content-idle (actively
	// producing) → spins.
	m, err := BuildModel(d, nil, nil, running, nil)
	testutil.NoError(t, err)
	wkr := roleByName(t, &m, orch, "wkr")
	testutil.Equal(t, wkr.TaskStatus, model.StatusInReview.String())
	testutil.Equal(t, wkr.Live, true)
	testutil.Equal(t, wkr.SessionRunning, true)
	testutil.Equal(t, wkr.SessionIdle, false)
	testutil.Equal(t, wkr.IsActive(), true)

	// Same live in_review worker, now content-idle (parked/done) → no spinner
	// (BUG-036 stays safe under the running+content-idle predicate).
	m2, err := BuildModel(d, nil, map[string]bool{"t-wkr": true}, running, nil)
	testutil.NoError(t, err)
	wkr2 := roleByName(t, &m2, orch, "wkr")
	testutil.Equal(t, wkr2.SessionIdle, true)
	testutil.Equal(t, wkr2.IsActive(), false)

	// BUG-C regression: a DEAD worker (session exited → dropped from the running
	// set) whose binding LINGERS (bindings don't end on session exit) stays Live
	// with an in_review task but MUST NOT spin. This is the case Live && !idle
	// alone got wrong — the running gate closes it.
	m3, err := BuildModel(d, nil, nil, map[string]bool{"t-coord": true}, nil) // t-wkr NOT running
	testutil.NoError(t, err)
	wkr3 := roleByName(t, &m3, orch, "wkr")
	testutil.Equal(t, wkr3.Live, true)
	testutil.Equal(t, wkr3.SessionRunning, false)
	testutil.Equal(t, wkr3.SessionIdle, false)
	testutil.Equal(t, wkr3.IsActive(), false)
}

// TestBuildModel_LiveWorkerInReviewSurfacesNeedsInput is the BUG-A headline at
// the BuildModel seam: a WORKER whose task has rolled to in_review (per #707 a
// hera worker deliberately sits in in_review while its session lingers alive for
// the coordinator to close out) but whose session is STILL LIVE and genuinely at
// a prompt MUST surface "(?)". RollHeraWorkerToReview keeps the binding live
// (rv.Live stays true; only status + meta change), and the App's needsInput set
// is content-aware (it flags a task only while it shows a CURRENT awaiting-input
// signal), so a flagged live in_review worker is genuinely blocked — the rollup
// must light its own row and the ancestor coordinator.
func TestBuildModel_LiveWorkerInReviewSurfacesNeedsInput(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")

	flagged := map[string]bool{"t-wkr": true}

	// In_progress + flagged: surfaces (the always-correct path).
	m, err := BuildModel(d, flagged, nil, nil, nil)
	testutil.NoError(t, err)
	testutil.Equal(t, roleByName(t, &m, orch, "wkr").NeedsInput, true)
	testutil.Equal(t, coordSubtreeNI(t, &m, orch), true)

	// The worker rolls to in_review but its binding stays LIVE (session alive,
	// #707). It is still in the content-aware needsInput set → still genuinely
	// blocked → "(?)" MUST persist on the row AND roll up to the coordinator.
	testutil.NoError(t, d.SetStatus("t-wkr", model.StatusInReview))
	m2, err := BuildModel(d, flagged, nil, nil, nil)
	testutil.NoError(t, err)
	testutil.Equal(t, roleByName(t, &m2, orch, "wkr").NeedsInput, true)
	testutil.Equal(t, coordSubtreeNI(t, &m2, orch), true)
}

// TestBuildModel_ExitedWorkerSuppressesNeedsInput is the BUG-023 guard under the
// loosened gate: once a worker's SESSION EXITS its binding ends (rv.Live becomes
// false), so even though the App's needsInput set still names the task (a marker
// in the log tail), the role no longer surfaces "(?)" — neither on its own
// (now-finished) row nor in the ancestor coordinator's rollup. Liveness, not task
// status, is the BUG-023 clear condition.
func TestBuildModel_ExitedWorkerSuppressesNeedsInput(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")

	flagged := map[string]bool{"t-wkr": true}

	// Worker rolled to in_review AND its session has exited → end the binding.
	testutil.NoError(t, d.SetStatus("t-wkr", model.StatusInReview))
	_, err := d.EndHeraBindingsForTask("t-wkr", "exit")
	testutil.NoError(t, err)

	m, err := BuildModel(d, flagged, nil, nil, nil)
	testutil.NoError(t, err)
	// The worker role is no longer live, so neither its own row nor the ancestor
	// coordinator's rollup pins "(?)".
	testutil.Equal(t, roleByName(t, &m, orch, "wkr").NeedsInput, false)
	testutil.Equal(t, coordSubtreeNI(t, &m, orch), false)
}

// errReader returns an error from ListHeraOrchestrators to prove BuildModel
// surfaces read errors rather than swallowing them.
type errReader struct{ HeraReader }

func (errReader) ListHeraOrchestrators(bool) ([]*db.HeraOrchestrator, error) {
	return nil, errors.New("boom")
}

func TestBuildModel_PropagatesReadError(t *testing.T) {
	_, err := BuildModel(errReader{}, nil, nil, nil, nil)
	testutil.Contains(t, errString(err), "boom")
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
