package hera

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// --- recycle_coord (add-coordinator-context-management) ---
//
// These tests pin the `coordinator-context-management` delta spec's
// "recycle_coord restarts a coordinator on its existing task without losing
// its place" requirement (design.md D5). None of RecycleCoord, RecycleTrigger,
// RecycleRunner, or BuildRecycleSeedPrompt exist yet (Stage 6) — this file
// fails to compile until then, proving the gap per tasks.md 1.4.
//
// RecycleRunner is the proposed daemon-side seam: the actual PTY kill/restart
// and idle/stray-job checks are injected (function-field style, per
// context/knowledge/testing.md) so RecycleCoord's gating logic — which trigger
// waits for idleness, which cleans up stray jobs before restarting — is
// testable without a real PTY or `claude agents` registry. RecycleStore is the
// narrow DB surface RecycleCoord/BuildRecycleSeedPrompt need, satisfied by the
// real *db.DB (mirroring the Store interface in service.go), so seed-prompt
// assembly is exercised against real hera fixtures rather than a mock.
//
// This is a reasonable proposal for the Stage 6 contract, not a mandate — the
// exact shape may be adjusted as long as the five scenarios below (same task
// survives, self-service waits for idle, human-forced does not wait, seed
// prompt needs no follow-up tool calls, stray job cleaned up before restart)
// keep passing.

// fakeRecycleRunner records calls so tests can assert ordering and gating
// without a real session/PTY.
type fakeRecycleRunner struct {
	idle          bool
	restartCalled bool
	restartTaskID string
	restartRoleID int64
	stopStrayErr  error
	restartErr    error
	calls         []string
}

func (f *fakeRecycleRunner) IsIdle(taskID string) bool { return f.idle }

func (f *fakeRecycleRunner) StopStrayJobs(taskID, sessionID string) error {
	f.calls = append(f.calls, "stop_stray")
	return f.stopStrayErr
}

func (f *fakeRecycleRunner) Restart(taskID string, roleID int64) error {
	f.calls = append(f.calls, "restart")
	f.restartCalled = true
	f.restartTaskID = taskID
	f.restartRoleID = roleID
	return f.restartErr
}

// seedRecycleCoordinator creates an orchestrator + bound coordinator role on a
// real task row, returning the task, orchestrator, and role for use by the
// tests below.
func seedRecycleCoordinator(t *testing.T, d *db.DB, orchName, taskWorktree, taskBranch, mission string) (*model.Task, *db.HeraOrchestrator, *db.HeraRole) {
	t.Helper()
	return seedRecycleRole(t, d, db.HeraKindCoordinator, orchName, "coord", taskWorktree, taskBranch, mission)
}

// seedRecycleRole creates an orchestrator + bound role of the given kind on a
// real task row, returning the task, orchestrator, and role for use by the
// tests below. Parameterized on kind (add-worker-bounce) so worker/freelance
// self-service recycle coverage can reuse the same fixture shape as the
// coordinator-only tests this file started with.
func seedRecycleRole(t *testing.T, d *db.DB, kind db.HeraRoleKind, orchName, roleName, taskWorktree, taskBranch, mission string) (*model.Task, *db.HeraOrchestrator, *db.HeraRole) {
	t.Helper()
	task := &model.Task{
		Name:     roleName + "-task",
		Status:   model.StatusInProgress,
		Project:  "test-project",
		Worktree: taskWorktree,
		Branch:   taskBranch,
	}
	testutil.NoError(t, d.Add(task))

	orch, err := d.CreateHeraOrchestrator(orchName, "master")
	testutil.NoError(t, err)

	role, _, err := d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID,
		Name:           roleName,
		Kind:           kind,
		ArgusProject:   task.Project,
		Prompt:         mission,
	}, task.ID, task.Worktree)
	testutil.NoError(t, err)

	return task, orch, role
}

func TestRecycleCoord_SameTaskWorktreeBranchBindingSurvives(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	task, _, role := seedRecycleCoordinator(t, d, "myorch", "/wt/coord", "argus/coord-branch", "you are the coordinator")
	runner := &fakeRecycleRunner{idle: true}

	testutil.NoError(t, RecycleCoord(d, runner, role.ID, "sess-1", RecycleHumanForced))

	got, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.ID, task.ID)
	testutil.Equal(t, got.Worktree, "/wt/coord")
	testutil.Equal(t, got.Branch, "argus/coord-branch")

	binding, err := d.HeraLiveBindingByRole(role.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, binding.ArgusTaskID, task.ID)
	testutil.Equal(t, binding.WorktreePath, "/wt/coord")

	// The restart must target the SAME task row — no new task/worktree minted.
	testutil.Equal(t, runner.restartTaskID, task.ID)
}

func TestRecycleCoord_SelfService_WaitsForIdle(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	_, _, role := seedRecycleCoordinator(t, d, "myorch", "/wt/coord", "argus/coord-branch", "mission")
	runner := &fakeRecycleRunner{idle: false}

	testutil.NoError(t, RecycleCoord(d, runner, role.ID, "sess-1", RecycleSelfService))
	testutil.Equal(t, runner.restartCalled, false)

	// Next tick of the background watcher: session has gone idle.
	runner.idle = true
	testutil.NoError(t, RecycleCoord(d, runner, role.ID, "sess-1", RecycleSelfService))
	testutil.Equal(t, runner.restartCalled, true)
}

func TestRecycleCoord_HumanForced_DoesNotWaitForIdle(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	_, _, role := seedRecycleCoordinator(t, d, "myorch", "/wt/coord", "argus/coord-branch", "mission")
	runner := &fakeRecycleRunner{idle: false} // actively producing output

	testutil.NoError(t, RecycleCoord(d, runner, role.ID, "sess-1", RecycleHumanForced))
	testutil.Equal(t, runner.restartCalled, true)
}

func TestRecycleCoord_StrayJobStoppedBeforeRestart(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	_, _, role := seedRecycleCoordinator(t, d, "myorch", "/wt/coord", "argus/coord-branch", "mission")
	runner := &fakeRecycleRunner{idle: true}

	testutil.NoError(t, RecycleCoord(d, runner, role.ID, "sess-1", RecycleHumanForced))

	testutil.DeepEqual(t, runner.calls, []string{"stop_stray", "restart"})
}

func TestBuildRecycleSeedPrompt_ComposesMissionPlanStateAndHandoffNote(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	task, orch, role := seedRecycleCoordinator(t, d, "myorch", "/wt/coord", "argus/coord-branch",
		"You are the coordinator for the config-management rollout.")

	// A worker under the same orchestrator, with a live status, contributes to
	// the "current plan-DAG node states" half of the seed.
	w := &model.Task{Name: "worker-task", Status: model.StatusInProgress, Project: "test-project", Worktree: "/wt/w1"}
	testutil.NoError(t, d.Add(w))
	workerRole, _, err := d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID,
		Name:           "w1",
		Kind:           db.HeraKindWorker,
		ArgusProject:   w.Project,
	}, w.ID, w.Worktree)
	testutil.NoError(t, err)
	testutil.NoError(t, d.UpsertHeraRoleStatus(workerRole.ID, db.HeraStatusWorking))

	testutil.NoError(t, d.SetMeta(task.ID, db.HeraMetaNamespace, "handoff_note", "watch the fan-in reconciliation"))

	prompt, err := BuildRecycleSeedPrompt(d, role.ID)
	testutil.NoError(t, err)

	testutil.Contains(t, prompt, "You are the coordinator for the config-management rollout.")
	testutil.Contains(t, prompt, "w1")
	testutil.Contains(t, prompt, string(db.HeraStatusWorking))
	testutil.Contains(t, prompt, "watch the fan-in reconciliation")

	// The original mission must be explicitly marked as historical, and the
	// current plan-DAG state / handoff note must precede it in the assembled
	// text — a fresh coordinator reads "what's actually going on" before "what
	// I was originally asked," so it doesn't anchor on a stale mission as its
	// live instruction (the BUG this ordering fixes).
	testutil.Contains(t, prompt, "do NOT treat this as your current instruction")

	missionIdx := strings.Index(prompt, "You are the coordinator for the config-management rollout.")
	planStateIdx := strings.Index(prompt, "## Current plan-DAG / role state")
	handoffIdx := strings.Index(prompt, "## Handoff note from your prior session")
	historicalMarkerIdx := strings.Index(prompt, "do NOT treat this as your current instruction")

	testutil.Equal(t, true, planStateIdx < missionIdx)
	testutil.Equal(t, true, handoffIdx < missionIdx)
	testutil.Equal(t, true, historicalMarkerIdx < missionIdx)
}

// TestBuildRecycleSeedPrompt_WorkerRoleWordingNotCoordinatorSpecific pins
// design.md D4 (add-worker-bounce): the seed prompt's opening line must not
// assume the recycled role is a coordinator when seeding a worker (or
// freelance) role's fresh session.
func TestBuildRecycleSeedPrompt_WorkerRoleWordingNotCoordinatorSpecific(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	_, _, role := seedRecycleRole(t, d, db.HeraKindWorker, "myorch", "w1", "/wt/w1", "argus/w1-branch",
		"You are the worker implementing the config-management rollout.")

	prompt, err := BuildRecycleSeedPrompt(d, role.ID)
	testutil.NoError(t, err)

	testutil.Contains(t, prompt, "You are the worker implementing the config-management rollout.")
	testutil.Contains(t, prompt, "do NOT treat this as your current instruction")

	if strings.Contains(prompt, "prior coordinator session") {
		t.Fatalf("seed prompt still uses coordinator-specific wording for a worker role: %q", prompt)
	}
}
