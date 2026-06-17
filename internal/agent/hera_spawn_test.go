package agent

import (
	"errors"
	"strings"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
)

func TestDeriveHeraWorkerName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Fix the login bug", "fix-the-login-bug"},
		{"", "worker"},
		{"!!!", "worker"},
		{"  Spaces  here ", "spaces-here"},
		{"this prompt is definitely much longer than forty characters total", "this-prompt-is-definitely-much-longer-th"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			testutil.Equal(t, DeriveHeraWorkerName(c.in), c.want)
		})
	}
}

func TestHeraWorkerOrientation_NamesOrchAndCoord(t *testing.T) {
	got := HeraWorkerOrientation("my-orch", "coord")
	testutil.Equal(t, strings.Contains(got, `"my-orch"`), true)
	testutil.Equal(t, strings.Contains(got, `"coord"`), true)
}

func TestHeraCoordinatorOrientation_NamesOrch(t *testing.T) {
	got := HeraCoordinatorOrientation("my-orch")
	testutil.Equal(t, strings.Contains(got, `"my-orch"`), true)
}

// TestSpawnHeraCoordinator_HappyPath drives the root-coordinator spawner: it
// creates a fresh orchestrator + coordinator role + binding to a new task,
// stamps meta:hera.role=coordinator, and carries the model override.
func TestSpawnHeraCoordinator_HappyPath(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{sessionPID: 4242}

	res, err := SpawnHeraCoordinator(d, fr, HeraCoordinatorSpawnInput{
		OrchestratorBaseName: "ship-feature",
		TaskPrompt:           "oriented body",
		RolePrompt:           "verbatim user prompt",
		Project:              "proj",
		Model:                "opus",
	})
	testutil.NoError(t, err)

	testutil.Equal(t, res.Orchestrator.Name, "ship-feature")
	testutil.Equal(t, res.Role.Name, "coord")
	testutil.Equal(t, res.Role.Kind, db.HeraKindCoordinator)
	testutil.Equal(t, res.Role.Prompt, "verbatim user prompt")
	testutil.Equal(t, res.Binding.ArgusTaskID, res.Task.ID)
	testutil.Equal(t, fr.startCalls, 1)

	// Task persisted with the oriented prompt body + model override.
	got, err := d.Get(res.Task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Prompt, "oriented body")
	testutil.Equal(t, got.Model, "opus")
	testutil.Equal(t, got.Name, "ship-feature")

	// Live coordinator binding under the new orchestrator.
	bnd, err := d.HeraLiveBindingByTaskAndOrchestrator(res.Task.ID, res.Orchestrator.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, bnd.ID, res.Binding.ID)

	// meta:hera.role=coordinator stamped.
	meta, _ := d.ListMeta(res.Task.ID, db.HeraMetaNamespace)
	found := false
	for _, e := range meta {
		if e.Key == db.HeraMetaKeyRole && e.Value == string(db.HeraKindCoordinator) {
			found = true
		}
	}
	testutil.Equal(t, found, true)
}

// TestSpawnHeraCoordinator_DeCollidesOrchName verifies a second spawn with the
// same base name lands on base-2 (the orchestrator-name de-collide), creating a
// genuinely new orchestrator rather than re-fetching the existing one.
func TestSpawnHeraCoordinator_DeCollidesOrchName(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{}

	first, err := SpawnHeraCoordinator(d, fr, HeraCoordinatorSpawnInput{
		OrchestratorBaseName: "dup", TaskPrompt: "b", Project: "proj",
	})
	testutil.NoError(t, err)
	testutil.Equal(t, first.Orchestrator.Name, "dup")

	second, err := SpawnHeraCoordinator(d, fr, HeraCoordinatorSpawnInput{
		OrchestratorBaseName: "dup", TaskPrompt: "b", Project: "proj",
	})
	testutil.NoError(t, err)
	testutil.Equal(t, second.Orchestrator.Name, "dup-2")
	if first.Orchestrator.ID == second.Orchestrator.ID {
		t.Fatal("expected two distinct orchestrators")
	}
}

// TestSpawnHeraCoordinator_StartFailureUnwinds forces runner.Start to fail and
// asserts NO orphan orchestrator / role / binding / task is left behind.
func TestSpawnHeraCoordinator_StartFailureUnwinds(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{startErr: errors.New("boom")}

	beforeTasks, _ := d.Tasks()
	beforeOrchs, _ := d.ListHeraOrchestrators(true)

	_, err := SpawnHeraCoordinator(d, fr, HeraCoordinatorSpawnInput{
		OrchestratorBaseName: "doomed", TaskPrompt: "b", Project: "proj",
	})
	if err == nil {
		t.Fatal("expected spawn to fail when runner.Start errors")
	}

	afterTasks, _ := d.Tasks()
	testutil.Equal(t, len(afterTasks), len(beforeTasks))
	afterOrchs, _ := d.ListHeraOrchestrators(true)
	testutil.Equal(t, len(afterOrchs), len(beforeOrchs))
	live, err := d.ListHeraLiveBindings()
	testutil.NoError(t, err)
	testutil.Equal(t, len(live), 0)
}

// TestSpawnHeraWorker_HappyPath drives the shared transactional spawner: it
// creates the task (worktree + echo session), stamps meta:hera.role=worker,
// uniquifies the role name, and writes the role+binding.
func TestSpawnHeraWorker_HappyPath(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{sessionPID: 4242}
	orch, err := d.CreateHeraOrchestrator("orch")
	testutil.NoError(t, err)

	res, err := SpawnHeraWorker(d, fr, HeraWorkerSpawnInput{
		OrchestratorID: orch.ID,
		BaseName:       "do-thing",
		TaskPrompt:     "oriented body",
		RolePrompt:     "verbatim user prompt",
		Project:        "proj",
	})
	testutil.NoError(t, err)

	testutil.Equal(t, res.Role.Name, "do-thing")
	testutil.Equal(t, res.Role.Kind, db.HeraKindWorker)
	testutil.Equal(t, res.Role.Prompt, "verbatim user prompt")
	testutil.Equal(t, res.Binding.ArgusTaskID, res.Task.ID)
	testutil.Equal(t, fr.startCalls, 1)

	// Task persisted with the oriented prompt body.
	got, err := d.Get(res.Task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Prompt, "oriented body")

	// Live binding under the orchestrator.
	bnd, err := d.HeraLiveBindingByTaskAndOrchestrator(res.Task.ID, orch.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, bnd.ID, res.Binding.ID)

	// meta:hera.role=worker stamped.
	meta, _ := d.ListMeta(res.Task.ID, db.HeraMetaNamespace)
	found := false
	for _, e := range meta {
		if e.Key == db.HeraMetaKeyRole && e.Value == string(db.HeraKindWorker) {
			found = true
		}
	}
	testutil.Equal(t, found, true)
}

// TestSpawnHeraWorker_ModelPropagates asserts a per-worker Model override flows
// into CreateInput and is persisted on the spawned task row (the per-task model
// that ResolveModel/BuildCmd later inject as --model at session start).
func TestSpawnHeraWorker_ModelPropagates(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{}
	orch, err := d.CreateHeraOrchestrator("orch")
	testutil.NoError(t, err)

	res, err := SpawnHeraWorker(d, fr, HeraWorkerSpawnInput{
		OrchestratorID: orch.ID,
		BaseName:       "modelled",
		TaskPrompt:     "body",
		Project:        "proj",
		Model:          "opus",
	})
	testutil.NoError(t, err)

	got, err := d.Get(res.Task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Model, "opus")
}

// TestSpawnHeraWorker_EmptyModelDefaults asserts an unset Model leaves the task
// model empty (backend default — no --model injection).
func TestSpawnHeraWorker_EmptyModelDefaults(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{}
	orch, err := d.CreateHeraOrchestrator("orch")
	testutil.NoError(t, err)

	res, err := SpawnHeraWorker(d, fr, HeraWorkerSpawnInput{
		OrchestratorID: orch.ID,
		BaseName:       "plain",
		TaskPrompt:     "body",
		Project:        "proj",
	})
	testutil.NoError(t, err)

	got, err := d.Get(res.Task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Model, "")
}

// TestSpawnHeraWorker_UniquifiesName verifies a second spawn of the same base
// name lands on base-2 (the role-name uniquifier).
func TestSpawnHeraWorker_UniquifiesName(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{}
	orch, err := d.CreateHeraOrchestrator("orch")
	testutil.NoError(t, err)

	first, err := SpawnHeraWorker(d, fr, HeraWorkerSpawnInput{
		OrchestratorID: orch.ID, BaseName: "dup", TaskPrompt: "b", Project: "proj",
	})
	testutil.NoError(t, err)
	testutil.Equal(t, first.Role.Name, "dup")

	second, err := SpawnHeraWorker(d, fr, HeraWorkerSpawnInput{
		OrchestratorID: orch.ID, BaseName: "dup", TaskPrompt: "b", Project: "proj",
	})
	testutil.NoError(t, err)
	testutil.Equal(t, second.Role.Name, "dup-2")
}

// TestSpawnHeraWorker_RoleBindingFailureUnwinds forces the role+binding insert
// to fail (invalid orchestrator FK) and asserts no orphan task / role / binding.
func TestSpawnHeraWorker_RoleBindingFailureUnwinds(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{}

	before, _ := d.Tasks()

	_, err := SpawnHeraWorker(d, fr, HeraWorkerSpawnInput{
		OrchestratorID: 999999, // no such orchestrator → role insert FK violation
		BaseName:       "orphan",
		TaskPrompt:     "body",
		Project:        "proj",
	})
	if err == nil {
		t.Fatal("expected spawn to fail on invalid orchestrator")
	}

	after, _ := d.Tasks()
	testutil.Equal(t, len(after), len(before))

	live, err := d.ListHeraLiveBindings()
	testutil.NoError(t, err)
	testutil.Equal(t, len(live), 0)
}

// TestSpawnHeraWorker_StartFailureUnwindsRoleAndBinding forces runner.Start to
// fail AFTER the role+binding are written and asserts the compensating cleanup
// removes the role (cascading the binding) — no orphan role/binding/task.
func TestSpawnHeraWorker_StartFailureUnwindsRoleAndBinding(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{startErr: errors.New("boom")}
	orch, err := d.CreateHeraOrchestrator("orch")
	testutil.NoError(t, err)

	before, _ := d.Tasks()

	_, err = SpawnHeraWorker(d, fr, HeraWorkerSpawnInput{
		OrchestratorID: orch.ID, BaseName: "w", TaskPrompt: "b", Project: "proj",
	})
	if err == nil {
		t.Fatal("expected spawn to fail when runner.Start errors")
	}

	// No orphan task.
	after, _ := d.Tasks()
	testutil.Equal(t, len(after), len(before))
	// No orphan role.
	roles, err := d.ListHeraRoles(orch.ID, true)
	testutil.NoError(t, err)
	testutil.Equal(t, len(roles), 0)
	// No orphan live binding.
	live, err := d.ListHeraLiveBindings()
	testutil.NoError(t, err)
	testutil.Equal(t, len(live), 0)
}
