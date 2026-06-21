package mcp

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// --- hera_status("failed") D2 tests ---

// TestHera_Status_Failed_Accepted verifies that "failed" is now a valid status
// value (previously the enum was idle|working|blocked|done only).
func TestHera_Status_Failed_Accepted(t *testing.T) {
	s, d := testHeraServer(t)
	seedCoordinator(t, s, d, "myorch", "/wt/coord")
	worker := addHeraTestTask(t, d, "/wt/worker")
	attachWorker(t, s, "myorch", worker.Worktree)

	cr := heraStatus(t, s, worker.Worktree, "failed")
	testutil.Equal(t, cr.IsError, false)
	testutil.Contains(t, cr.Content[0].Text, "**status**: failed")
}

// TestHera_Status_Failed_InvalidEnumNamesAllFive verifies that the invalid-status
// error message now enumerates all five values including "failed".
func TestHera_Status_Failed_InvalidEnumNamesAllFive(t *testing.T) {
	s, d := testHeraServer(t)
	task := addHeraTestTask(t, d, "/wt/status-five")

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_status",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"status":"winning"
		}`, task.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "failed")
	testutil.Contains(t, cr.Content[0].Text, "done")
	testutil.Contains(t, cr.Content[0].Text, "idle")
}

// TestHera_Status_Failed_RollsWorkerToReview verifies that a worker reporting
// status="failed" gets its bound task rolled to in_review.
func TestHera_Status_Failed_RollsWorkerToReview(t *testing.T) {
	s, d := testHeraServer(t)
	seedCoordinator(t, s, d, "myorch", "/wt/coord")
	worker := addHeraTestTask(t, d, "/wt/worker")
	attachWorker(t, s, "myorch", worker.Worktree)

	cr := heraStatus(t, s, worker.Worktree, "failed")
	testutil.Equal(t, cr.IsError, false)

	got, _ := d.Get(worker.ID)
	testutil.Equal(t, got.Status, model.StatusInReview)
}

// TestHera_Status_Failed_NoReadyToClose is the D2 key invariant: a worker
// reporting "failed" must NOT stamp ready_to_close — the task is not done, just
// failed and surfaced for coordinator attention.
func TestHera_Status_Failed_NoReadyToClose(t *testing.T) {
	s, d := testHeraServer(t)
	seedCoordinator(t, s, d, "myorch", "/wt/coord")
	worker := addHeraTestTask(t, d, "/wt/worker")
	attachWorker(t, s, "myorch", worker.Worktree)

	cr := heraStatus(t, s, worker.Worktree, "failed")
	testutil.Equal(t, cr.IsError, false)

	meta, _ := d.ListMeta(worker.ID, db.HeraMetaNamespace)
	for _, e := range meta {
		if e.Key == db.HeraMetaKeyReadyToClose && e.Value == "true" {
			t.Fatalf("ready_to_close was stamped for a failed worker (D2 invariant violated)")
		}
	}
}

// TestHera_Status_Failed_CoordinatorUnchanged verifies coordinators are not
// rolled when they report "failed" (the roll is worker-kind only).
func TestHera_Status_Failed_CoordinatorUnchanged(t *testing.T) {
	s, d := testHeraServer(t)
	coord := seedCoordinator(t, s, d, "myorch", "/wt/coord")

	cr := heraStatus(t, s, coord.Worktree, "failed")
	testutil.Equal(t, cr.IsError, false)

	got, _ := d.Get(coord.ID)
	testutil.Equal(t, got.Status, model.StatusInProgress) // NOT rolled
}

// TestHera_Status_Failed_DoesNotClobberComplete verifies idempotency when the
// task is already terminal (same guard as the done roll).
func TestHera_Status_Failed_DoesNotClobberComplete(t *testing.T) {
	s, d := testHeraServer(t)
	seedCoordinator(t, s, d, "myorch", "/wt/coord")
	worker := addHeraTestTask(t, d, "/wt/worker")
	attachWorker(t, s, "myorch", worker.Worktree)
	testutil.NoError(t, d.SetStatus(worker.ID, model.StatusComplete))

	cr := heraStatus(t, s, worker.Worktree, "failed")
	testutil.Equal(t, cr.IsError, false)

	got, _ := d.Get(worker.ID)
	testutil.Equal(t, got.Status, model.StatusComplete) // not clobbered
}
