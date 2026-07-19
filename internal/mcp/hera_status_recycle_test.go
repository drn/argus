package mcp

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// --- hera_status handoff_note / request_recycle (add-coordinator-context-management) ---
//
// These tests pin the `hera-coordination` delta spec's extension of hera_status
// with two optional, coordinator-only parameters: handoff_note (recorded into
// task_meta) and request_recycle (records a pending-recycle intent, consumed by
// the recycle_coord primitive). Neither parameter exists on toolHeraStatus yet,
// so every test below is expected to fail until Stage 3 (hera_status extension)
// lands. The pending-recycle intent is assumed to be mirrored into task_meta
// (namespace "hera", key "pending_recycle") — the same sidecar convention
// already used for context_size/handoff_note/ready_to_close — since design.md
// does not name an alternate storage location.

// heraStatusExtended calls hera_status with the optional handoff_note /
// request_recycle parameters alongside status, mirroring the heraStatus helper
// in hera_test.go but exposing the new fields under test.
func heraStatusExtended(t *testing.T, s *Server, cwd, status, handoffNote string, requestRecycle bool) ToolCallResult {
	t.Helper()
	args := map[string]interface{}{"cwd": cwd}
	if status != "" {
		args["status"] = status
	}
	if handoffNote != "" {
		args["handoff_note"] = handoffNote
	}
	if requestRecycle {
		args["request_recycle"] = true
	}
	raw, err := json.Marshal(args)
	testutil.NoError(t, err)
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "hera_status",
		Arguments: json.RawMessage(raw),
	})
	testutil.NoError(t, respErr(resp))
	return callResult(t, resp)
}

// attachFreelance joins freelanceWorktree as a freelance role under orch,
// mirroring attachWorker for the freelance kind.
func attachFreelance(t *testing.T, s *Server, orch, freelanceWorktree string) {
	t.Helper()
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_join",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q, "orchestrator": %q, "role_name": "fl1", "kind": "freelance"
		}`, freelanceWorktree, orch)),
	})
	testutil.Equal(t, callResult(t, resp).IsError, false)
}

// metaHas reports whether taskID's "hera" namespace task_meta contains key=value.
func metaHas(t *testing.T, d *db.DB, taskID, key, value string) bool {
	t.Helper()
	meta, err := d.ListMeta(taskID, db.HeraMetaNamespace)
	testutil.NoError(t, err)
	for _, e := range meta {
		if e.Key == key && e.Value == value {
			return true
		}
	}
	return false
}

// TestHeraStatus_Coordinator_HandoffNote_Recorded pins the "Coordinator can
// record a handoff note" scenario: a coordinator calling hera_status with a
// non-empty handoff_note must have it overwritten into task_meta(hera,
// handoff_note) in the same call.
func TestHeraStatus_Coordinator_HandoffNote_Recorded(t *testing.T) {
	s, d := testHeraServer(t)
	coord := seedCoordinator(t, s, d, "myorch", "/wt/coord")

	cr := heraStatusExtended(t, s, coord.Worktree, "working", "wrap up the config change, watch the fan-in", false)
	testutil.Equal(t, cr.IsError, false)

	if !metaHas(t, d, coord.ID, "handoff_note", "wrap up the config change, watch the fan-in") {
		t.Fatalf("task_meta(hera, handoff_note) was not stamped for coordinator handoff_note call")
	}
}

// TestHeraStatus_Coordinator_RequestRecycle_Recorded pins the "Coordinator can
// request recycle" scenario: request_recycle=true must record a pending-recycle
// intent for the caller's task.
func TestHeraStatus_Coordinator_RequestRecycle_Recorded(t *testing.T) {
	s, d := testHeraServer(t)
	coord := seedCoordinator(t, s, d, "myorch", "/wt/coord")

	cr := heraStatusExtended(t, s, coord.Worktree, "working", "", true)
	testutil.Equal(t, cr.IsError, false)

	if !metaHas(t, d, coord.ID, "pending_recycle", "true") {
		t.Fatalf("no pending-recycle intent was recorded for coordinator request_recycle=true call")
	}
}

// TestHeraStatus_Coordinator_HandoffNoteAndRecycle_SameCall pins that a single
// hera_status call can do the harvest-note write and signal recycle intent
// together, per design.md D5 ("one call does the harvest-note write and
// signals recycle intent together, rather than two separate tools").
func TestHeraStatus_Coordinator_HandoffNoteAndRecycle_SameCall(t *testing.T) {
	s, d := testHeraServer(t)
	coord := seedCoordinator(t, s, d, "myorch", "/wt/coord")

	cr := heraStatusExtended(t, s, coord.Worktree, "idle", "handing off cleanly", true)
	testutil.Equal(t, cr.IsError, false)

	if !metaHas(t, d, coord.ID, "handoff_note", "handing off cleanly") {
		t.Fatalf("handoff_note was not stamped alongside request_recycle in the same call")
	}
	if !metaHas(t, d, coord.ID, "pending_recycle", "true") {
		t.Fatalf("pending-recycle intent was not recorded alongside handoff_note in the same call")
	}
}

// TestHeraStatus_NonCoordinator_NewParams_Rejected pins the "Non-coordinator
// cannot use the new parameters" scenario for both worker and freelance
// callers, and for each of the two new parameters individually: the tool must
// error naming the offending parameter, and no task_meta write or recycle
// intent may occur.
func TestHeraStatus_NonCoordinator_NewParams_Rejected(t *testing.T) {
	for _, tc := range []struct {
		name           string
		kind           string
		handoffNote    string
		requestRecycle bool
		wantParamName  string
	}{
		{"worker handoff_note", "worker", "should not be allowed", false, "handoff_note"},
		{"worker request_recycle", "worker", "", true, "request_recycle"},
		{"freelance handoff_note", "freelance", "should not be allowed", false, "handoff_note"},
		{"freelance request_recycle", "freelance", "", true, "request_recycle"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, d := testHeraServer(t)
			seedCoordinator(t, s, d, "myorch", "/wt/coord")

			var caller *model.Task
			switch tc.kind {
			case "worker":
				caller = addHeraTestTask(t, d, "/wt/"+tc.name)
				attachWorker(t, s, "myorch", caller.Worktree)
			case "freelance":
				caller = addHeraTestTask(t, d, "/wt/"+tc.name)
				attachFreelance(t, s, "myorch", caller.Worktree)
			}

			cr := heraStatusExtended(t, s, caller.Worktree, "working", tc.handoffNote, tc.requestRecycle)
			testutil.Equal(t, cr.IsError, true)
			testutil.Contains(t, cr.Content[0].Text, tc.wantParamName)

			// No task_meta write should have occurred for the rejected parameter.
			if tc.handoffNote != "" && metaHas(t, d, caller.ID, "handoff_note", tc.handoffNote) {
				t.Fatalf("handoff_note was written despite rejection")
			}
			if tc.requestRecycle && metaHas(t, d, caller.ID, "pending_recycle", "true") {
				t.Fatalf("pending-recycle intent was recorded despite rejection")
			}
		})
	}
}
