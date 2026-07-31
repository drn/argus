package mcp

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
)

// --- hera_revive (add-hera-revive) ---
//
// fakeHeraReviver records the HeraReviveInput it was called with and returns
// a fixed outcome/error, so these tests exercise resolution + response
// rendering without a real runner/PTY (the gating logic itself is covered by
// internal/hera/revive_test.go and internal/daemon/revive_test.go).
type fakeHeraReviver struct {
	called     bool
	calledWith HeraReviveInput
	outcome    string
	err        error
}

func (f *fakeHeraReviver) reviver() HeraReviver {
	return func(in HeraReviveInput) (string, error) {
		f.called = true
		f.calledWith = in
		return f.outcome, f.err
	}
}

func TestHeraRevive_NonCoordinatorRejected(t *testing.T) {
	s, d := testHeraServer(t)
	s.SetHeraReviver((&fakeHeraReviver{}).reviver())
	orch, err := d.CreateHeraOrchestrator("O", "")
	testutil.NoError(t, err)

	callerTask := addHeraTestTask(t, d, "/wt/worker-caller")
	_, _, err = d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "w1", Kind: db.HeraKindWorker, ArgusProject: "test-project",
	}, callerTask.ID, callerTask.Worktree)
	testutil.NoError(t, err)

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_revive",
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

func TestHeraRevive_UnknownRoleRejected(t *testing.T) {
	s, d := testHeraServer(t)
	s.SetHeraReviver((&fakeHeraReviver{}).reviver())
	coordTask := seedCoordinator(t, s, d, "O", "/wt/coord")

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_revive",
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

func TestHeraRevive_OwnRoleRejected(t *testing.T) {
	s, d := testHeraServer(t)
	s.SetHeraReviver((&fakeHeraReviver{}).reviver())
	coordTask := seedCoordinator(t, s, d, "O", "/wt/coord")

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_revive",
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

func TestHeraRevive_NoLiveBindingRejected(t *testing.T) {
	s, d := testHeraServer(t)
	s.SetHeraReviver((&fakeHeraReviver{}).reviver())
	coordTask := seedCoordinator(t, s, d, "O", "/wt/coord")

	orch, err := d.HeraOrchestratorByName("O")
	testutil.NoError(t, err)
	_, err = d.CreateHeraPlannedRole(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "planned-1", Kind: db.HeraKindWorker, ArgusProject: "test-project", Prompt: "later",
	})
	testutil.NoError(t, err)

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_revive",
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

func TestHeraRevive_Success(t *testing.T) {
	s, d := testHeraServer(t)
	coordTask := seedCoordinator(t, s, d, "O", "/wt/coord")

	orch, err := d.HeraOrchestratorByName("O")
	testutil.NoError(t, err)
	workerTask := addHeraTestTask(t, d, "/wt/worker-1")
	_, _, err = d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "worker-1", Kind: db.HeraKindWorker, ArgusProject: "test-project",
	}, workerTask.ID, workerTask.Worktree)
	testutil.NoError(t, err)

	fr := &fakeHeraReviver{outcome: "kicked_stuck"}
	s.SetHeraReviver(fr.reviver())

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_revive",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"role_name":"worker-1","orchestrator":"O"
		}`, coordTask.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("expected success, got error: %s", cr.Content[0].Text)
	}
	testutil.Equal(t, fr.called, true)
	testutil.Equal(t, fr.calledWith.TaskID, workerTask.ID)
	testutil.Equal(t, fr.calledWith.IsCoordinator, false)
	testutil.Contains(t, cr.Content[0].Text, "kicked_stuck")
}

func TestHeraRevive_NotConfiguredWhenReviverNil(t *testing.T) {
	s, d := testHeraServer(t)
	coordTask := seedCoordinator(t, s, d, "O", "/wt/coord")

	orch, err := d.HeraOrchestratorByName("O")
	testutil.NoError(t, err)
	workerTask := addHeraTestTask(t, d, "/wt/worker-1")
	_, _, err = d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "worker-1", Kind: db.HeraKindWorker, ArgusProject: "test-project",
	}, workerTask.ID, workerTask.Worktree)
	testutil.NoError(t, err)

	// testHeraServer does NOT call SetHeraReviver — s.heraRevive stays nil.
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_revive",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"role_name":"worker-1","orchestrator":"O"
		}`, coordTask.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	if !cr.IsError {
		t.Fatal("expected a not-configured rejection")
	}
	testutil.Contains(t, cr.Content[0].Text, "not configured")
}
