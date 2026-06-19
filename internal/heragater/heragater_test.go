package heragater

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// gaterFixture wires a Watcher to a real in-memory DB with recording fake
// materialize/ping seams.
type gaterFixture struct {
	d         *db.DB
	w         *Watcher
	mu        sync.Mutex
	mat       []*db.HeraRole // roles passed to materialize, in order
	matBranch map[int64]string
	matFail   bool // when true, materialize returns an error (HOLD-by-failure)
	pingFail  bool // when true, ping returns an error (delivery failure)
	pings     []ping
}

type ping struct {
	coord int64
	tldr  string
}

func newGaterFixture(t *testing.T) *gaterFixture {
	t.Helper()
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	f := &gaterFixture{d: d, matBranch: map[int64]string{}}
	f.w = New(d,
		func(role *db.HeraRole, taskPrompt, project, branch, backend, model string) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.matFail {
				return errors.New("materialize boom")
			}
			f.mat = append(f.mat, role)
			f.matBranch[role.ID] = branch
			// Simulate materialization: insert a binding so the node leaves the
			// planned set (matching the real CreateAndStart behaviour).
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
	return f
}

func newRunningTask(name string) *model.Task {
	return &model.Task{Name: name, Status: model.StatusInProgress, Project: "proj", Branch: "argus/" + name}
}

func (f *gaterFixture) materialized() []*db.HeraRole {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*db.HeraRole(nil), f.mat...)
}

func (f *gaterFixture) pingCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.pings)
}

// seedCoord creates an orchestrator + a coordinator role+binding (so a coord
// exists to be pinged) and returns the orchestrator id.
func (f *gaterFixture) seedCoord(t *testing.T, orchName string) int64 {
	t.Helper()
	o, err := f.d.CreateHeraOrchestrator(orchName)
	testutil.NoError(t, err)
	coordTask := &model.Task{Name: "coord-task", Status: model.StatusInProgress, Project: "proj"}
	testutil.NoError(t, f.d.Add(coordTask))
	_, _, err = f.d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: o.ID, Name: "coord", Kind: db.HeraKindCoordinator, ArgusProject: "proj",
	}, coordTask.ID, "/wt/coord")
	testutil.NoError(t, err)
	return o.ID
}

// boundWorker creates a worker role+binding at a given task status and role
// status, returning the role. This is a "blocker" that has already run.
func (f *gaterFixture) boundWorker(t *testing.T, orchID int64, name string, taskStatus model.Status, roleStatus db.HeraRoleStatusValue) *db.HeraRole {
	t.Helper()
	task := &model.Task{Name: name + "-task", Status: taskStatus, Project: "proj", Branch: "argus/" + name}
	testutil.NoError(t, f.d.Add(task))
	role, _, err := f.d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orchID, Name: name, Kind: db.HeraKindWorker, ArgusProject: "proj",
	}, task.ID, "/wt/"+name)
	testutil.NoError(t, err)
	testutil.NoError(t, f.d.UpsertHeraRoleStatus(role.ID, roleStatus))
	return role
}

func (f *gaterFixture) planned(t *testing.T, orchID int64, name string) *db.HeraRole {
	t.Helper()
	r, err := f.d.CreateHeraPlannedRole(db.CreateHeraRoleInput{
		OrchestratorID: orchID, Name: name, ArgusProject: "proj", Prompt: "do " + name,
	})
	testutil.NoError(t, err)
	return r
}

func TestGater_MaterializesWhenAllBlockersDone(t *testing.T) {
	f := newGaterFixture(t)
	orch := f.seedCoord(t, "orch")
	blocker := f.boundWorker(t, orch, "1a", model.StatusInReview, db.HeraStatusDone)
	node := f.planned(t, orch, "2a")
	testutil.NoError(t, f.d.AddHeraBlock(node.ID, blocker.ID))

	f.w.Tick()

	mat := f.materialized()
	testutil.Equal(t, len(mat), 1)
	testutil.Equal(t, mat[0].ID, node.ID)
	testutil.Equal(t, f.pingCount(), 0)
	// base_branch resolved from the blocker's branch.
	testutil.Equal(t, f.matBranch[node.ID], "argus/1a")
}

func TestGater_NodeWithNoBlockersMaterializes(t *testing.T) {
	f := newGaterFixture(t)
	orch := f.seedCoord(t, "orch")
	f.planned(t, orch, "1a")
	f.w.Tick()
	testutil.Equal(t, len(f.materialized()), 1)
}

func TestGater_StaysPlannedWhileBlockerWorking(t *testing.T) {
	f := newGaterFixture(t)
	orch := f.seedCoord(t, "orch")
	// Blocker is iterating on CI: task in_progress, role status working (not done).
	blocker := f.boundWorker(t, orch, "1a", model.StatusInProgress, db.HeraStatusWorking)
	node := f.planned(t, orch, "2a")
	testutil.NoError(t, f.d.AddHeraBlock(node.ID, blocker.ID))

	f.w.Tick()

	testutil.Equal(t, len(f.materialized()), 0) // stays planned
	testutil.Equal(t, f.pingCount(), 0)         // no failure ping for a live, working blocker

	// Node is still a planned node.
	planned, err := f.d.ListHeraPlannedNodes()
	testutil.NoError(t, err)
	found := false
	for _, p := range planned {
		if p.ID == node.ID {
			found = true
		}
	}
	testutil.Equal(t, found, true)
}

func TestGater_StaysPlannedWhenOnlySomeBlockersDone(t *testing.T) {
	f := newGaterFixture(t)
	orch := f.seedCoord(t, "orch")
	done := f.boundWorker(t, orch, "1a", model.StatusInReview, db.HeraStatusDone)
	working := f.boundWorker(t, orch, "1b", model.StatusInProgress, db.HeraStatusWorking)
	node := f.planned(t, orch, "2a")
	testutil.NoError(t, f.d.AddHeraBlock(node.ID, done.ID))
	testutil.NoError(t, f.d.AddHeraBlock(node.ID, working.ID))

	f.w.Tick()

	testutil.Equal(t, len(f.materialized()), 0)
	testutil.Equal(t, f.pingCount(), 0)
}

func TestGater_HoldsAndPingsOnCrashedBlocker(t *testing.T) {
	f := newGaterFixture(t)
	orch := f.seedCoord(t, "orch")
	// Blocker session ended (task complete) WITHOUT role status done → failed.
	crashed := f.boundWorker(t, orch, "1a", model.StatusComplete, db.HeraStatusWorking)
	node := f.planned(t, orch, "2a")
	testutil.NoError(t, f.d.AddHeraBlock(node.ID, crashed.ID))

	f.w.Tick()

	testutil.Equal(t, len(f.materialized()), 0) // held, not materialized
	testutil.Equal(t, f.pingCount(), 1)
	testutil.Equal(t, strings.Contains(f.pings[0].tldr, "held"), true)
}

func TestGater_HoldPingDedupedAcrossTicks(t *testing.T) {
	f := newGaterFixture(t)
	orch := f.seedCoord(t, "orch")
	crashed := f.boundWorker(t, orch, "1a", model.StatusComplete, db.HeraStatusIdle)
	node := f.planned(t, orch, "2a")
	testutil.NoError(t, f.d.AddHeraBlock(node.ID, crashed.ID))

	f.w.Tick()
	f.w.Tick()
	f.w.Tick()

	testutil.Equal(t, f.pingCount(), 1) // pinged once, not per-tick
	testutil.Equal(t, len(f.materialized()), 0)
}

func TestGater_FailedHoldPingRetriesNextTick(t *testing.T) {
	// A failed ping must NOT dedup the held key — the next tick retries so the
	// "hold AND notify" contract is not silently dropped by a transient failure.
	f := newGaterFixture(t)
	orch := f.seedCoord(t, "orch")
	crashed := f.boundWorker(t, orch, "1a", model.StatusComplete, db.HeraStatusWorking)
	node := f.planned(t, orch, "2a")
	testutil.NoError(t, f.d.AddHeraBlock(node.ID, crashed.ID))

	f.pingFail = true
	f.w.Tick() // ping fails → no recorded ping, key NOT marked
	testutil.Equal(t, f.pingCount(), 0)

	f.pingFail = false
	f.w.Tick() // retry succeeds
	testutil.Equal(t, f.pingCount(), 1)

	f.w.Tick() // now deduped — no repeat
	testutil.Equal(t, f.pingCount(), 1)
	testutil.Equal(t, len(f.materialized()), 0)
}

func TestGater_Idempotent_NoDoubleSpawnOnStatusFlap(t *testing.T) {
	f := newGaterFixture(t)
	orch := f.seedCoord(t, "orch")
	blocker := f.boundWorker(t, orch, "1a", model.StatusInReview, db.HeraStatusDone)
	node := f.planned(t, orch, "2a")
	testutil.NoError(t, f.d.AddHeraBlock(node.ID, blocker.ID))

	f.w.Tick() // materializes (inserts a binding for node)
	// Status flap: blocker toggles working then done again.
	testutil.NoError(t, f.d.UpsertHeraRoleStatus(blocker.ID, db.HeraStatusWorking))
	f.w.Tick()
	testutil.NoError(t, f.d.UpsertHeraRoleStatus(blocker.ID, db.HeraStatusDone))
	f.w.Tick()

	testutil.Equal(t, len(f.materialized()), 1) // exactly one spawn
}

func TestGater_HeldBlockerThatNeverRanHoldsDependent(t *testing.T) {
	// A blocker that is itself still a planned node (never bound) holds the
	// dependent — never spawn behind upstream work that hasn't run.
	f := newGaterFixture(t)
	orch := f.seedCoord(t, "orch")
	upstream := f.planned(t, orch, "1a") // never bound
	node := f.planned(t, orch, "2a")
	testutil.NoError(t, f.d.AddHeraBlock(node.ID, upstream.ID))

	f.w.Tick()

	// node is held (upstream never ran); upstream itself has no blockers → ready.
	// So upstream materializes, node does not.
	mat := f.materialized()
	testutil.Equal(t, len(mat), 1)
	testutil.Equal(t, mat[0].ID, upstream.ID)
}

func TestGater_TransitivePlannedBlockerKeepsDependentPlanned(t *testing.T) {
	// Regression for the dogfood bug: a never-materialized planned blocker is
	// PENDING, not FAILED. Chain A→B→C where A is a live, working blocker, B is a
	// planned node still waiting on A, and C is planned behind B. B has never been
	// bound. The old blockerOutcome conflated "no live binding" with "failed",
	// so C was HELD (and the coordinator pinged) the instant the tick ran — even
	// though B simply had not started yet. This broke every DAG deeper than two
	// levels. C must stay PLANNED with no ping; only B's genuine non-start gates it.
	f := newGaterFixture(t)
	orch := f.seedCoord(t, "orch")
	a := f.boundWorker(t, orch, "1a", model.StatusInProgress, db.HeraStatusWorking) // live, working
	b := f.planned(t, orch, "2a")                                                   // never bound, blocked by A
	c := f.planned(t, orch, "3a")                                                   // never bound, blocked by B
	testutil.NoError(t, f.d.AddHeraBlock(b.ID, a.ID))
	testutil.NoError(t, f.d.AddHeraBlock(c.ID, b.ID))

	f.w.Tick()

	// Nothing materializes (A is still working; B and C wait), and critically no
	// coordinator ping fires for C — a planned blocker is not a failed blocker.
	testutil.Equal(t, len(f.materialized()), 0)
	testutil.Equal(t, f.pingCount(), 0)

	// Both B and C remain planned nodes (neither held nor spawned).
	planned, err := f.d.ListHeraPlannedNodes()
	testutil.NoError(t, err)
	foundB, foundC := false, false
	for _, p := range planned {
		if p.ID == b.ID {
			foundB = true
		}
		if p.ID == c.ID {
			foundC = true
		}
	}
	testutil.Equal(t, foundB, true)
	testutil.Equal(t, foundC, true)
}

func TestGater_MissingBlockerPrunedMakesNodeReady(t *testing.T) {
	// Gater-level missing-blocker prune: a planned node whose SOLE blocker role is
	// deleted has no extant blockers (FK cascade removed the edge), so the gater
	// treats it as ready and materializes it on the next tick.
	f := newGaterFixture(t)
	orch := f.seedCoord(t, "orch")
	blocker := f.planned(t, orch, "1a") // never materialized; will be deleted
	node := f.planned(t, orch, "2a")
	testutil.NoError(t, f.d.AddHeraBlock(node.ID, blocker.ID))

	// While the blocker exists and never reached done, the node is held (not ready).
	f.w.Tick()
	matBefore := f.materialized()
	for _, r := range matBefore {
		if r.ID == node.ID {
			t.Fatal("node should be held while its sole blocker still exists unfinished")
		}
	}

	// Delete the blocker — its edge cascades away, so node has no blockers left.
	testutil.NoError(t, f.d.DeleteHeraRole(blocker.ID))

	f.w.Tick()
	found := false
	for _, r := range f.materialized() {
		if r.ID == node.ID {
			found = true
		}
	}
	testutil.Equal(t, found, true)
}

func TestGater_MaterializeFailureLeavesNodePlanned(t *testing.T) {
	f := newGaterFixture(t)
	orch := f.seedCoord(t, "orch")
	node := f.planned(t, orch, "1a")
	f.matFail = true

	f.w.Tick()

	// No binding inserted (materialize errored) → still planned.
	planned, err := f.d.ListHeraPlannedNodes()
	testutil.NoError(t, err)
	testutil.Equal(t, len(planned), 1)
	testutil.Equal(t, planned[0].ID, node.ID)
}

// TestGater_HoldNoCoordinatorNoPanic covers the holdAndPing path when no
// coordinator exists to ping (logged, no panic, no ping recorded).
func TestGater_HoldNoCoordinatorNoPanic(t *testing.T) {
	f := newGaterFixture(t)
	o, err := f.d.CreateHeraOrchestrator("nocoord")
	testutil.NoError(t, err)
	crashed := f.boundWorker(t, o.ID, "1a", model.StatusComplete, db.HeraStatusWorking)
	node := f.planned(t, o.ID, "2a")
	testutil.NoError(t, f.d.AddHeraBlock(node.ID, crashed.ID))

	f.w.Tick() // must not panic; no coordinator → no ping

	testutil.Equal(t, f.pingCount(), 0)
	testutil.Equal(t, len(f.materialized()), 0)
}

// TestGater_BaseBranchFromEndedBlockerBinding covers resolveBaseBranch's
// fallback to the latest (ended) binding when the blocker has no live binding.
func TestGater_BaseBranchFromEndedBlockerBinding(t *testing.T) {
	f := newGaterFixture(t)
	orch := f.seedCoord(t, "orch")
	task := &model.Task{Name: "1a-task", Status: model.StatusInReview, Project: "proj", Branch: "argus/1a"}
	testutil.NoError(t, f.d.Add(task))
	blocker, binding, err := f.d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch, Name: "1a", Kind: db.HeraKindWorker, ArgusProject: "proj",
	}, task.ID, "/wt/1a")
	testutil.NoError(t, err)
	testutil.NoError(t, f.d.UpsertHeraRoleStatus(blocker.ID, db.HeraStatusDone))
	testutil.NoError(t, f.d.EndHeraBinding(binding.ID, "done"))

	node := f.planned(t, orch, "2a")
	testutil.NoError(t, f.d.AddHeraBlock(node.ID, blocker.ID))

	f.w.Tick()

	mat := f.materialized()
	testutil.Equal(t, len(mat), 1)
	testutil.Equal(t, f.matBranch[node.ID], "argus/1a")
}

func TestGater_StartStop(t *testing.T) {
	f := newGaterFixture(t)
	orch := f.seedCoord(t, "orch")
	node := f.planned(t, orch, "1a")
	matched := make(chan struct{}, 1)
	f.w.SetOnMaterialize(func(*db.HeraRole) {
		select {
		case matched <- struct{}{}:
		default:
		}
	})
	f.w.SetInterval(time.Hour) // only the immediate first tick matters
	go f.w.Start()
	select {
	case <-matched:
	case <-time.After(2 * time.Second):
		t.Fatal("expected the immediate first tick to materialize the node")
	}
	f.w.Stop()
	f.w.Stop() // idempotent
	_ = node
}
