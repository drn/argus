package mcp

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// attachWorkerTask adds a fresh task, attaches a worker role under an existing
// orchestrator, and returns the task (so its cwd resolves to a worker caller).
func attachWorkerTask(t *testing.T, s *Server, d *db.DB, orch, worktree, roleName string) *model.Task {
	t.Helper()
	task := addHeraTestTask(t, d, worktree)
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_join",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q, "orchestrator": %q, "role_name": %q, "kind": "worker"
		}`, worktree, orch, roleName)),
	})
	testutil.NoError(t, respErr(resp))
	return task
}

func planNode(t *testing.T, s *Server, cwd, name, prompt string) ToolCallResult {
	t.Helper()
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_plan_node",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q, "name": %q, "prompt": %q
		}`, cwd, name, prompt)),
	})
	testutil.NoError(t, respErr(resp))
	return callResult(t, resp)
}

func TestHeraPlanNode_CreatesBindinglessRole(t *testing.T) {
	s, d := testHeraServer(t)
	coord := seedCoordinator(t, s, d, "orch", "/wt/coord")

	cr := planNode(t, s, coord.Worktree, "2c-fact-checker", "verify the facts")
	testutil.Equal(t, cr.IsError, false)
	testutil.Contains(t, cr.Content[0].Text, "Planned node created")

	orch, err := d.HeraOrchestratorByName("orch")
	testutil.NoError(t, err)
	role, err := d.HeraRoleByName(orch.ID, "2c-fact-checker")
	testutil.NoError(t, err)
	testutil.Equal(t, role.Kind, db.HeraKindWorker)
	testutil.Equal(t, role.Prompt, "verify the facts")
	// No binding, no agent, no inbox — it is a planned node.
	has, err := d.HeraRoleHasBinding(role.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, has, false)
	planned, err := d.ListHeraPlannedNodes()
	testutil.NoError(t, err)
	testutil.Equal(t, len(planned), 1)
	// Inherits the coordinator's project.
	testutil.Equal(t, role.ArgusProject, coord.Project)
}

func TestHeraPlanNode_NonCoordinatorRejected(t *testing.T) {
	s, d := testHeraServer(t)
	seedCoordinator(t, s, d, "orch", "/wt/coord")
	worker := attachWorkerTask(t, s, d, "orch", "/wt/w", "w1")

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_plan_node",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q, "name": "x", "prompt": "y"
		}`, worker.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "only coordinators may author the plan")
}

func TestHeraBlock_AddsEdge(t *testing.T) {
	s, d := testHeraServer(t)
	coord := seedCoordinator(t, s, d, "orch", "/wt/coord")
	planNode(t, s, coord.Worktree, "a", "do a")
	planNode(t, s, coord.Worktree, "b", "do b")

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_block",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q, "blocked": "b", "blocker": "a"
		}`, coord.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, false)

	orch, _ := d.HeraOrchestratorByName("orch")
	b, _ := d.HeraRoleByName(orch.ID, "b")
	a, _ := d.HeraRoleByName(orch.ID, "a")
	blockers, err := d.HeraBlockersOf(b.ID)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, blockers, []int64{a.ID})
}

func TestHeraBlock_NonCoordinatorRejected(t *testing.T) {
	s, d := testHeraServer(t)
	coord := seedCoordinator(t, s, d, "orch", "/wt/coord")
	planNode(t, s, coord.Worktree, "a", "do a")
	planNode(t, s, coord.Worktree, "b", "do b")
	worker := attachWorkerTask(t, s, d, "orch", "/wt/w", "w1")

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_block",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q, "blocked": "b", "blocker": "a"
		}`, worker.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "only coordinators may author the plan")
}

func TestHeraBlock_CycleRejected(t *testing.T) {
	s, d := testHeraServer(t)
	coord := seedCoordinator(t, s, d, "orch", "/wt/coord")
	planNode(t, s, coord.Worktree, "a", "p")
	planNode(t, s, coord.Worktree, "b", "p")
	// b<-a is fine; a<-b would cycle.
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "hera_block",
		Arguments: json.RawMessage(fmt.Sprintf(`{"cwd": %q, "blocked": "b", "blocker": "a"}`, coord.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	testutil.Equal(t, callResult(t, resp).IsError, false)

	resp = doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "hera_block",
		Arguments: json.RawMessage(fmt.Sprintf(`{"cwd": %q, "blocked": "a", "blocker": "b"}`, coord.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "cycle")
}

func TestHeraBlock_UnknownRole(t *testing.T) {
	s, d := testHeraServer(t)
	coord := seedCoordinator(t, s, d, "orch", "/wt/coord")
	planNode(t, s, coord.Worktree, "a", "p")
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "hera_block",
		Arguments: json.RawMessage(fmt.Sprintf(`{"cwd": %q, "blocked": "ghost", "blocker": "a"}`, coord.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "not found")
}

func TestHeraPlan_WholeGraphInOneCall(t *testing.T) {
	s, d := testHeraServer(t)
	coord := seedCoordinator(t, s, d, "orch", "/wt/coord")

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_plan",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q,
			"nodes": [
				{"name": "1a", "prompt": "stage one a"},
				{"name": "1b", "prompt": "stage one b"},
				{"name": "2a", "prompt": "stage two a"}
			],
			"edges": [
				{"blocked": "2a", "blocker": "1a"},
				{"blocked": "2a", "blocker": "1b"}
			]
		}`, coord.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, false)
	testutil.Contains(t, cr.Content[0].Text, "Plan submitted")

	orch, _ := d.HeraOrchestratorByName("orch")
	planned, err := d.ListHeraPlannedNodes()
	testutil.NoError(t, err)
	testutil.Equal(t, len(planned), 3)

	n2a, _ := d.HeraRoleByName(orch.ID, "2a")
	blockers, err := d.HeraBlockersOf(n2a.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(blockers), 2)
}

func TestHeraPlan_CycleRejected(t *testing.T) {
	s, d := testHeraServer(t)
	coord := seedCoordinator(t, s, d, "orch", "/wt/coord")
	orch, _ := d.HeraOrchestratorByName("orch")
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_plan",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q,
			"nodes": [{"name": "a", "prompt": "p"}, {"name": "b", "prompt": "p"}],
			"edges": [{"blocked": "b", "blocker": "a"}, {"blocked": "a", "blocker": "b"}]
		}`, coord.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "cycle")

	// Atomic rollback: the cyclic batch must leave ZERO rows — no orphan planned
	// nodes from the (valid) node inserts that ran before the bad edge.
	planned, err := d.ListHeraPlannedNodes()
	testutil.NoError(t, err)
	testutil.Equal(t, len(planned), 0)
	_, aErr := d.HeraRoleByName(orch.ID, "a")
	testutil.ErrorIs(t, aErr, db.ErrHeraNotFound)
	_, bErr := d.HeraRoleByName(orch.ID, "b")
	testutil.ErrorIs(t, bErr, db.ErrHeraNotFound)
}

func TestHeraPlan_NonCoordinatorRejected(t *testing.T) {
	s, d := testHeraServer(t)
	seedCoordinator(t, s, d, "orch", "/wt/coord")
	worker := attachWorkerTask(t, s, d, "orch", "/wt/w", "w1")
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_plan",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q, "nodes": [{"name": "a", "prompt": "p"}]
		}`, worker.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "only coordinators may author the plan")
}

// --- hera_plan_node_update ---

func TestHeraPlanNodeUpdate_EditsPrompt(t *testing.T) {
	s, d := testHeraServer(t)
	coord := seedCoordinator(t, s, d, "orch", "/wt/coord")
	planNode(t, s, coord.Worktree, "1a-writer", "original prompt")

	orch, _ := d.HeraOrchestratorByName("orch")
	role, _ := d.HeraRoleByName(orch.ID, "1a-writer")
	testutil.Equal(t, role.Prompt, "original prompt")

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_plan_node_update",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q, "name": "1a-writer", "prompt": "revised prompt"
		}`, coord.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, false)
	testutil.Contains(t, cr.Content[0].Text, "Planned node updated")

	updated, _ := d.HeraRoleByName(orch.ID, "1a-writer")
	testutil.Equal(t, updated.Prompt, "revised prompt")
}

func TestHeraPlanNodeUpdate_EditsProject(t *testing.T) {
	s, d := testHeraServer(t)
	coord := seedCoordinator(t, s, d, "orch", "/wt/coord")
	planNode(t, s, coord.Worktree, "1a-writer", "some prompt")

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_plan_node_update",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q, "name": "1a-writer", "project": "new-project"
		}`, coord.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, false)

	orch, _ := d.HeraOrchestratorByName("orch")
	updated, _ := d.HeraRoleByName(orch.ID, "1a-writer")
	testutil.Equal(t, updated.ArgusProject, "new-project")
}

func TestHeraPlanNodeUpdate_RejectsMaterialized(t *testing.T) {
	s, d := testHeraServer(t)
	coord := seedCoordinator(t, s, d, "orch", "/wt/coord")
	// Attach a worker with a binding (simulates a materialized node).
	orch, _ := d.HeraOrchestratorByName("orch")
	workerTask := addHeraTestTask(t, d, "/wt/w1")
	_, _, err := d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID,
		Name:           "1a-live",
		Kind:           db.HeraKindWorker,
		ArgusProject:   "proj",
		Prompt:         "original",
	}, workerTask.ID, "/wt/w1")
	testutil.NoError(t, err)

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_plan_node_update",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q, "name": "1a-live", "prompt": "new prompt"
		}`, coord.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "already materialized")
}

func TestHeraPlanNodeUpdate_RejectsNonCoordinator(t *testing.T) {
	s, d := testHeraServer(t)
	seedCoordinator(t, s, d, "orch", "/wt/coord")
	worker := attachWorkerTask(t, s, d, "orch", "/wt/w", "w1")

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_plan_node_update",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q, "name": "anything", "prompt": "p"
		}`, worker.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "only coordinators may author the plan")
}

func TestHeraPlanNodeUpdate_RejectsEmptyMutation(t *testing.T) {
	s, d := testHeraServer(t)
	coord := seedCoordinator(t, s, d, "orch", "/wt/coord")
	planNode(t, s, coord.Worktree, "1a-writer", "original")

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_plan_node_update",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q, "name": "1a-writer"
		}`, coord.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "at least one of prompt or project")
}

// --- hera_unblock ---

func TestHeraUnblock_DropsExistingEdge(t *testing.T) {
	s, d := testHeraServer(t)
	coord := seedCoordinator(t, s, d, "orch", "/wt/coord")
	planNode(t, s, coord.Worktree, "a", "do a")
	planNode(t, s, coord.Worktree, "b", "do b")
	// Add block edge: b waits on a.
	doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "hera_block",
		Arguments: json.RawMessage(fmt.Sprintf(`{"cwd": %q, "blocked": "b", "blocker": "a"}`, coord.Worktree)),
	})

	orch, _ := d.HeraOrchestratorByName("orch")
	b, _ := d.HeraRoleByName(orch.ID, "b")
	blockersBefore, _ := d.HeraBlockersOf(b.ID)
	testutil.Equal(t, len(blockersBefore), 1)

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_unblock",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q, "blocked": "b", "blocker": "a"
		}`, coord.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, false)
	testutil.Contains(t, cr.Content[0].Text, "Blocking edge removed")

	blockersAfter, _ := d.HeraBlockersOf(b.ID)
	testutil.Equal(t, len(blockersAfter), 0)
}

func TestHeraUnblock_IdempotentOnMissingEdge(t *testing.T) {
	s, d := testHeraServer(t)
	coord := seedCoordinator(t, s, d, "orch", "/wt/coord")
	planNode(t, s, coord.Worktree, "a", "do a")
	planNode(t, s, coord.Worktree, "b", "do b")
	// No block edge added — unblock should still succeed.
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_unblock",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q, "blocked": "b", "blocker": "a"
		}`, coord.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, false)
}

func TestHeraUnblock_RejectsNonCoordinator(t *testing.T) {
	s, d := testHeraServer(t)
	seedCoordinator(t, s, d, "orch", "/wt/coord")
	worker := attachWorkerTask(t, s, d, "orch", "/wt/w", "w1")

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_unblock",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q, "blocked": "b", "blocker": "a"
		}`, worker.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "only coordinators may author the plan")
}

// --- hera_plan_node_cancel ---

func TestHeraPlanNodeCancel_CancelsPlannedNode(t *testing.T) {
	s, d := testHeraServer(t)
	coord := seedCoordinator(t, s, d, "orch", "/wt/coord")
	planNode(t, s, coord.Worktree, "1a-worker", "do work")

	// Node is in the planned set before cancel.
	plansBefore, _ := d.ListHeraPlannedNodes()
	testutil.Equal(t, len(plansBefore), 1)

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_plan_node_cancel",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q, "name": "1a-worker"
		}`, coord.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, false)
	testutil.Contains(t, cr.Content[0].Text, "Planned node cancelled")

	// After cancel the node must be excluded from the planned set (gater-side).
	plansAfter, _ := d.ListHeraPlannedNodes()
	testutil.Equal(t, len(plansAfter), 0)

	// But the role must still exist in the DB (kept for plan visibility).
	orch, _ := d.HeraOrchestratorByName("orch")
	role, err := d.HeraRoleByName(orch.ID, "1a-worker")
	testutil.NoError(t, err)
	if role.CancelledAt == nil {
		t.Fatal("expected cancelled_at to be set after cancel")
	}
}

func TestHeraPlanNodeCancel_RejectsMaterialized(t *testing.T) {
	s, d := testHeraServer(t)
	coord := seedCoordinator(t, s, d, "orch", "/wt/coord")
	// Attach a worker with a binding (simulates a materialized node).
	orch, _ := d.HeraOrchestratorByName("orch")
	workerTask := addHeraTestTask(t, d, "/wt/w2")
	_, _, err := d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID,
		Name:           "1b-live",
		Kind:           db.HeraKindWorker,
		ArgusProject:   "proj",
		Prompt:         "prompt",
	}, workerTask.ID, "/wt/w2")
	testutil.NoError(t, err)

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_plan_node_cancel",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q, "name": "1b-live"
		}`, coord.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "already materialized")
}

func TestHeraPlanNodeCancel_RejectsNonCoordinator(t *testing.T) {
	s, d := testHeraServer(t)
	seedCoordinator(t, s, d, "orch", "/wt/coord")
	worker := attachWorkerTask(t, s, d, "orch", "/wt/w", "w1")

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_plan_node_cancel",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q, "name": "anything"
		}`, worker.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "only coordinators may author the plan")
}

// TestHeraPlan_EdgeReferencingExistingRole confirms an edge endpoint resolves
// against a pre-existing orchestrator role (not just nodes in this call).
func TestHeraPlan_EdgeReferencingExistingRole(t *testing.T) {
	s, d := testHeraServer(t)
	coord := seedCoordinator(t, s, d, "orch", "/wt/coord")
	planNode(t, s, coord.Worktree, "1a", "existing")

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_plan",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q,
			"nodes": [{"name": "2a", "prompt": "new"}],
			"edges": [{"blocked": "2a", "blocker": "1a"}]
		}`, coord.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	testutil.Equal(t, callResult(t, resp).IsError, false)

	orch, _ := d.HeraOrchestratorByName("orch")
	n2a, _ := d.HeraRoleByName(orch.ID, "2a")
	n1a, _ := d.HeraRoleByName(orch.ID, "1a")
	blockers, err := d.HeraBlockersOf(n2a.ID)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, blockers, []int64{n1a.ID})
}
