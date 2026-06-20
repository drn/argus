package mcp

// Stage 1 failing tests for sub-coordinator plan-authoring tools
// (add-hera-subcoord-nodes).
//
// Planned API referenced (does not exist yet — tests will fail to compile):
//   - hera_plan_node JSON: "kind" field accepting "worker"/"subcoord" + "goal" field
//   - hera_plan nodes JSON: per-node "kind" and "goal" fields
//   - db.HeraNodeKindSubCoord / db.HeraNodeKindWorker constants (via store layer)
//   - db.HeraRole.NodeKind field surfaced by ListHeraPlannedNodes
//
// The tool JSON protocol (kind/goal params) does not require new Go identifiers in
// this file — the tests drive the tools via JSON and inspect responses. However,
// the assertion against db.HeraNodeKindSubCoord on the persisted role DOES require
// the new constant, so these tests still fail to compile as intended.

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
)

// TestSubCoord_HeraPlanNode_AcceptsSubCoordKindAndGoal covers:
//   - "hera_plan_node accepts a sub-coordinator node"
//   - The tool accepts kind=subcoord + goal, returns success, persists NodeKind.
func TestSubCoord_HeraPlanNode_AcceptsSubCoordKindAndGoal(t *testing.T) {
	s, d := testHeraServer(t)
	coord := seedCoordinator(t, s, d, "orch", "/wt/coord")

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_plan_node",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q,
			"name": "3a-auth",
			"goal": "build the authentication sub-system end-to-end",
			"kind": "subcoord"
		}`, coord.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, false)
	testutil.Contains(t, cr.Content[0].Text, "Planned node created")

	orch, err := d.HeraOrchestratorByName("orch")
	testutil.NoError(t, err)
	role, err := d.HeraRoleByName(orch.ID, "3a-auth")
	testutil.NoError(t, err)
	// The hera_roles kind stays worker (D2) — subcoord is stored as NodeKind.
	testutil.Equal(t, role.Kind, db.HeraKindWorker)
	// NodeKind discriminator persisted.
	testutil.Equal(t, role.NodeKind, db.HeraNodeKindSubCoord)
	// Goal is the role's Prompt.
	testutil.Equal(t, role.Prompt, "build the authentication sub-system end-to-end")
	// No binding — it is a planned node.
	has, err := d.HeraRoleHasBinding(role.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, has, false)
}

// TestSubCoord_HeraPlanNode_SubCoordRequiresGoal covers:
//   - "Sub-coordinator node requires a goal" (hera_plan_node path)
//   - A subcoord node with no goal is rejected.
func TestSubCoord_HeraPlanNode_SubCoordRequiresGoal(t *testing.T) {
	s, d := testHeraServer(t)
	coord := seedCoordinator(t, s, d, "orch", "/wt/coord")

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_plan_node",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q,
			"name": "3a-auth",
			"kind": "subcoord"
		}`, coord.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "goal")
	// Nothing persisted.
	orch, _ := d.HeraOrchestratorByName("orch")
	planned, err := d.ListHeraPlannedNodes()
	testutil.NoError(t, err)
	testutil.Equal(t, len(planned), 0)
	_ = orch
}

// TestSubCoord_HeraPlanNode_OmittedKindCreatesLeafWorker covers:
//   - "Omitted kind creates a leaf worker"
//   - When kind is absent the existing behaviour is byte-identical: worker node.
func TestSubCoord_HeraPlanNode_OmittedKindCreatesLeafWorker(t *testing.T) {
	s, d := testHeraServer(t)
	coord := seedCoordinator(t, s, d, "orch", "/wt/coord")

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_plan_node",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q,
			"name": "1a-impl",
			"prompt": "implement the feature"
		}`, coord.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	testutil.Equal(t, callResult(t, resp).IsError, false)

	orch, _ := d.HeraOrchestratorByName("orch")
	role, err := d.HeraRoleByName(orch.ID, "1a-impl")
	testutil.NoError(t, err)
	testutil.Equal(t, role.Kind, db.HeraKindWorker)
	testutil.Equal(t, role.NodeKind, db.HeraNodeKindWorker)
}

// TestSubCoord_HeraPlanNode_NonCoordinatorRejectedForSubCoord covers:
//   - "Non-coordinator cannot author a sub-coordinator node"
func TestSubCoord_HeraPlanNode_NonCoordinatorRejectedForSubCoord(t *testing.T) {
	s, d := testHeraServer(t)
	seedCoordinator(t, s, d, "orch", "/wt/coord")
	worker := attachWorkerTask(t, s, d, "orch", "/wt/w", "w1")

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_plan_node",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q,
			"name": "3a-auth",
			"kind": "subcoord",
			"goal": "build auth"
		}`, worker.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "only coordinators may author the plan")
}

// TestSubCoord_HeraPlan_MixedNodeKinds covers:
//   - "Whole-graph submission mixes node kinds"
//   - hera_plan accepts nodes with different kinds; each is persisted correctly.
func TestSubCoord_HeraPlan_MixedNodeKinds(t *testing.T) {
	s, d := testHeraServer(t)
	coord := seedCoordinator(t, s, d, "orch", "/wt/coord")

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_plan",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q,
			"nodes": [
				{"name": "1a-research", "prompt": "do research", "kind": "worker"},
				{"name": "2a-auth-team", "goal": "build the auth sub-system", "kind": "subcoord"},
				{"name": "3a-deploy",   "prompt": "deploy everything"}
			],
			"edges": [
				{"blocked": "2a-auth-team", "blocker": "1a-research"},
				{"blocked": "3a-deploy",    "blocker": "2a-auth-team"}
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

	research, _ := d.HeraRoleByName(orch.ID, "1a-research")
	authTeam, _ := d.HeraRoleByName(orch.ID, "2a-auth-team")
	deploy, _ := d.HeraRoleByName(orch.ID, "3a-deploy")

	testutil.Equal(t, research.NodeKind, db.HeraNodeKindWorker)
	testutil.Equal(t, authTeam.NodeKind, db.HeraNodeKindSubCoord)
	testutil.Equal(t, authTeam.Prompt, "build the auth sub-system") // goal is the prompt
	testutil.Equal(t, deploy.NodeKind, db.HeraNodeKindWorker)

	// Blocking edges are intact.
	authBlockers, err := d.HeraBlockersOf(authTeam.ID)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, authBlockers, []int64{research.ID})

	deployBlockers, err := d.HeraBlockersOf(deploy.ID)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, deployBlockers, []int64{authTeam.ID})
}

// TestSubCoord_HeraPlan_SubCoordRequiresGoalInWholeGraph covers:
//   - "Sub-coordinator node requires a goal" (hera_plan whole-graph path)
//   - A subcoord node in a hera_plan batch with no goal rejects the request.
func TestSubCoord_HeraPlan_SubCoordRequiresGoalInWholeGraph(t *testing.T) {
	s, d := testHeraServer(t)
	coord := seedCoordinator(t, s, d, "orch", "/wt/coord")

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_plan",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q,
			"nodes": [
				{"name": "1a", "prompt": "leaf"},
				{"name": "2a", "kind": "subcoord"}
			]
		}`, coord.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "goal")

	// Atomic rollback: no orphan planned nodes from the valid first node.
	planned, err := d.ListHeraPlannedNodes()
	testutil.NoError(t, err)
	testutil.Equal(t, len(planned), 0)
}
