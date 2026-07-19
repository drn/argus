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

// TestHeraCoordinatorOrientation_CarriesFiveDisciplineHabits is a snapshot-style
// test asserting the add-coordinator-context-management D4 habits all survive in
// the spawn orientation text: small window, low-default-effort-with-escalation,
// the sharpened delegation rule, pointers-not-payloads, and
// distillate-harvest-before-retire (design.md D4 acceptance criterion).
func TestHeraCoordinatorOrientation_CarriesFiveDisciplineHabits(t *testing.T) {
	got := strings.ToLower(HeraCoordinatorOrientation("my-orch"))

	// 1. Small window (habit, not a model setting).
	testutil.Equal(t, strings.Contains(got, "small window"), true)

	// 2. Low default reasoning effort, escalate for real judgment calls.
	testutil.Equal(t, strings.Contains(got, "low reasoning effort"), true)
	testutil.Equal(t, strings.Contains(got, "escalate"), true)

	// 3. Delegation bias sharpened to a concrete rule: native sub-agent for
	// investigation, hera_spawn_worker reserved for its-own-worktree work.
	testutil.Equal(t, strings.Contains(got, "delegate with prejudice"), true)
	testutil.Equal(t, strings.Contains(got, "agent/task"), true)
	testutil.Equal(t, strings.Contains(got, "hera_spawn_worker"), true)

	// 4. Pointers, not payloads.
	testutil.Equal(t, strings.Contains(got, "pointers, not payloads"), true)
	testutil.Equal(t, strings.Contains(got, "path:line"), true)

	// 5. Distillate-harvest-before-retire: design.md checkpoint + handoff_note
	// via the extended hera_status call.
	testutil.Equal(t, strings.Contains(got, "harvest a distillate"), true)
	testutil.Equal(t, strings.Contains(got, "design.md"), true)
	testutil.Equal(t, strings.Contains(got, "handoff_note"), true)
	testutil.Equal(t, strings.Contains(got, "request_recycle"), true)
}

// TestHeraCoordinatorOrientation_NoSelfPromotion guards the coordinator's core
// contract. The HEADLINE rule is dispatch-don't-implement: a coordinator with
// actual work calls hera_spawn_worker (regardless of repo) and never implements
// in its own session. The real bug was a coordinator using hera_new_orchestrator
// on ITSELF as a relabel and then doing the work solo — so the orientation must
// (a) lead with "you do NOT implement — spawn a worker", (b) keep the self-invoke
// guardrail and state it is not a way to "become the one who does the work",
// (c) treat cross-repo as a normal worker spawn (no new orchestrator), and
// (d) reserve a new coordinator session for genuine multi-project/multi-phase
// sub-teams. HeraWorkerOrientation is intentionally NOT covered: worker
// self-promotion is the correct pattern.
func TestHeraCoordinatorOrientation_NoSelfPromotion(t *testing.T) {
	got := HeraCoordinatorOrientation("my-orch")
	cases := []struct {
		name    string
		sub     string
		present bool
	}{
		// The old self-promotion phrasing AND cross-repo-as-a-trigger phrasing are gone.
		{"no-old-become-subcoord", "become a sub-coordinator", false},
		{"no-crossrepo-as-subteam-trigger", "sub-team (including one in another repo)", false},
		// Headline: a coordinator dispatches, it does NOT implement.
		{"headline-no-implement", "you do NOT implement", true},
		{"headline-spawn-worker-for-work", "spawn a worker with hera_spawn_worker", true},
		// hera_new_orchestrator-on-self is a relabel-and-implement antipattern, not a sub-team.
		{"warns-no-self-invoke", "NEVER call hera_new_orchestrator on yourself", true},
		{"not-a-way-to-do-work", "NOT a way to become the one who does the work", true},
		// Cross-repo work is a normal worker spawn — no new orchestrator.
		{"crossrepo-normal-spawn", "cross-repo work is just a normal worker spawn", true},
		{"no-new-orch-for-crossrepo", "no new orchestrator needed", true},
		// A NEW coordinator session is reserved for multi-project/phase decomposition.
		{"subteam-trigger-is-scope", "multiple projects or phases", true},
		{"path-worker-promotion", "worker-promotion", true},
		{"path-subcoord-node", "kind=subcoord hera_plan_node", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			testutil.Equal(t, strings.Contains(got, c.sub), c.present)
		})
	}
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
	orch, err := d.CreateHeraOrchestrator("orch", "")
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
	orch, err := d.CreateHeraOrchestrator("orch", "")
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
	orch, err := d.CreateHeraOrchestrator("orch", "")
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

// TestSpawnHeraWorker_ArchetypePassthrough asserts an explicit Archetype flows
// onto BOTH the spawned task (the model-resolution key) and the worker role (the
// mirrored display value).
func TestSpawnHeraWorker_ArchetypePassthrough(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{}
	orch, err := d.CreateHeraOrchestrator("orch", "")
	testutil.NoError(t, err)

	res, err := SpawnHeraWorker(d, fr, HeraWorkerSpawnInput{
		OrchestratorID: orch.ID,
		BaseName:       "ci",
		TaskPrompt:     "body",
		Project:        "proj",
		Archetype:      "ci_loop",
	})
	testutil.NoError(t, err)

	// Mirrored onto the role.
	testutil.Equal(t, res.Role.Archetype, "ci_loop")
	// Persisted onto the task (the authoritative resolution key).
	got, err := d.Get(res.Task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Archetype, "ci_loop")
}

// TestSpawnHeraWorker_ArchetypeDefaultsCodeSlice asserts an omitted archetype
// defaults to code_slice on both the task and the role.
func TestSpawnHeraWorker_ArchetypeDefaultsCodeSlice(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{}
	orch, err := d.CreateHeraOrchestrator("orch", "")
	testutil.NoError(t, err)

	res, err := SpawnHeraWorker(d, fr, HeraWorkerSpawnInput{
		OrchestratorID: orch.ID,
		BaseName:       "plain",
		TaskPrompt:     "body",
		Project:        "proj",
	})
	testutil.NoError(t, err)

	testutil.Equal(t, res.Role.Archetype, "code_slice")
	got, err := d.Get(res.Task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Archetype, "code_slice")
}

// TestSpawnHeraCoordinator_ArchetypeDefaultsOrchestrator asserts a born-bound
// coordinator defaults to the orchestrator archetype on the task + coord role.
func TestSpawnHeraCoordinator_ArchetypeDefaultsOrchestrator(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{}

	res, err := SpawnHeraCoordinator(d, fr, HeraCoordinatorSpawnInput{
		OrchestratorBaseName: "coord-orch",
		TaskPrompt:           "body",
		Project:              "proj",
	})
	testutil.NoError(t, err)

	testutil.Equal(t, res.Role.Archetype, "orchestrator")
	got, err := d.Get(res.Task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Archetype, "orchestrator")
}

// TestMaterializeHeraWorker_ArchetypePropagates asserts a planned role's authored
// archetype is copied onto the materialized task (the gater propagation path).
func TestMaterializeHeraWorker_ArchetypePropagates(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{}
	orch, err := d.CreateHeraOrchestrator("orch", "")
	testutil.NoError(t, err)

	planned, err := d.CreateHeraPlannedRole(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "rev", ArgusProject: "proj", Prompt: "v", Archetype: "review",
	})
	testutil.NoError(t, err)

	res, err := MaterializeHeraWorker(d, fr, HeraMaterializeInput{
		Role: planned, TaskPrompt: "body", Project: "proj",
	})
	testutil.NoError(t, err)

	got, err := d.Get(res.Task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Archetype, "review")
}

// TestSpawnHeraWorker_UniquifiesName verifies a second spawn of the same base
// name lands on base-2 (the role-name uniquifier).
func TestSpawnHeraWorker_UniquifiesName(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{}
	orch, err := d.CreateHeraOrchestrator("orch", "")
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

func TestHeraCheckInOrientation_NamesAndInstructsPoll(t *testing.T) {
	got := HeraCheckInOrientation("orch", "coord")
	testutil.Equal(t, strings.Contains(got, `"orch"`), true)
	testutil.Equal(t, strings.Contains(got, `"coord"`), true)
	// The standing order: check in via hera_send, then POLL hera_inbox (pulled,
	// not pushed) for go/wait before real work.
	testutil.Equal(t, strings.Contains(got, "hera_send"), true)
	testutil.Equal(t, strings.Contains(got, "hera_inbox"), true)
	testutil.Equal(t, strings.Contains(strings.ToLower(got), "poll"), true)
	testutil.Equal(t, strings.Contains(strings.ToLower(got), "go"), true)
	testutil.Equal(t, strings.Contains(strings.ToLower(got), "wait"), true)
}

// TestMaterializeHeraWorker_BindsPreCreatedRole asserts materialization binds and
// starts the EXISTING planned role (no second role/agent), resolves the worktree
// + base_branch at materialize time, and delivers the check-in-prefixed prompt.
func TestMaterializeHeraWorker_BindsPreCreatedRole(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{sessionPID: 99}
	orch, err := d.CreateHeraOrchestrator("orch", "")
	testutil.NoError(t, err)

	// Pre-create the planned role (no binding).
	planned, err := d.CreateHeraPlannedRole(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "2b-impl", ArgusProject: "proj", Prompt: "verbatim",
	})
	testutil.NoError(t, err)

	rolesBefore, err := d.ListHeraRoles(orch.ID, true)
	testutil.NoError(t, err)

	checkInPrompt := HeraCheckInOrientation("orch", "coord") + "\n\n---\n\nverbatim"
	res, err := MaterializeHeraWorker(d, fr, HeraMaterializeInput{
		Role:       planned,
		TaskPrompt: checkInPrompt,
		Project:    "proj",
		// Branch left empty: resolution from blocker branches is the gater's
		// job (TestGater_* exercises it against real blocker branches); the
		// project default (HEAD) is used here.
	})
	testutil.NoError(t, err)

	// Bound the SAME role (no new role minted).
	testutil.Equal(t, res.Role.ID, planned.ID)
	rolesAfter, err := d.ListHeraRoles(orch.ID, true)
	testutil.NoError(t, err)
	testutil.Equal(t, len(rolesAfter), len(rolesBefore))
	testutil.Equal(t, fr.startCalls, 1)

	// A live binding now exists for the planned role.
	bnd, err := d.HeraLiveBindingByRole(planned.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, bnd.ArgusTaskID, res.Task.ID)

	// The task carries the check-in-prefixed prompt.
	got, err := d.Get(res.Task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Prompt, checkInPrompt)

	// It is no longer a planned node (it has a binding now).
	planNodes, err := d.ListHeraPlannedNodes()
	testutil.NoError(t, err)
	testutil.Equal(t, len(planNodes), 0)
}

// TestMaterializeHeraWorker_NilRole guards the nil-role input.
func TestMaterializeHeraWorker_NilRole(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	_, err := MaterializeHeraWorker(d, &fakeRunner{}, HeraMaterializeInput{Role: nil, Project: "proj"})
	if err == nil {
		t.Fatal("expected error on nil role")
	}
}

// TestMaterializeHeraWorker_StartFailureEndsBindingNotRole forces runner.Start to
// fail and asserts the compensating cleanup ENDS the binding but LEAVES the
// planned role intact (authored data must survive a failed materialize).
func TestMaterializeHeraWorker_StartFailureEndsBindingNotRole(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{startErr: errors.New("boom")}
	orch, err := d.CreateHeraOrchestrator("orch", "")
	testutil.NoError(t, err)
	planned, err := d.CreateHeraPlannedRole(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "2b-impl", ArgusProject: "proj", Prompt: "v",
	})
	testutil.NoError(t, err)
	before, _ := d.Tasks()

	_, err = MaterializeHeraWorker(d, fr, HeraMaterializeInput{
		Role: planned, TaskPrompt: "body", Project: "proj",
	})
	if err == nil {
		t.Fatal("expected materialize to fail when runner.Start errors")
	}

	// No orphan task.
	after, _ := d.Tasks()
	testutil.Equal(t, len(after), len(before))
	// The role survives (authored data).
	got, err := d.HeraRole(planned.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.ID, planned.ID)
	// No live binding left.
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
	orch, err := d.CreateHeraOrchestrator("orch", "")
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
