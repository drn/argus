package heragater

// Stage 1 failing tests for sub-coordinator gater routing
// (add-hera-subcoord-nodes).
//
// Planned API referenced (does not exist yet — tests will fail to compile):
//   - db.HeraRole.NodeKind field (HeraNodeKindSubCoord / HeraNodeKindWorker)
//   - db.CreateHeraRoleInput.NodeKind field
//   - Watcher.SetSubCoordMaterializer(func) — a second seam for the subcoord path
//   - db.HeraNodeKindSubCoord / db.HeraNodeKindWorker constants
//
// All tests in this file fail to compile (or fail at assertion) until Stage 3
// adds the above. The gater fixture's materialize callback records which path was
// taken, keyed on whether the seam called was the worker or subcoord materializer.

import (
	"errors"
	"sync"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// extendedGaterFixture extends gaterFixture with a sub-coordinator materializer
// seam. The base fixture's Materializer is still used for worker nodes;
// SetSubCoordMaterializer routes subcoord nodes to this seam.
type extendedGaterFixture struct {
	gaterFixture
	scMu           sync.Mutex
	scMat          []*db.HeraRole // subcoord nodes materialized via the subcoord seam
	scMatFail      bool
	workerMatCalls int // tracks calls to the WORKER materializer to verify no cross-routing
}

func newExtendedGaterFixture(t *testing.T) *extendedGaterFixture {
	t.Helper()
	f := &extendedGaterFixture{
		gaterFixture: *newGaterFixture(t),
	}
	// Override the base watcher with a fresh one that includes both seams.
	// The base materialize seam records worker materializations.
	d := f.gaterFixture.d
	f.w = New(d,
		func(role *db.HeraRole, taskPrompt, project, branch, backend, mdl string) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.matFail {
				return errors.New("worker materialize boom")
			}
			f.workerMatCalls++
			f.mat = append(f.mat, role)
			f.matBranch[role.ID] = branch
			// Simulate: insert a binding so the node leaves the planned set.
			tk := newRunningTask(role.Name)
			_ = d.Add(tk)
			_, _ = d.CreateHeraBinding(db.CreateHeraBindingInput{
				RoleID: role.ID, OrchestratorID: role.OrchestratorID,
				ArgusTaskID: tk.ID, WorktreePath: "/wt/" + role.Name,
			})
			return nil
		},
		func(fromRoleID, coordRoleID int64, body, tldr string) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.pingFail {
				return errors.New("ping boom")
			}
			f.pings = append(f.pings, ping{coord: coordRoleID, tldr: tldr})
			return nil
		},
	)
	// Register the subcoord seam on the watcher.
	f.w.SetSubCoordMaterializer(func(role *db.HeraRole, taskPrompt, project, branch, backend, mdl string) error {
		f.scMu.Lock()
		defer f.scMu.Unlock()
		if f.scMatFail {
			return errors.New("subcoord materialize boom")
		}
		f.scMat = append(f.scMat, role)
		// Simulate: insert a binding so the node leaves the planned set.
		tk := newRunningTask("sc-" + role.Name)
		_ = d.Add(tk)
		_, _ = d.CreateHeraBinding(db.CreateHeraBindingInput{
			RoleID: role.ID, OrchestratorID: role.OrchestratorID,
			ArgusTaskID: tk.ID, WorktreePath: "/wt/sc-" + role.Name,
		})
		return nil
	})
	return f
}

func (f *extendedGaterFixture) subCoordMaterialized() []*db.HeraRole {
	f.scMu.Lock()
	defer f.scMu.Unlock()
	return append([]*db.HeraRole(nil), f.scMat...)
}

// seedSubCoordNode creates a planned role with NodeKind=subcoord under an orch.
func (f *extendedGaterFixture) seedSubCoordNode(t *testing.T, orchID int64, name string) *db.HeraRole {
	t.Helper()
	r, err := f.d.CreateHeraPlannedRole(db.CreateHeraRoleInput{
		OrchestratorID: orchID, Name: name, ArgusProject: "proj",
		Prompt:   "goal: " + name,
		NodeKind: db.HeraNodeKindSubCoord,
	})
	testutil.NoError(t, err)
	return r
}

// TestSubCoord_GaterRoutesSubCoordToCoordinatorPath covers:
//   - "Gater materializes a sub-coordinator node as a distinct coordinator agent"
//     (routing aspect)
//   - A ready subcoord node calls SetSubCoordMaterializer's seam, NOT the worker seam.
func TestSubCoord_GaterRoutesSubCoordToCoordinatorPath(t *testing.T) {
	f := newExtendedGaterFixture(t)
	orch := f.seedCoord(t, "orch")
	subCoordNode := f.seedSubCoordNode(t, orch, "3a-auth")

	f.w.Tick()

	// The subcoord materializer was called.
	sc := f.subCoordMaterialized()
	testutil.Equal(t, len(sc), 1)
	testutil.Equal(t, sc[0].ID, subCoordNode.ID)

	// The WORKER materializer was NOT called for the subcoord node.
	testutil.Equal(t, f.workerMatCalls, 0)
}

// TestSubCoord_GaterRoutesWorkerNodeToWorkerPath covers:
//   - Default (worker/absent kind) behaviour unchanged
//   - A ready worker node still calls the worker seam, not the subcoord seam.
func TestSubCoord_GaterRoutesWorkerNodeToWorkerPath(t *testing.T) {
	f := newExtendedGaterFixture(t)
	orch := f.seedCoord(t, "orch")
	workerNode := f.planned(t, orch, "2a-worker") // NodeKind defaults to worker

	f.w.Tick()

	// Worker materializer was called.
	testutil.Equal(t, f.workerMatCalls, 1)
	mat := f.materialized()
	testutil.Equal(t, len(mat), 1)
	testutil.Equal(t, mat[0].ID, workerNode.ID)

	// Subcoord materializer was NOT called.
	testutil.Equal(t, len(f.subCoordMaterialized()), 0)
}

// TestSubCoord_GaterIdempotentSubCoordAlreadyBound covers:
//   - "Materialization is idempotent — already-bound subcoord not re-materialized"
//   - A subcoord node that already has a binding is not materialized again.
func TestSubCoord_GaterIdempotentSubCoordAlreadyBound(t *testing.T) {
	f := newExtendedGaterFixture(t)
	orch := f.seedCoord(t, "orch")
	node := f.seedSubCoordNode(t, orch, "3a-auth")

	f.w.Tick() // materializes the node (inserts a binding)

	// The node now has a binding — it leaves the planned set.
	f.w.Tick()
	f.w.Tick()

	// Exactly one spawn — no double-spawn on repeated ticks.
	testutil.Equal(t, len(f.subCoordMaterialized()), 1)
}

// TestSubCoord_GaterMixedPlanWorkerAndSubCoord verifies that a plan containing
// both worker and subcoord nodes routes each to its correct materializer path.
func TestSubCoord_GaterMixedPlanWorkerAndSubCoord(t *testing.T) {
	f := newExtendedGaterFixture(t)
	orch := f.seedCoord(t, "orch")

	workerNode := f.planned(t, orch, "1a-research") // worker (default)
	scNode := f.seedSubCoordNode(t, orch, "2a-auth-team")
	// 2a is blocked by 1a — worker must complete first.
	testutil.NoError(t, f.d.AddHeraBlock(scNode.ID, workerNode.ID))

	// Tick 1: worker has no blockers, materializes. Subcoord is still blocked.
	f.w.Tick()
	testutil.Equal(t, f.workerMatCalls, 1)
	testutil.Equal(t, len(f.subCoordMaterialized()), 0)

	// Mark worker done so the subcoord becomes ready.
	workerBindings, err := f.d.ListHeraBindingsByRole(workerNode.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(workerBindings) > 0, true)
	testutil.NoError(t, f.d.UpsertHeraRoleStatus(workerNode.ID, db.HeraStatusDone))

	// Tick 2: subcoord is now ready, routes to subcoord materializer.
	f.w.Tick()
	testutil.Equal(t, f.workerMatCalls, 1)  // no extra worker materializations
	testutil.Equal(t, len(f.subCoordMaterialized()), 1)
	testutil.Equal(t, f.subCoordMaterialized()[0].ID, scNode.ID)
}

// TestSubCoord_GaterWorkerRoleDoneGatesParentDependents covers:
//   - "Worker-role status still gates the parent's dependents"
//   - After a subcoord node materializes, its worker-role status reaching `done`
//     unblocks the parent's dependent that was waiting on it.
func TestSubCoord_GaterWorkerRoleDoneGatesParentDependents(t *testing.T) {
	f := newExtendedGaterFixture(t)
	orch := f.seedCoord(t, "orch")

	// Plan: scNode → deployNode. scNode is a subcoord; deployNode is a worker.
	scNode := f.seedSubCoordNode(t, orch, "2a-auth-team")
	deployNode := f.planned(t, orch, "3a-deploy")
	testutil.NoError(t, f.d.AddHeraBlock(deployNode.ID, scNode.ID))

	// Tick 1: scNode has no blockers → materializes via subcoord path.
	f.w.Tick()
	testutil.Equal(t, len(f.subCoordMaterialized()), 1)
	testutil.Equal(t, f.workerMatCalls, 0)
	// deployNode is still blocked (scNode's worker role not done yet).
	testutil.Equal(t, len(f.materialized()), 0)

	// Mark the sub-coord's WORKER role in the parent as done.
	// (The gater gates on the hera ROLE status of the node in the PARENT orch.)
	testutil.NoError(t, f.d.UpsertHeraRoleStatus(scNode.ID, db.HeraStatusDone))

	// Tick 2: deployNode's blocker (scNode worker role) is now done → materializes.
	f.w.Tick()
	testutil.Equal(t, f.workerMatCalls, 1)
	mat := f.materialized()
	testutil.Equal(t, len(mat), 1)
	testutil.Equal(t, mat[0].ID, deployNode.ID)
}

// TestSubCoord_GaterSubCoordFailedMaterializeStaysPlanned verifies that when
// the subcoord materializer returns an error, the node stays planned (no binding)
// and the next tick retries — same contract as the worker path.
func TestSubCoord_GaterSubCoordFailedMaterializeStaysPlanned(t *testing.T) {
	f := newExtendedGaterFixture(t)
	orch := f.seedCoord(t, "orch")
	node := f.seedSubCoordNode(t, orch, "3a-auth")
	f.scMatFail = true

	f.w.Tick()

	// No binding inserted (materialize errored) → still planned.
	planned, err := f.d.ListHeraPlannedNodes()
	testutil.NoError(t, err)
	testutil.Equal(t, len(planned), 1)
	testutil.Equal(t, planned[0].ID, node.ID)
}

// TestSubCoord_GaterSubCoordNotAWorkerNodeInParentView covers:
//   - Confirm the planned node (subcoord kind) still appears in ListHeraPlannedNodes
//     before materialization (gater picks it up correctly).
func TestSubCoord_GaterSubCoordAppearsInPlannedSet(t *testing.T) {
	f := newExtendedGaterFixture(t)
	orch := f.seedCoord(t, "orch")
	node := f.seedSubCoordNode(t, orch, "3a-auth")

	planned, err := f.d.ListHeraPlannedNodes()
	testutil.NoError(t, err)
	testutil.Equal(t, len(planned), 1)
	testutil.Equal(t, planned[0].ID, node.ID)
	// The gater can inspect the NodeKind to branch on the materialize path.
	testutil.Equal(t, planned[0].NodeKind, db.HeraNodeKindSubCoord)
}

// TestSubCoord_GaterHoldsSubCoordBehindFailedBlocker verifies that the hold-and-
// ping logic applies identically to subcoord nodes (the hold decision is based on
// blockers, not the node's own kind).
func TestSubCoord_GaterHoldsSubCoordBehindFailedBlocker(t *testing.T) {
	f := newExtendedGaterFixture(t)
	orch := f.seedCoord(t, "orch")
	// A worker blocker that ran and ended without done.
	crashed := f.boundWorker(t, orch, "1a", model.StatusComplete, db.HeraStatusWorking)
	// A subcoord node blocked behind it.
	scNode := f.seedSubCoordNode(t, orch, "2a-auth")
	testutil.NoError(t, f.d.AddHeraBlock(scNode.ID, crashed.ID))

	f.w.Tick()

	// Node held — neither worker nor subcoord materializer called.
	testutil.Equal(t, f.workerMatCalls, 0)
	testutil.Equal(t, len(f.subCoordMaterialized()), 0)
	// Coordinator was pinged about the hold.
	testutil.Equal(t, f.pingCount(), 1)
}
