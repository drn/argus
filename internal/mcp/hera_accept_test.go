package mcp

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// --- hera_accept (add-hera-accept-lifecycle) ---

func TestHeraAccept_NonCoordinatorRejected(t *testing.T) {
	s, d := testHeraServer(t)
	orch, err := d.CreateHeraOrchestrator("O", "")
	testutil.NoError(t, err)

	callerTask := addHeraTestTask(t, d, "/wt/worker-caller")
	_, _, err = d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "w1", Kind: db.HeraKindWorker, ArgusProject: "test-project",
	}, callerTask.ID, callerTask.Worktree)
	testutil.NoError(t, err)

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_accept",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"role_name":"whatever","orchestrator":"O"
		}`, callerTask.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	if !cr.IsError {
		t.Fatal("expected a coordinator-only rejection")
	}
	testutil.Contains(t, cr.Content[0].Text, "only coordinators")
}

func TestHeraAccept_UnknownRoleRejected(t *testing.T) {
	s, d := testHeraServer(t)
	coordTask := seedCoordinator(t, s, d, "O", "/wt/coord")

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_accept",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"role_name":"ghost","orchestrator":"O"
		}`, coordTask.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	if !cr.IsError {
		t.Fatal("expected an unknown-role rejection")
	}
	testutil.Contains(t, cr.Content[0].Text, "not found")
}

func TestHeraAccept_OwnRoleRejected(t *testing.T) {
	s, d := testHeraServer(t)
	coordTask := seedCoordinator(t, s, d, "O", "/wt/coord")

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_accept",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"role_name":"coord","orchestrator":"O"
		}`, coordTask.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	if !cr.IsError {
		t.Fatal("expected a self-target rejection")
	}
	testutil.Contains(t, cr.Content[0].Text, "own")
}

func TestHeraAccept_NoLiveBindingRejected(t *testing.T) {
	s, d := testHeraServer(t)
	coordTask := seedCoordinator(t, s, d, "O", "/wt/coord")

	orch, err := d.HeraOrchestratorByName("O")
	testutil.NoError(t, err)
	_, err = d.CreateHeraPlannedRole(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "planned-1", Kind: db.HeraKindWorker, ArgusProject: "test-project", Prompt: "later",
	})
	testutil.NoError(t, err)

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_accept",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"role_name":"planned-1","orchestrator":"O"
		}`, coordTask.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	if !cr.IsError {
		t.Fatal("expected a no-live-binding rejection")
	}
	testutil.Contains(t, cr.Content[0].Text, "no live binding")
}

func TestHeraAccept_FlipsInProgressWorkerAndNotifies(t *testing.T) {
	s, d := testHeraServer(t)
	coordTask := seedCoordinator(t, s, d, "O", "/wt/coord")

	orch, err := d.HeraOrchestratorByName("O")
	testutil.NoError(t, err)
	workerTask := addHeraTestTask(t, d, "/wt/worker-1")
	workerRole, _, err := d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "worker-1", Kind: db.HeraKindWorker, ArgusProject: "test-project",
	}, workerTask.ID, workerTask.Worktree)
	testutil.NoError(t, err)
	testutil.Equal(t, workerTask.Status, model.StatusInProgress)

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_accept",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"role_name":"worker-1","orchestrator":"O"
		}`, coordTask.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("expected success, got error: %s", cr.Content[0].Text)
	}
	testutil.Contains(t, cr.Content[0].Text, "accepted")

	got, err := d.Get(workerTask.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, model.StatusComplete)

	msgs, err := d.HeraInbox(workerRole.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(msgs), 1)
	testutil.Contains(t, msgs[0].Body, "accepted")
}

func TestHeraAccept_FlipsInReviewWorker(t *testing.T) {
	s, d := testHeraServer(t)
	coordTask := seedCoordinator(t, s, d, "O", "/wt/coord")

	orch, err := d.HeraOrchestratorByName("O")
	testutil.NoError(t, err)
	workerTask := addHeraTestTask(t, d, "/wt/worker-1")
	_, _, err = d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "worker-1", Kind: db.HeraKindWorker, ArgusProject: "test-project",
	}, workerTask.ID, workerTask.Worktree)
	testutil.NoError(t, err)
	workerTask.Status = model.StatusInReview
	testutil.NoError(t, d.Update(workerTask))

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_accept",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"role_name":"worker-1","orchestrator":"O"
		}`, coordTask.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("expected success, got error: %s", cr.Content[0].Text)
	}

	got, err := d.Get(workerTask.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, model.StatusComplete)
}

func TestHeraAccept_AlreadyCompleteIsNoOp(t *testing.T) {
	s, d := testHeraServer(t)
	coordTask := seedCoordinator(t, s, d, "O", "/wt/coord")

	orch, err := d.HeraOrchestratorByName("O")
	testutil.NoError(t, err)
	workerTask := addHeraTestTask(t, d, "/wt/worker-1")
	workerRole, _, err := d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "worker-1", Kind: db.HeraKindWorker, ArgusProject: "test-project",
	}, workerTask.ID, workerTask.Worktree)
	testutil.NoError(t, err)
	workerTask.Status = model.StatusComplete
	testutil.NoError(t, d.Update(workerTask))

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_accept",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"role_name":"worker-1","orchestrator":"O"
		}`, coordTask.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("expected a clean no-op success, got error: %s", cr.Content[0].Text)
	}
	testutil.Contains(t, cr.Content[0].Text, "already complete")

	got, err := d.Get(workerTask.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, model.StatusComplete)

	msgs, err := d.HeraInbox(workerRole.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(msgs), 0)
}

func TestHeraAccept_CustomMessageIncluded(t *testing.T) {
	s, d := testHeraServer(t)
	coordTask := seedCoordinator(t, s, d, "O", "/wt/coord")

	orch, err := d.HeraOrchestratorByName("O")
	testutil.NoError(t, err)
	workerTask := addHeraTestTask(t, d, "/wt/worker-1")
	workerRole, _, err := d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "worker-1", Kind: db.HeraKindWorker, ArgusProject: "test-project",
	}, workerTask.ID, workerTask.Worktree)
	testutil.NoError(t, err)

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_accept",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"role_name":"worker-1","orchestrator":"O","message":"great job on the edge cases"
		}`, coordTask.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("expected success, got error: %s", cr.Content[0].Text)
	}

	msgs, err := d.HeraInbox(workerRole.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(msgs), 1)
	testutil.Contains(t, msgs[0].Body, "great job on the edge cases")
}
