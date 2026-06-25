package hera

import (
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
)

// *db.DB must satisfy the AdoptStore seam (the compile-time canary; mirrors the
// MutateStore assertion elsewhere). addTask is shared from panes_test.go.
var _ AdoptStore = (*db.DB)(nil)

func TestAdoptTaskIntoOrchestrator(t *testing.T) {
	t.Run("creates worker role + live binding and stamps meta", func(t *testing.T) {
		d := memDB(t)
		target := seedOrch(t, d, "target")
		addTask(t, d, "free-1")
		ops := NewAdoptOps(d)

		res, err := ops.AdoptTaskIntoOrchestrator(AdoptInput{
			ArgusTaskID:    "free-1",
			OrchestratorID: target,
			RoleName:       "freelancer-x",
			ArgusProject:   "p",
			WorktreePath:   "/wt/free-1",
		})
		testutil.NoError(t, err)
		testutil.Equal(t, res.RoleName, "freelancer-x")

		// Worker role under the target with a live binding to the task.
		role, err := d.HeraRole(res.RoleID)
		testutil.NoError(t, err)
		testutil.Equal(t, role.Kind, db.HeraKindWorker)
		testutil.Equal(t, role.OrchestratorID, target)
		live, err := d.HeraLiveBindingByTaskAndOrchestrator("free-1", target)
		testutil.NoError(t, err)
		testutil.Equal(t, live.WorktreePath, "/wt/free-1")

		// meta:hera.role=worker stamped.
		meta, err := d.ListMetaByNamespace(db.HeraMetaNamespace)
		testutil.NoError(t, err)
		testutil.Equal(t, meta["free-1"][db.HeraMetaKeyRole], string(db.HeraKindWorker))
	})

	t.Run("de-collides the default role name", func(t *testing.T) {
		d := memDB(t)
		target := seedOrch(t, d, "target")
		// An active role named "dup" already exists under the target.
		seedBoundRole(t, d, target, "dup", db.HeraKindWorker, "")
		addTask(t, d, "free-2")
		ops := NewAdoptOps(d)

		res, err := ops.AdoptTaskIntoOrchestrator(AdoptInput{
			ArgusTaskID: "free-2", OrchestratorID: target, RoleName: "dup", WorktreePath: "/wt",
		})
		testutil.NoError(t, err)
		testutil.Equal(t, res.RoleName, "dup-2")
	})

	t.Run("rejects empty task id", func(t *testing.T) {
		d := memDB(t)
		target := seedOrch(t, d, "target")
		_, err := NewAdoptOps(d).AdoptTaskIntoOrchestrator(AdoptInput{OrchestratorID: target})
		testutil.Contains(t, errStr(err), "no argus task id")
	})

	t.Run("rejects a duplicate live binding under the same orchestrator", func(t *testing.T) {
		d := memDB(t)
		orch := seedOrch(t, d, "orch")
		// Freelancer already bound under this orchestrator.
		seedBoundRole(t, d, orch, "free", db.HeraKindFreelance, "free-3")
		_, err := NewAdoptOps(d).AdoptTaskIntoOrchestrator(AdoptInput{
			ArgusTaskID: "free-3", OrchestratorID: orch, RoleName: "free", WorktreePath: "/wt",
		})
		testutil.Contains(t, errStr(err), "already bound")
	})

	t.Run("a worktree-orchestrator collision rolls the role back (no orphan)", func(t *testing.T) {
		d := memDB(t)
		target := seedOrch(t, d, "target")
		// An existing live binding already occupies (worktree, orchestrator).
		occupant := seedBoundRole(t, d, target, "occupant", db.HeraKindWorker, "")
		_, err := d.CreateHeraBinding(db.CreateHeraBindingInput{
			RoleID: occupant.ID, ArgusTaskID: "occupant-task", WorktreePath: "/wt/shared",
		})
		testutil.NoError(t, err)
		addTask(t, d, "occupant-task")
		addTask(t, d, "tf")

		before, err := d.ListHeraRoles(target, true)
		testutil.NoError(t, err)

		// Adopting a DIFFERENT task with the SAME worktree under the same
		// orchestrator collides on idx_hera_bindings_live_worktree_orch. The
		// transactional create must roll the worker role back.
		_, err = NewAdoptOps(d).AdoptTaskIntoOrchestrator(AdoptInput{
			ArgusTaskID: "tf", OrchestratorID: target, RoleName: "tf", WorktreePath: "/wt/shared",
		})
		testutil.Equal(t, err != nil, true)

		after, err := d.ListHeraRoles(target, true)
		testutil.NoError(t, err)
		testutil.Equal(t, len(after), len(before)) // no orphan role left behind
	})

	t.Run("rejects an unknown orchestrator", func(t *testing.T) {
		d := memDB(t)
		addTask(t, d, "free-4")
		_, err := NewAdoptOps(d).AdoptTaskIntoOrchestrator(AdoptInput{
			ArgusTaskID: "free-4", OrchestratorID: 999, WorktreePath: "/wt",
		})
		testutil.Contains(t, errStr(err), "no longer exists")
	})
}

func TestReparentCoordinator(t *testing.T) {
	t.Run("nests a coordinator under the chosen parent", func(t *testing.T) {
		d := memDB(t)
		child := seedOrch(t, d, "child")
		seedBoundRole(t, d, child, "child", db.HeraKindCoordinator, "child-coord")
		parent := seedOrch(t, d, "parent")
		seedBoundRole(t, d, parent, "parent", db.HeraKindCoordinator, "parent-coord")

		res, err := NewAdoptOps(d).ReparentCoordinator(ReparentInput{
			ChildOrchestratorID:  child,
			ParentOrchestratorID: parent,
		})
		testutil.NoError(t, err)
		testutil.Equal(t, res.RoleName, "child")

		// A worker role under the parent bound to the child's coord task.
		linkRole, err := d.HeraRole(res.RoleID)
		testutil.NoError(t, err)
		testutil.Equal(t, linkRole.Kind, db.HeraKindWorker)
		testutil.Equal(t, linkRole.OrchestratorID, parent)
		link, err := d.HeraLiveBindingByTaskAndOrchestrator("child-coord", parent)
		testutil.NoError(t, err)
		testutil.Equal(t, link.ID, res.BindingID)
	})

	t.Run("rejects self-adoption", func(t *testing.T) {
		d := memDB(t)
		c := seedOrch(t, d, "c")
		seedBoundRole(t, d, c, "c", db.HeraKindCoordinator, "c-coord")
		_, err := NewAdoptOps(d).ReparentCoordinator(ReparentInput{
			ChildOrchestratorID: c, ParentOrchestratorID: c,
		})
		testutil.Contains(t, errStr(err), "under itself")
	})

	t.Run("rejects a descendant target (cycle)", func(t *testing.T) {
		d := memDB(t)
		// gp coordinator task is "gp-coord"; child is nested under gp via a worker
		// role in gp bound to child's coord task → SubtreeOrchIDs(child) excludes
		// gp, but SubtreeOrchIDs(gp) includes child. So re-parenting gp under
		// child is a cycle.
		gp := seedOrch(t, d, "gp")
		seedBoundRole(t, d, gp, "gp", db.HeraKindCoordinator, "gp-coord")
		child := seedOrch(t, d, "child")
		seedBoundRole(t, d, child, "child", db.HeraKindCoordinator, "child-coord")
		// Nest child under gp: a worker role in gp bound to child's coord task.
		nest, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: gp, Name: "child", Kind: db.HeraKindWorker})
		testutil.NoError(t, err)
		_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: nest.ID, ArgusTaskID: "child-coord", WorktreePath: "/wt"})
		testutil.NoError(t, err)

		_, err = NewAdoptOps(d).ReparentCoordinator(ReparentInput{
			ChildOrchestratorID: gp, ParentOrchestratorID: child,
		})
		testutil.Contains(t, errStr(err), "cycle")
	})

	t.Run("rejects a coordinator with no coordinator role", func(t *testing.T) {
		d := memDB(t)
		child := seedOrch(t, d, "child") // no coord role
		parent := seedOrch(t, d, "parent")
		_, err := NewAdoptOps(d).ReparentCoordinator(ReparentInput{
			ChildOrchestratorID: child, ParentOrchestratorID: parent,
		})
		testutil.Contains(t, errStr(err), "no coordinator role")
	})

	t.Run("resolves task+worktree from the latest ended coord binding (dormant)", func(t *testing.T) {
		d := memDB(t)
		child := seedOrch(t, d, "child")
		coord := seedBoundRole(t, d, child, "child", db.HeraKindCoordinator, "child-coord")
		// Coordinator session ended — only an ENDED binding remains.
		live, err := d.HeraLiveBindingByRole(coord.ID)
		testutil.NoError(t, err)
		testutil.NoError(t, d.EndHeraBinding(live.ID, "manual"))
		parent := seedOrch(t, d, "parent")

		res, err := NewAdoptOps(d).ReparentCoordinator(ReparentInput{
			ChildOrchestratorID: child, ParentOrchestratorID: parent,
		})
		testutil.NoError(t, err)
		link, err := d.HeraLiveBindingByTaskAndOrchestrator("child-coord", parent)
		testutil.NoError(t, err)
		testutil.Equal(t, link.ID, res.BindingID)
		testutil.Equal(t, link.WorktreePath, "/wt/child-coord")
	})
}

// TestReparentCoordinator_BUG026 proves the teardown invariant: a coordinator
// already nested under one parent (live link) PLUS a leftover ended link role,
// re-parented under a new parent, ends the live link and DELETES every prior
// link role so exactly one clean link remains — no de-collided duplicates.
func TestReparentCoordinator_BUG026(t *testing.T) {
	d := memDB(t)
	child := seedOrch(t, d, "child")
	coord := seedBoundRole(t, d, child, "child", db.HeraKindCoordinator, "ct")

	// Old parent P with a leftover ENDED link role (a prior move the resync
	// reconciler ended but left behind) AND a current LIVE link role, both
	// pointing at the child's coord task "ct". The ended link is created+ended
	// first so the live link can reuse the same (worktree, orchestrator) — only
	// one live binding per (worktree, orch) is allowed (partial unique index).
	p := seedOrch(t, d, "P")
	endedLink, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: p, Name: "child", Kind: db.HeraKindWorker})
	testutil.NoError(t, err)
	endedBnd, err := d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: endedLink.ID, ArgusTaskID: "ct", WorktreePath: "/wt/ct"})
	testutil.NoError(t, err)
	testutil.NoError(t, d.EndHeraBinding(endedBnd.ID, "resync_missing"))
	liveLink, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: p, Name: "child-2", Kind: db.HeraKindWorker})
	testutil.NoError(t, err)
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: liveLink.ID, ArgusTaskID: "ct", WorktreePath: "/wt/ct"})
	testutil.NoError(t, err)

	_ = endedLink
	_ = liveLink
	// New parent Q.
	q := seedOrch(t, d, "Q")

	res, err := NewAdoptOps(d).ReparentCoordinator(ReparentInput{
		ChildOrchestratorID: child, ParentOrchestratorID: q,
	})
	testutil.NoError(t, err)

	// Every prior parent-link role under P is torn down (live + ended). Identity
	// checks would be fragile (SQLite reuses freed rowids), so assert the END
	// STATE: P holds no roles at all.
	pRoles, err := d.ListHeraRoles(p, true)
	testutil.NoError(t, err)
	testutil.Equal(t, len(pRoles), 0)

	// The child's own coordinator role + its live binding survive untouched.
	gotCoord, err := d.HeraRole(coord.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, gotCoord.Kind, db.HeraKindCoordinator)

	// Exactly two live bindings remain for "ct": the child's coord binding and
	// the single clean link under Q. No de-collided duplicate link roles.
	testutil.Equal(t, liveBindingCount(t, d, "ct"), 2)
	link, err := d.HeraLiveBindingByTaskAndOrchestrator("ct", q)
	testutil.NoError(t, err)
	testutil.Equal(t, link.ID, res.BindingID)

	// Re-parent AGAIN under a third orchestrator R: the Q link is torn down and
	// exactly one clean link remains — repeated moves never accumulate links.
	r := seedOrch(t, d, "R")
	_, err = NewAdoptOps(d).ReparentCoordinator(ReparentInput{
		ChildOrchestratorID: child, ParentOrchestratorID: r,
	})
	testutil.NoError(t, err)
	testutil.Equal(t, liveBindingCount(t, d, "ct"), 2) // coord + single link, still
	qRoles, err := d.ListHeraRoles(q, true)
	testutil.NoError(t, err)
	testutil.Equal(t, len(qRoles), 0) // the Q link role is gone
	_, err = d.HeraLiveBindingByTaskAndOrchestrator("ct", r)
	testutil.NoError(t, err)
}

// liveBindingCount returns the number of live (un-ended) bindings for a task.
func liveBindingCount(t *testing.T, d *db.DB, taskID string) int {
	t.Helper()
	all, err := d.ListHeraBindingsByTask(taskID)
	testutil.NoError(t, err)
	n := 0
	for _, b := range all {
		if b.EndedAt == nil {
			n++
		}
	}
	return n
}

func TestDetachCoordinator(t *testing.T) {
	t.Run("un-nests a nested coordinator back to top-level", func(t *testing.T) {
		d := memDB(t)
		// Child C nested under parent P: a worker link role in P bound to C's
		// coord task, plus C's own coordinator role + binding.
		child := seedOrch(t, d, "child")
		coord := seedBoundRole(t, d, child, "child", db.HeraKindCoordinator, "child-coord")
		parent := seedOrch(t, d, "parent")
		seedBoundRole(t, d, parent, "parent", db.HeraKindCoordinator, "parent-coord")
		// Nest C under P first via the real re-parent op.
		_, err := NewAdoptOps(d).ReparentCoordinator(ReparentInput{
			ChildOrchestratorID: child, ParentOrchestratorID: parent,
		})
		testutil.NoError(t, err)
		// Sanity: the link exists before detach.
		_, err = d.HeraLiveBindingByTaskAndOrchestrator("child-coord", parent)
		testutil.NoError(t, err)

		res, err := NewAdoptOps(d).DetachCoordinator(child)
		testutil.NoError(t, err)
		testutil.Equal(t, res.LinksRemoved, 1)

		// The parent link role + its live binding are gone.
		_, err = d.HeraLiveBindingByTaskAndOrchestrator("child-coord", parent)
		testutil.ErrorIs(t, err, db.ErrHeraNotFound)
		pRoles, err := d.ListHeraRoles(parent, true)
		testutil.NoError(t, err)
		// Only the parent's own coordinator role remains under P.
		testutil.Equal(t, len(pRoles), 1)
		testutil.Equal(t, pRoles[0].Kind, db.HeraKindCoordinator)

		// C's own coordinator role + its live binding survive untouched, and C
		// holds no live parent link — it is top-level again.
		gotCoord, err := d.HeraRole(coord.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, gotCoord.Kind, db.HeraKindCoordinator)
		testutil.Equal(t, liveBindingCount(t, d, "child-coord"), 1) // only the coord binding
	})

	t.Run("is an idempotent no-op on an already-top-level coordinator", func(t *testing.T) {
		d := memDB(t)
		child := seedOrch(t, d, "child")
		coord := seedBoundRole(t, d, child, "child", db.HeraKindCoordinator, "child-coord")

		before := liveBindingCount(t, d, "child-coord")
		res, err := NewAdoptOps(d).DetachCoordinator(child)
		testutil.NoError(t, err)
		testutil.Equal(t, res.LinksRemoved, 0) // already top-level

		// Coord role + binding untouched; a second detach is still a clean no-op.
		gotCoord, err := d.HeraRole(coord.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, gotCoord.Kind, db.HeraKindCoordinator)
		testutil.Equal(t, liveBindingCount(t, d, "child-coord"), before)
		res2, err := NewAdoptOps(d).DetachCoordinator(child)
		testutil.NoError(t, err)
		testutil.Equal(t, res2.LinksRemoved, 0)
	})

	t.Run("resolves task from the latest ended coord binding (dormant) and detaches", func(t *testing.T) {
		d := memDB(t)
		child := seedOrch(t, d, "child")
		coord := seedBoundRole(t, d, child, "child", db.HeraKindCoordinator, "child-coord")
		parent := seedOrch(t, d, "parent")
		seedBoundRole(t, d, parent, "parent", db.HeraKindCoordinator, "parent-coord")
		_, err := NewAdoptOps(d).ReparentCoordinator(ReparentInput{
			ChildOrchestratorID: child, ParentOrchestratorID: parent,
		})
		testutil.NoError(t, err)
		// C's coordinator session ends — only an ENDED coord binding remains.
		coordLive, err := d.HeraLiveBindingByRole(coord.ID)
		testutil.NoError(t, err)
		testutil.NoError(t, d.EndHeraBinding(coordLive.ID, "manual"))

		res, err := NewAdoptOps(d).DetachCoordinator(child)
		testutil.NoError(t, err)
		testutil.Equal(t, res.LinksRemoved, 1)
		_, err = d.HeraLiveBindingByTaskAndOrchestrator("child-coord", parent)
		testutil.ErrorIs(t, err, db.ErrHeraNotFound)
	})

	t.Run("rejects an unknown orchestrator", func(t *testing.T) {
		d := memDB(t)
		_, err := NewAdoptOps(d).DetachCoordinator(999)
		testutil.Contains(t, errStr(err), "no longer exists")
	})

	t.Run("rejects a coordinator with no coordinator role", func(t *testing.T) {
		d := memDB(t)
		child := seedOrch(t, d, "child") // no coord role
		_, err := NewAdoptOps(d).DetachCoordinator(child)
		testutil.Contains(t, errStr(err), "no coordinator role")
	})
}

func TestListActiveOrchestrators(t *testing.T) {
	d := memDB(t)
	a := seedOrch(t, d, "a")
	arch := seedOrch(t, d, "arch")
	testutil.NoError(t, d.ArchiveHeraOrchestrator(arch))
	ops := NewAdoptOps(d)
	got, err := ops.ListActiveOrchestrators()
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 1)
	testutil.Equal(t, got[0].ID, a)
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
