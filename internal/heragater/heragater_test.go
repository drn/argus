package heragater

import (
	"database/sql"
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
	from  int64
	coord int64
	body  string
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
			f.pings = append(f.pings, ping{from: fromRoleID, coord: coordRoleID, body: body, tldr: tldr})
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

func (f *gaterFixture) lastPing() ping {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pings[len(f.pings)-1]
}

// seedCoord creates an orchestrator + a coordinator role+binding (so a coord
// exists to be pinged) and returns the orchestrator id.
func (f *gaterFixture) seedCoord(t *testing.T, orchName string) int64 {
	t.Helper()
	o, err := f.d.CreateHeraOrchestrator(orchName, "")
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

// coordRole returns the coordinator role seedCoord created for an orchestrator.
func (f *gaterFixture) coordRole(t *testing.T, orchID int64) *db.HeraRole {
	t.Helper()
	coords, err := f.d.ListHeraRolesByKind(orchID, db.HeraKindCoordinator)
	testutil.NoError(t, err)
	testutil.Equal(t, len(coords), 1)
	return coords[0]
}

// insertLegacyBlock writes a hera_blocks edge directly, bypassing AddHeraBlock's
// validation. Used to simulate edges already present in the live DB before a new
// validation rule existed (e.g. a coordinator-as-blocker edge, which AddHeraBlock
// now rejects but the gater must still handle defensively).
func insertLegacyBlock(t *testing.T, f *gaterFixture, blockedRoleID, blockerRoleID int64) {
	t.Helper()
	err := f.d.WithTx(func(tx *sql.Tx) error {
		_, execErr := tx.Exec(
			`INSERT INTO hera_blocks (blocked_role_id, blocker_role_id, created_at) VALUES (?, ?, ?)`,
			blockedRoleID, blockerRoleID, time.Now().UTC().Format(time.RFC3339Nano))
		return execErr
	})
	testutil.NoError(t, err)
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

func TestGater_AliveCoordinatorBlockerKeepsDependentPlanned(t *testing.T) {
	// BUG-003: blocking a worker node on the coordinator role must NOT be classified
	// as a failed blocker. A coordinator's session is alive and never reaches
	// role-status done, so labelling it "failed" (session-ended-without-done) is
	// wrong. The dependent stays PLANNED (permanently pending), with NO hold-ping —
	// the old blockerOutcome held it the moment the coordinator's task left
	// in_progress (e.g. went in_review while the coordinator kept coordinating).
	f := newGaterFixture(t)
	orch := f.seedCoord(t, "orch")
	coord := f.coordRole(t, orch)
	// Move the coordinator's bound task to in_review while its binding stays LIVE —
	// the coordinator is still alive (it never "finishes"), but its task status is
	// no longer in_progress. This is the exact shape that mis-fired as "failed".
	bind, err := f.d.HeraLiveBindingByRole(coord.ID)
	testutil.NoError(t, err)
	testutil.NoError(t, f.d.SetStatus(bind.ArgusTaskID, model.StatusInReview))

	node := f.planned(t, orch, "4a-flex")
	// AddHeraBlock now REJECTS a coordinator-as-blocker edge (option a), so insert
	// the edge directly to simulate a LEGACY edge already present in the live DB
	// (the bug was observed on such data). The gater's coordinator guard is the
	// defense-in-depth that keeps this stuck-forever edge from mis-firing as failed.
	insertLegacyBlock(t, f, node.ID, coord.ID)

	f.w.Tick()

	testutil.Equal(t, len(f.materialized()), 0) // stays planned, never materializes
	testutil.Equal(t, f.pingCount(), 0)         // NO false "failed blocker" hold-ping

	// Node remains a planned node (neither held-and-pinged nor spawned).
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
	o, err := f.d.CreateHeraOrchestrator("nocoord", "")
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

// seedCoordWithBranch creates an orchestrator + a coordinator role+binding whose
// bound task carries the given branch (so a root node can resolve the
// coordinator's branch). Optionally sets an explicit orchestrator base_branch.
// Returns the orchestrator id.
func (f *gaterFixture) seedCoordWithBranch(t *testing.T, orchName, baseBranch, coordBranch string) int64 {
	t.Helper()
	o, err := f.d.CreateHeraOrchestrator(orchName, baseBranch)
	testutil.NoError(t, err)
	coordTask := &model.Task{Name: "coord-task", Status: model.StatusInProgress, Project: "proj", Branch: coordBranch}
	testutil.NoError(t, f.d.Add(coordTask))
	_, _, err = f.d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: o.ID, Name: "coord", Kind: db.HeraKindCoordinator, ArgusProject: "proj",
	}, coordTask.ID, "/wt/coord")
	testutil.NoError(t, err)
	return o.ID
}

// TestGater_RootUsesExplicitOrchestratorBase covers the delta scenario "Root node
// uses the explicit orchestrator base branch": an explicit base_branch set at
// bootstrap wins over the coordinator branch.
func TestGater_RootUsesExplicitOrchestratorBase(t *testing.T) {
	f := newGaterFixture(t)
	// Explicit base differs from the coordinator's own branch so we prove the
	// explicit override wins.
	orch := f.seedCoordWithBranch(t, "orch", "feature/explicit", "argus/coord")
	node := f.planned(t, orch, "1a")

	f.w.Tick()

	mat := f.materialized()
	testutil.Equal(t, len(mat), 1)
	testutil.Equal(t, f.matBranch[node.ID], "feature/explicit")
}

// TestGater_RootDefaultsToCoordinatorBranch covers "Root node defaults to the
// coordinator branch": no explicit base → coordinator role's bound-task branch.
func TestGater_RootDefaultsToCoordinatorBranch(t *testing.T) {
	f := newGaterFixture(t)
	orch := f.seedCoordWithBranch(t, "orch", "", "feature/coord-wip")
	node := f.planned(t, orch, "1a")

	f.w.Tick()

	mat := f.materialized()
	testutil.Equal(t, len(mat), 1)
	testutil.Equal(t, f.matBranch[node.ID], "feature/coord-wip")
}

// TestGater_RootFallsBackToProjectDefault covers "Falls back to the project
// default when no base resolves": no explicit base and no resolvable coordinator
// branch (coordinator task has empty branch) → resolveBaseBranch returns "" so
// CreateAndStart applies the project default. This is the backward-compat case,
// including a coordinator that sits on the project default branch (empty Branch).
func TestGater_RootFallsBackToProjectDefault(t *testing.T) {
	f := newGaterFixture(t)
	// seedCoord binds a coordinator task with NO branch → coordinator branch
	// unresolvable, no explicit base → "".
	orch := f.seedCoord(t, "orch")
	node := f.planned(t, orch, "1a")

	f.w.Tick()

	mat := f.materialized()
	testutil.Equal(t, len(mat), 1)
	testutil.Equal(t, f.matBranch[node.ID], "")
}

// TestGater_BlockerHavingNodeBaseUnchanged is a regression guard for the delta
// scenario "Blocker-having node base resolution is unchanged": even with an
// explicit orchestrator base AND a coordinator branch, a node that HAS a blocker
// still resolves from the most-recently-bound blocker's branch, never the root
// fallback.
func TestGater_BlockerHavingNodeBaseUnchanged(t *testing.T) {
	f := newGaterFixture(t)
	orch := f.seedCoordWithBranch(t, "orch", "feature/explicit", "argus/coord")
	blocker := f.boundWorker(t, orch, "1a", model.StatusInReview, db.HeraStatusDone)
	node := f.planned(t, orch, "2a")
	testutil.NoError(t, f.d.AddHeraBlock(node.ID, blocker.ID))

	f.w.Tick()

	mat := f.materialized()
	testutil.Equal(t, len(mat), 1)
	// Resolves from the blocker's branch, NOT the explicit/coordinator root base.
	testutil.Equal(t, f.matBranch[node.ID], "argus/1a")
}

// TestGater_ExplicitFailedBlockerHoldsWithoutSessionEnd covers the D2 gating
// half: a blocker that self-declares role-status `failed` while its session is
// STILL LIVE (task in_progress, binding live) holds its dependent immediately —
// the gater does not wait for the session to end. This exercises the explicit
// failed branch in blockerOutcome, which takes precedence over the session-death
// inference path.
func TestGater_ExplicitFailedBlockerHoldsWithoutSessionEnd(t *testing.T) {
	f := newGaterFixture(t)
	orch := f.seedCoord(t, "orch")
	// Live session (in_progress, live binding) but the worker self-reports failed.
	failed := f.boundWorker(t, orch, "1a", model.StatusInProgress, db.HeraStatusFailed)
	node := f.planned(t, orch, "2a")
	testutil.NoError(t, f.d.AddHeraBlock(node.ID, failed.ID))

	f.w.Tick()

	testutil.Equal(t, len(f.materialized()), 0) // held, not materialized
	testutil.Equal(t, f.pingCount(), 1)
	testutil.Equal(t, strings.Contains(f.lastPing().tldr, "held"), true)
	// The blocker's session never ended — its task is still in_progress.
	bt, err := f.d.HeraLiveBindingByRole(failed.ID)
	testutil.NoError(t, err)
	tk, err := f.d.Get(bt.ArgusTaskID)
	testutil.NoError(t, err)
	testutil.Equal(t, tk.Status, model.StatusInProgress)
}

// TestGater_PlannedDependentReWaitsWhenBlockerReopens covers "Planned dependents
// re-wait when a blocker reopens": a done blocker returns to working before the
// dependent materialized; the gater reads the current status and keeps the
// dependent planned (it does not materialize off the stale done).
func TestGater_PlannedDependentReWaitsWhenBlockerReopens(t *testing.T) {
	f := newGaterFixture(t)
	orch := f.seedCoord(t, "orch")
	blocker := f.boundWorker(t, orch, "1a", model.StatusInProgress, db.HeraStatusDone)
	node := f.planned(t, orch, "2a")
	testutil.NoError(t, f.d.AddHeraBlock(node.ID, blocker.ID))

	// Blocker reopens to working BEFORE the dependent materializes.
	testutil.NoError(t, f.d.UpsertHeraRoleStatus(blocker.ID, db.HeraStatusWorking))

	f.w.Tick()

	testutil.Equal(t, len(f.materialized()), 0) // does not materialize off stale done
	testutil.Equal(t, f.pingCount(), 0)         // working blocker → planned, not held

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

// TestGater_HeldDedupClearsOnRecoveryAndReArms covers the re-arm contract: a held
// node behind a failed blocker pings once; when the blocker recovers the dedup
// clears (and one recovery notice fires); a subsequent re-failure pings AGAIN.
func TestGater_HeldDedupClearsOnRecoveryAndReArms(t *testing.T) {
	f := newGaterFixture(t)
	orch := f.seedCoord(t, "orch")
	blocker := f.boundWorker(t, orch, "1a", model.StatusInProgress, db.HeraStatusFailed)
	node := f.planned(t, orch, "2a")
	testutil.NoError(t, f.d.AddHeraBlock(node.ID, blocker.ID))

	f.w.Tick() // held → ping #1
	testutil.Equal(t, f.pingCount(), 1)
	testutil.Equal(t, strings.Contains(f.lastPing().tldr, "held"), true)

	// Recover: blocker flips back to working. The re-arm sweep clears the dedup and
	// emits exactly one recovery notice.
	testutil.NoError(t, f.d.UpsertHeraRoleStatus(blocker.ID, db.HeraStatusWorking))
	f.w.Tick() // recovery notice → ping #2 (unblocked)
	testutil.Equal(t, f.pingCount(), 2)
	testutil.Equal(t, strings.Contains(f.lastPing().tldr, "unblocked"), true)

	// A tick with no change emits nothing more (notice was one-time).
	f.w.Tick()
	testutil.Equal(t, f.pingCount(), 2)

	// Re-fail: the dedup re-armed, so this pings AGAIN (held).
	testutil.NoError(t, f.d.UpsertHeraRoleStatus(blocker.ID, db.HeraStatusFailed))
	f.w.Tick() // held → ping #3
	testutil.Equal(t, f.pingCount(), 3)
	testutil.Equal(t, strings.Contains(f.lastPing().tldr, "held"), true)
}

// TestGater_RecoveryNoticeAddressedToCoordinatorFromNode covers "Recovery clears
// the dedup and notifies once": EXACTLY ONE unblocked notice, addressed to the
// coordinator role and sent FROM the held node's own role (self-send-safe).
func TestGater_RecoveryNoticeAddressedToCoordinatorFromNode(t *testing.T) {
	f := newGaterFixture(t)
	orch := f.seedCoord(t, "orch")
	coord := f.coordRole(t, orch)
	blocker := f.boundWorker(t, orch, "1a", model.StatusInProgress, db.HeraStatusFailed)
	node := f.planned(t, orch, "2a")
	testutil.NoError(t, f.d.AddHeraBlock(node.ID, blocker.ID))

	f.w.Tick() // held ping
	testutil.Equal(t, f.pingCount(), 1)

	// Recover.
	testutil.NoError(t, f.d.UpsertHeraRoleStatus(blocker.ID, db.HeraStatusDone))
	f.w.Tick()

	testutil.Equal(t, f.pingCount(), 2) // exactly one recovery notice on top of the hold
	recovery := f.lastPing()
	testutil.Equal(t, strings.Contains(recovery.tldr, "unblocked"), true)
	testutil.Equal(t, recovery.coord, coord.ID) // addressed to the coordinator
	testutil.Equal(t, recovery.from, node.ID)   // sent from the held node's own role
	// Never to itself (self-send guard would reject from==to).
	testutil.Equal(t, recovery.from != recovery.coord, true)
}

// TestGater_NoRecoveryNoticeForMaterializedNode covers the Non-Goals case: a node
// that ALREADY MATERIALIZED (has a binding) whose blocker later reopens gets NO
// notice — a running worker cannot be un-spawned. The held key is cleared
// silently when the node leaves the planned set.
func TestGater_NoRecoveryNoticeForMaterializedNode(t *testing.T) {
	f := newGaterFixture(t)
	orch := f.seedCoord(t, "orch")
	blocker := f.boundWorker(t, orch, "1a", model.StatusInProgress, db.HeraStatusFailed)
	node := f.planned(t, orch, "2a")
	testutil.NoError(t, f.d.AddHeraBlock(node.ID, blocker.ID))

	f.w.Tick() // node held behind failed blocker → ping #1
	testutil.Equal(t, f.pingCount(), 1)

	// Materialize the node manually (give it a binding) so it leaves the planned
	// set — simulating the coordinator force-spawning it past the hold.
	tk := newRunningTask(node.Name)
	testutil.NoError(t, f.d.Add(tk))
	_, err := f.d.CreateHeraBinding(db.CreateHeraBindingInput{
		RoleID: node.ID, OrchestratorID: node.OrchestratorID,
		ArgusTaskID: tk.ID, WorktreePath: "/wt/" + node.Name,
	})
	testutil.NoError(t, err)

	// Now flip the blocker around (recover then re-fail). Because the node is no
	// longer planned, the re-arm sweep clears the key SILENTLY — zero new pings.
	testutil.NoError(t, f.d.UpsertHeraRoleStatus(blocker.ID, db.HeraStatusWorking))
	f.w.Tick()
	testutil.NoError(t, f.d.UpsertHeraRoleStatus(blocker.ID, db.HeraStatusFailed))
	f.w.Tick()

	testutil.Equal(t, f.pingCount(), 1) // no recovery notice, no re-held ping
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
