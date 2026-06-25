package mcp

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// --- hera_send required status (D1, make-hera-plan-living) ---

// heraSend is a convenience wrapper that calls hera_send via the MCP tool and
// returns the ToolCallResult. Mirrors heraStatus in hera_test.go.
func heraSend(t *testing.T, s *Server, cwd, body, tldr, status, to string) ToolCallResult {
	t.Helper()
	args := map[string]interface{}{
		"cwd":  cwd,
		"body": body,
		"tldr": tldr,
	}
	if status != "" {
		args["status"] = status
	}
	if to != "" {
		args["to"] = to
	}
	raw, err := json.Marshal(args)
	testutil.NoError(t, err)
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "hera_send",
		Arguments: json.RawMessage(raw),
	})
	testutil.NoError(t, respErr(resp))
	return callResult(t, resp)
}

// TestHeraSend_Worker_NoStatus_Rejected verifies that a worker calling
// hera_send without a status gets a hard error naming all five valid values,
// and that no message is persisted.
func TestHeraSend_Worker_NoStatus_Rejected(t *testing.T) {
	s, d := testHeraServer(t)
	seedCoordinator(t, s, d, "myorch", "/wt/coord")
	worker := addHeraTestTask(t, d, "/wt/worker")
	attachWorker(t, s, "myorch", worker.Worktree)

	// Send with no status — must fail before any message is sent.
	cr := heraSend(t, s, worker.Worktree, "report body", "done report", "", "")
	testutil.Equal(t, cr.IsError, true)
	// Error message must name all five valid values.
	for _, v := range []string{"idle", "working", "blocked", "done", "failed"} {
		testutil.Contains(t, cr.Content[0].Text, v)
	}

	// Verify no message was persisted in the DB. The coordinator's inbox must be
	// empty (we check via hera_inbox on the coordinator worktree).
	coordInbox := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "hera_inbox",
		Arguments: json.RawMessage(`{"cwd":"/wt/coord"}`),
	})
	testutil.NoError(t, respErr(coordInbox))
	inboxCR := callResult(t, coordInbox)
	testutil.Equal(t, inboxCR.IsError, false)
	testutil.Contains(t, inboxCR.Content[0].Text, "Inbox empty")
}

// TestHeraSend_Freelance_NoStatus_Rejected verifies that a freelance role
// (same as worker) also requires status on hera_send.
func TestHeraSend_Freelance_NoStatus_Rejected(t *testing.T) {
	s, d := testHeraServer(t)
	seedCoordinator(t, s, d, "myorch", "/wt/coord")

	freelanceTask := addHeraTestTask(t, d, "/wt/freelance")
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_join",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"orchestrator":"myorch","role_name":"fl","kind":"freelance"
		}`, freelanceTask.Worktree)),
	})
	testutil.Equal(t, callResult(t, resp).IsError, false)

	cr := heraSend(t, s, freelanceTask.Worktree, "freelance report", "report", "", "")
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "status is required")
}

// TestHeraSend_Worker_StatusWorking_Applied verifies that a worker sending with
// status=working has its role status set to working synchronously, and the
// message is delivered.
func TestHeraSend_Worker_StatusWorking_Applied(t *testing.T) {
	s, d := testHeraServer(t)
	seedCoordinator(t, s, d, "myorch", "/wt/coord")
	worker := addHeraTestTask(t, d, "/wt/worker")
	attachWorker(t, s, "myorch", worker.Worktree)

	cr := heraSend(t, s, worker.Worktree, "still working", "progress", "working", "")
	testutil.Equal(t, cr.IsError, false)
	testutil.Contains(t, cr.Content[0].Text, "Message sent")

	// Role status must be "working" synchronously after the call.
	orch, err := d.HeraOrchestratorByName("myorch")
	testutil.NoError(t, err)
	role, err := d.HeraRoleByName(orch.ID, "w1")
	testutil.NoError(t, err)
	rs, err := d.HeraRoleStatusFor(role.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, rs.Status, db.HeraStatusWorking)
}

// TestHeraSend_Worker_StatusDone_RollsToReview verifies that a worker sending
// with status=done rolls its bound argus task to in_review + stamps
// ready_to_close, exactly as hera_status(done) would.
func TestHeraSend_Worker_StatusDone_RollsToReview(t *testing.T) {
	s, d := testHeraServer(t)
	seedCoordinator(t, s, d, "myorch", "/wt/coord")
	worker := addHeraTestTask(t, d, "/wt/worker") // StatusInProgress
	attachWorker(t, s, "myorch", worker.Worktree)

	cr := heraSend(t, s, worker.Worktree, "all done", "done report", "done", "")
	testutil.Equal(t, cr.IsError, false)

	// Task must have been rolled to in_review synchronously.
	got, err := d.Get(worker.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, model.StatusInReview)

	// ready_to_close must be stamped.
	meta, _ := d.ListMeta(worker.ID, db.HeraMetaNamespace)
	found := false
	for _, e := range meta {
		if e.Key == db.HeraMetaKeyReadyToClose && e.Value == "true" {
			found = true
		}
	}
	testutil.Equal(t, found, true)
}

// TestHeraSend_Worker_StatusFailed_RollsToReviewNoReadyToClose verifies that a
// worker sending with status=failed rolls its bound task to in_review WITHOUT
// stamping ready_to_close (D2 invariant).
func TestHeraSend_Worker_StatusFailed_RollsToReviewNoReadyToClose(t *testing.T) {
	s, d := testHeraServer(t)
	seedCoordinator(t, s, d, "myorch", "/wt/coord")
	worker := addHeraTestTask(t, d, "/wt/worker")
	attachWorker(t, s, "myorch", worker.Worktree)

	cr := heraSend(t, s, worker.Worktree, "hit a wall", "failed", "failed", "")
	testutil.Equal(t, cr.IsError, false)

	got, err := d.Get(worker.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Status, model.StatusInReview)

	// ready_to_close must NOT be stamped.
	meta, _ := d.ListMeta(worker.ID, db.HeraMetaNamespace)
	for _, e := range meta {
		if e.Key == db.HeraMetaKeyReadyToClose && e.Value == "true" {
			t.Fatalf("ready_to_close was stamped for a failed worker (D2 invariant violated)")
		}
	}
}

// TestHeraSend_Coordinator_NoStatus_Succeeds verifies that a coordinator sender
// with explicit "to" and no status successfully sends the message and leaves the
// coordinator's status unchanged.
func TestHeraSend_Coordinator_NoStatus_Succeeds(t *testing.T) {
	s, d := testHeraServer(t)
	coord := seedCoordinator(t, s, d, "myorch", "/wt/coord")
	worker := addHeraTestTask(t, d, "/wt/worker")
	attachWorker(t, s, "myorch", worker.Worktree)

	// Set coordinator to a known status first so we can verify it's unchanged.
	cr0 := heraStatus(t, s, coord.Worktree, "working")
	testutil.Equal(t, cr0.IsError, false)

	// Coordinator sends without status.
	cr := heraSend(t, s, coord.Worktree, "task for you", "assignment", "", "w1")
	testutil.Equal(t, cr.IsError, false)
	testutil.Contains(t, cr.Content[0].Text, "Message sent")
	testutil.Contains(t, cr.Content[0].Text, "**to**: w1")

	// Coordinator's status must still be "working" (unchanged).
	orch, err := d.HeraOrchestratorByName("myorch")
	testutil.NoError(t, err)
	role, err := d.HeraRoleByName(orch.ID, "coord")
	testutil.NoError(t, err)
	rs, err := d.HeraRoleStatusFor(role.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, rs.Status, db.HeraStatusWorking)
}

// TestHeraSend_StatusApply_IndependentOfDelivery verifies that the status
// mutation is applied synchronously even when the notifier is nil (meaning
// doorbell delivery is skipped). This pins the D1 invariant that status never
// rides the best-effort delivery path.
//
// The testHeraServer wires hera.New(d, nil) — nil notifier — so delivery is
// always skipped in MCP tests, yet status must apply regardless.
func TestHeraSend_StatusApply_IndependentOfDelivery(t *testing.T) {
	s, d := testHeraServer(t)
	seedCoordinator(t, s, d, "myorch", "/wt/coord")
	worker := addHeraTestTask(t, d, "/wt/worker")
	attachWorker(t, s, "myorch", worker.Worktree)

	// nil notifier → delivery is deferred/dropped; status must still apply.
	cr := heraSend(t, s, worker.Worktree, "body", "tldr", "blocked", "")
	testutil.Equal(t, cr.IsError, false)

	orch, err := d.HeraOrchestratorByName("myorch")
	testutil.NoError(t, err)
	role, err := d.HeraRoleByName(orch.ID, "w1")
	testutil.NoError(t, err)
	rs, err := d.HeraRoleStatusFor(role.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, rs.Status, db.HeraStatusBlocked)
}
