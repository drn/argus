package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// --- BUG-059: hera_join claim/attach agreement across a stale-task /
// reused-worktree collision, and the new hera_rebind repair verb. ---

// TestHera_Join_Claim_ResolvesLiveTaskDespiteStaleWorktreeCollision proves the
// reported symptom is gone: a born-bound worker whose binding is rooted at
// the LIVE task can claim it even though a stale, archived task shares the
// worktree and was listed first (the pre-fix "first cwd match wins" hazard).
func TestHera_Join_Claim_ResolvesLiveTaskDespiteStaleWorktreeCollision(t *testing.T) {
	s, d := testHeraServer(t)
	orch, err := d.CreateHeraOrchestrator("O", "")
	testutil.NoError(t, err)

	// Stale task added FIRST, archived, its binding already ENDED — mirrors
	// the live-data smoking gun (a prior task's worktree reused by a new one).
	stale := &model.Task{Name: "stale", Status: model.StatusInReview, Archived: true, Project: "p", Worktree: "/wt/shared-059"}
	testutil.NoError(t, d.Add(stale))

	live := &model.Task{Name: "live", Status: model.StatusInProgress, Project: "p", Worktree: "/wt/shared-059"}
	testutil.NoError(t, d.Add(live))

	role, _, err := d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: db.HeraKindWorker, ArgusProject: "p", Prompt: "do the thing",
	}, live.ID, live.Worktree)
	testutil.NoError(t, err)

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_join",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"orchestrator":"O"
		}`, stale.Worktree)), // cwd matches BOTH tasks' identical worktree path
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("claim did not resolve the live worker: %s", cr.Content[0].Text)
	}
	testutil.Contains(t, cr.Content[0].Text, "role_name**: w")
	testutil.Contains(t, cr.Content[0].Text, role.Name)
}

// TestHera_Join_Claim_WorktreeFallbackWhenBindingTaskDrifted isolates the
// worktree fallback: the live binding's own argus_task_id has drifted to a
// ghost id (no corresponding task row), so the task-keyed lookup misses — the
// worktree-keyed fallback still claims it.
func TestHera_Join_Claim_WorktreeFallbackWhenBindingTaskDrifted(t *testing.T) {
	s, d := testHeraServer(t)
	orch, err := d.CreateHeraOrchestrator("O", "")
	testutil.NoError(t, err)

	live := &model.Task{Name: "live", Status: model.StatusInProgress, Project: "p", Worktree: "/wt/drifted-059"}
	testutil.NoError(t, d.Add(live))

	// Binding rooted at a drifted/ghost task id; worktree still matches live.
	_, _, err = d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: db.HeraKindWorker, ArgusProject: "p",
	}, "ghost-task-id", live.Worktree)
	testutil.NoError(t, err)

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_join",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"orchestrator":"O"
		}`, live.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("worktree fallback failed to claim: %s", cr.Content[0].Text)
	}
	testutil.Contains(t, cr.Content[0].Text, "role_name**: w")
}

// TestHera_Join_Attach_CollisionReturnsFriendlyMessage proves attach no
// longer bubbles a raw UNIQUE constraint error: it detects the worktree-keyed
// live binding and returns an actionable message pointing at claim / hera_rebind.
func TestHera_Join_Attach_CollisionReturnsFriendlyMessage(t *testing.T) {
	s, d := testHeraServer(t)
	orch, err := d.CreateHeraOrchestrator("O", "")
	testutil.NoError(t, err)

	live := &model.Task{Name: "live", Status: model.StatusInProgress, Project: "p", Worktree: "/wt/collide-059"}
	testutil.NoError(t, d.Add(live))

	_, _, err = d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: db.HeraKindWorker, ArgusProject: "p",
	}, "ghost-task-id", live.Worktree)
	testutil.NoError(t, err)

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_join",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"orchestrator":"O","role_name":"w2","kind":"worker"
		}`, live.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	msg := cr.Content[0].Text
	testutil.Contains(t, msg, "already holds a live binding")
	testutil.Contains(t, msg, "hera_rebind")
	if strings.Contains(strings.ToUpper(msg), "UNIQUE") {
		t.Fatalf("attach leaked a raw UNIQUE constraint error: %q", msg)
	}
}

// TestHera_Join_Claim_AmbiguousCwdSurfaces confirms the resolver's ambiguity
// refusal reaches the caller rather than silently binding to a guessed task.
func TestHera_Join_Claim_AmbiguousCwdSurfaces(t *testing.T) {
	s, d := testHeraServer(t)
	_, err := d.CreateHeraOrchestrator("O", "")
	testutil.NoError(t, err)

	a := &model.Task{Name: "a", Status: model.StatusInProgress, Project: "p", Worktree: "/wt/amb-059"}
	testutil.NoError(t, d.Add(a))
	b := &model.Task{Name: "b", Status: model.StatusInProgress, Project: "p", Worktree: "/wt/amb-059"}
	testutil.NoError(t, d.Add(b))

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_join",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"orchestrator":"O"
		}`, a.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "multiple live argus tasks")
}

// TestHera_NewOrchestrator_WorktreeCollisionDrifted covers the bootstrap-side
// guard: a live binding already occupies (worktree, orchestrator) under a
// DRIFTED argus_task_id (no task-keyed match), so hera_new_orchestrator must
// still catch it via the worktree-keyed pre-check and return an actionable
// message instead of letting the role+binding INSERT hit the raw
// idx_hera_bindings_live_worktree_orch UNIQUE constraint.
func TestHera_NewOrchestrator_WorktreeCollisionDrifted(t *testing.T) {
	s, d := testHeraServer(t)
	orch, err := d.CreateHeraOrchestrator("myorch-drift", "")
	testutil.NoError(t, err)

	_, _, err = d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.HeraKindCoordinator, ArgusProject: "p",
	}, "ghost", "/wt/new-orch-drift")
	testutil.NoError(t, err)

	live := &model.Task{Name: "live", Status: model.StatusInProgress, Project: "p", Worktree: "/wt/new-orch-drift"}
	testutil.NoError(t, d.Add(live))

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_new_orchestrator",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"name":"myorch-drift","coordinator_role_name":"coord2"
		}`, live.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "this worktree already holds a live binding")

	// No second binding created: the worktree+orch still resolves to the ORIGINAL binding.
	stillGhost, err := d.HeraLiveBindingByWorktreeAndOrchestrator(live.Worktree, orch.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, stillGhost.ArgusTaskID, "ghost")
}

// --- hera_rebind ---

// TestHeraRebind_RepairsDriftedBinding is the core repair: a live binding
// whose argus_task_id has drifted to a stale/ghost task is reconciled to the
// caller's real live task. After the repair both lookup paths agree, the old
// binding is ended, and the role (and its messages) survive.
func TestHeraRebind_RepairsDriftedBinding(t *testing.T) {
	s, d := testHeraServer(t)
	orch, err := d.CreateHeraOrchestrator("O", "")
	testutil.NoError(t, err)

	role, err := d.CreateHeraRole(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: db.HeraKindWorker, ArgusProject: "p", Prompt: "ship it",
	})
	testutil.NoError(t, err)
	coord, err := d.CreateHeraRole(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.HeraKindCoordinator, ArgusProject: "p",
	})
	testutil.NoError(t, err)

	old, err := d.CreateHeraBinding(db.CreateHeraBindingInput{
		RoleID: role.ID, OrchestratorID: orch.ID, ArgusTaskID: "ghost", WorktreePath: "/wt/rebind-happy",
	})
	testutil.NoError(t, err)

	// A message to the worker role must survive the reconcile (keyed on role_id).
	_, err = d.SendHeraMessage(coord.ID, role.ID, "keep going", "keep going", nil)
	testutil.NoError(t, err)

	live := &model.Task{Name: "live", Status: model.StatusInProgress, Project: "p", Worktree: "/wt/rebind-happy"}
	testutil.NoError(t, d.Add(live))

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_rebind",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"orchestrator":"O"
		}`, live.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("hera_rebind failed: %s", cr.Content[0].Text)
	}
	text := cr.Content[0].Text
	testutil.Contains(t, text, "reconciled**: true")
	testutil.Contains(t, text, fmt.Sprintf("argus_task_id**: %s", live.ID))
	testutil.Contains(t, text, fmt.Sprintf("ended_binding_ids**: [%d]", old.ID))

	// Both lookup paths now resolve the SAME (new) binding.
	byTask, err := d.HeraLiveBindingByTaskAndOrchestrator(live.ID, orch.ID)
	testutil.NoError(t, err)
	byWt, err := d.HeraLiveBindingByWorktreeAndOrchestrator("/wt/rebind-happy", orch.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, byTask.ID, byWt.ID)
	testutil.NotEqual(t, byTask.ID, old.ID)

	// The role's message survived.
	unread, err := d.HeraInbox(role.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(unread), 1)

	// meta:hera.role mirrored to the live task.
	meta, err := d.ListMeta(live.ID, db.HeraMetaNamespace)
	testutil.NoError(t, err)
	found := false
	for _, e := range meta {
		if e.Key == db.HeraMetaKeyRole && e.Value == string(db.HeraKindWorker) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected role meta mirrored to live task, got %+v", meta)
	}
}

// TestHeraRebind_NoOpWhenAlreadyConsistent: a healthy binding is left untouched.
func TestHeraRebind_NoOpWhenAlreadyConsistent(t *testing.T) {
	s, d := testHeraServer(t)
	orch, err := d.CreateHeraOrchestrator("O", "")
	testutil.NoError(t, err)

	live := &model.Task{Name: "live", Status: model.StatusInProgress, Project: "p", Worktree: "/wt/rebind-noop"}
	testutil.NoError(t, d.Add(live))

	role, bnd, err := d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: db.HeraKindWorker, ArgusProject: "p",
	}, live.ID, live.Worktree)
	testutil.NoError(t, err)
	_ = role

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_rebind",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"orchestrator":"O"
		}`, live.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("hera_rebind failed: %s", cr.Content[0].Text)
	}
	text := cr.Content[0].Text
	testutil.Contains(t, text, "reconciled**: false")
	testutil.Contains(t, text, fmt.Sprintf("binding_id**: %d", bnd.ID))

	// No new binding created — still the same live binding.
	still, err := d.HeraLiveBindingByTaskAndOrchestrator(live.ID, orch.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, still.ID, bnd.ID)
}

// TestHeraRebind_RoleNameSelectsBinding covers the explicit role_name branch
// on a single-candidate repair.
func TestHeraRebind_RoleNameSelectsBinding(t *testing.T) {
	s, d := testHeraServer(t)
	orch, err := d.CreateHeraOrchestrator("O", "")
	testutil.NoError(t, err)

	_, _, err = d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: db.HeraKindWorker, ArgusProject: "p",
	}, "ghost", "/wt/rebind-rolename")
	testutil.NoError(t, err)

	live := &model.Task{Name: "live", Status: model.StatusInProgress, Project: "p", Worktree: "/wt/rebind-rolename"}
	testutil.NoError(t, d.Add(live))

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_rebind",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"orchestrator":"O","role_name":"w"
		}`, live.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("hera_rebind (role_name) failed: %s", cr.Content[0].Text)
	}
	text := cr.Content[0].Text
	testutil.Contains(t, text, "reconciled**: true")
	testutil.Contains(t, text, "role_name**: w")
	testutil.Contains(t, text, fmt.Sprintf("argus_task_id**: %s", live.ID))
}

// TestHeraRebind_RefusesAmbiguousCwd: two in_progress tasks share the
// worktree, so the caller's identity cannot be determined — refuse.
func TestHeraRebind_RefusesAmbiguousCwd(t *testing.T) {
	s, d := testHeraServer(t)
	orch, err := d.CreateHeraOrchestrator("O", "")
	testutil.NoError(t, err)

	_, _, err = d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: db.HeraKindWorker, ArgusProject: "p",
	}, "a", "/wt/rebind-amb")
	testutil.NoError(t, err)

	a := &model.Task{ID: "a", Name: "a", Status: model.StatusInProgress, Project: "p", Worktree: "/wt/rebind-amb"}
	testutil.NoError(t, d.Add(a))
	b := &model.Task{ID: "b", Name: "b", Status: model.StatusInProgress, Project: "p", Worktree: "/wt/rebind-amb"}
	testutil.NoError(t, d.Add(b))

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_rebind",
		Arguments: json.RawMessage(`{
			"cwd":"/wt/rebind-amb","orchestrator":"O"
		}`),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "multiple live argus tasks")
}

// TestHeraRebind_RefusesMultipleRolesWithoutRoleName: the caller's task and
// its worktree are bound to DIFFERENT roles under the same orchestrator — a
// genuinely tangled state that requires an explicit role_name to resolve.
func TestHeraRebind_RefusesMultipleRolesWithoutRoleName(t *testing.T) {
	s, d := testHeraServer(t)
	orch, err := d.CreateHeraOrchestrator("O", "")
	testutil.NoError(t, err)

	live := &model.Task{Name: "live", Status: model.StatusInProgress, Project: "p", Worktree: "/wt/rebind-multi"}
	testutil.NoError(t, d.Add(live))

	// r1 bound to the caller's task id but a DIFFERENT worktree.
	_, _, err = d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "r1", Kind: db.HeraKindWorker, ArgusProject: "p",
	}, live.ID, "/wt/rebind-multi-other")
	testutil.NoError(t, err)
	// r2 bound to the caller's worktree but a ghost task id.
	_, _, err = d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "r2", Kind: db.HeraKindWorker, ArgusProject: "p",
	}, "ghost", live.Worktree)
	testutil.NoError(t, err)

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_rebind",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"orchestrator":"O"
		}`, live.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "multiple roles")
}

// TestHeraRebind_RefusesWhenNothingToReconcile: no live binding for the
// orchestrator at this worktree/task — hera_rebind repairs, it does not create.
func TestHeraRebind_RefusesWhenNothingToReconcile(t *testing.T) {
	s, d := testHeraServer(t)
	_, err := d.CreateHeraOrchestrator("O", "")
	testutil.NoError(t, err)

	live := &model.Task{Name: "live", Status: model.StatusInProgress, Project: "p", Worktree: "/wt/rebind-nothing"}
	testutil.NoError(t, d.Add(live))

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_rebind",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"orchestrator":"O"
		}`, live.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "nothing to reconcile")
}

func TestHeraRebind_UnknownOrchestrator(t *testing.T) {
	s, d := testHeraServer(t)
	live := &model.Task{Name: "live", Status: model.StatusInProgress, Project: "p", Worktree: "/wt/rebind-unknown-orch"}
	testutil.NoError(t, d.Add(live))

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_rebind",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"orchestrator":"ghost-orch"
		}`, live.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "does not exist")
}
