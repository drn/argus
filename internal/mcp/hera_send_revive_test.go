package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
)

// --- hera_send auto-revive (add-hera-send-auto-revive) ---
//
// These tests exercise toolHeraSend's new auto-revive attempt using the same
// fakeHeraReviver harness hera_revive_test.go defines: they assert whether
// the reviver was called, with what args, and how the outcome (or its
// absence) is rendered in the hera_send response — never a real PTY/runner.

func TestHeraSend_AutoRevive_DeadRecipientRestartedBeforeSend(t *testing.T) {
	s, d := testHeraServer(t)
	coordWt, workerWt := setupOrchWithWorker(t, s, d)
	_ = workerWt

	fr := &fakeHeraReviver{outcome: "restarted_dead"}
	s.SetHeraReviver(fr.reviver())

	orch, err := d.HeraOrchestratorByName("test-orch")
	testutil.NoError(t, err)
	workerRole, err := d.HeraRoleByName(orch.ID, "w1")
	testutil.NoError(t, err)
	binding, err := d.HeraLiveBindingByRole(workerRole.ID)
	testutil.NoError(t, err)

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_send",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"to":"w1","body":"wake up","tldr":"wake"
		}`, coordWt)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("expected success, got error: %s", cr.Content[0].Text)
	}
	testutil.Equal(t, fr.called, true)
	testutil.Equal(t, fr.calledWith.TaskID, binding.ArgusTaskID)
	testutil.Equal(t, fr.calledWith.IsCoordinator, false)
	testutil.Contains(t, cr.Content[0].Text, "- **revive**:")
	testutil.Contains(t, cr.Content[0].Text, "restarted")
	testutil.Contains(t, cr.Content[0].Text, "Message sent")
}

func TestHeraSend_AutoRevive_SkipOutcomesStillDeliver(t *testing.T) {
	for _, outcome := range []string{
		"skipped_busy",
		"skipped_blocked_on_prompt",
		"skipped_coordinator_live",
		"kicked_stuck",
	} {
		t.Run(outcome, func(t *testing.T) {
			s, d := testHeraServer(t)
			coordWt, _ := setupOrchWithWorker(t, s, d)

			fr := &fakeHeraReviver{outcome: outcome}
			s.SetHeraReviver(fr.reviver())

			resp := doRequest(t, s, "tools/call", ToolCallParams{
				Name: "hera_send",
				Arguments: json.RawMessage(fmt.Sprintf(`{
					"cwd":%q,"to":"w1","body":"hi","tldr":"hi"
				}`, coordWt)),
			})
			testutil.NoError(t, respErr(resp))
			cr := callResult(t, resp)
			if cr.IsError {
				t.Fatalf("expected success, got error: %s", cr.Content[0].Text)
			}
			testutil.Equal(t, fr.called, true)
			testutil.Contains(t, cr.Content[0].Text, "- **revive**:")
			testutil.Contains(t, cr.Content[0].Text, "Message sent")
			testutil.Contains(t, cr.Content[0].Text, "**to**: w1")
		})
	}
}

func TestHeraSend_AutoRevive_NoLiveBindingSkipsSilentlyAndSendSucceeds(t *testing.T) {
	s, d := testHeraServer(t)
	coordTask := seedCoordinator(t, s, d, "O", "/wt/coord")

	orch, err := d.HeraOrchestratorByName("O")
	testutil.NoError(t, err)
	_, err = d.CreateHeraPlannedRole(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "planned-1", Kind: db.HeraKindWorker, ArgusProject: "test-project", Prompt: "later",
	})
	testutil.NoError(t, err)

	fr := &fakeHeraReviver{outcome: "restarted_dead"}
	s.SetHeraReviver(fr.reviver())

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_send",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"to":"planned-1","body":"hi","tldr":"hi"
		}`, coordTask.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("expected success (message still stored/attempted), got error: %s", cr.Content[0].Text)
	}
	testutil.Equal(t, fr.called, false)
	if strings.Contains(cr.Content[0].Text, "- **revive**:") {
		t.Fatalf("expected no revive line when recipient has no live binding, got: %s", cr.Content[0].Text)
	}
	testutil.Contains(t, cr.Content[0].Text, "Message sent")
}

func TestHeraSend_AutoRevive_ReviverNilDoesNotBlockSend(t *testing.T) {
	s, d := testHeraServer(t)
	coordWt, _ := setupOrchWithWorker(t, s, d)

	// testHeraServer does NOT call SetHeraReviver — s.heraRevive stays nil.
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_send",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"to":"w1","body":"hi","tldr":"hi"
		}`, coordWt)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("expected success, got error: %s", cr.Content[0].Text)
	}
	if strings.Contains(cr.Content[0].Text, "- **revive**:") {
		t.Fatalf("expected no revive line when no reviver is wired, got: %s", cr.Content[0].Text)
	}
	testutil.Contains(t, cr.Content[0].Text, "Message sent")
}

func TestHeraSend_AutoRevive_ReviveErrorDoesNotBlockSend(t *testing.T) {
	s, d := testHeraServer(t)
	coordWt, _ := setupOrchWithWorker(t, s, d)

	fr := &fakeHeraReviver{err: errors.New("boom")}
	s.SetHeraReviver(fr.reviver())

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_send",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"to":"w1","body":"hi","tldr":"hi"
		}`, coordWt)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("expected success despite revive error, got error: %s", cr.Content[0].Text)
	}
	testutil.Equal(t, fr.called, true)
	if strings.Contains(cr.Content[0].Text, "- **revive**:") {
		t.Fatalf("expected no revive line when the revive call itself errors, got: %s", cr.Content[0].Text)
	}
	testutil.Contains(t, cr.Content[0].Text, "Message sent")
}

func TestHeraSend_AutoRevive_WorkerDefaultRouteNeverTriggers(t *testing.T) {
	s, d := testHeraServer(t)
	_, workerWt := setupOrchWithWorker(t, s, d)

	fr := &fakeHeraReviver{outcome: "restarted_dead"}
	s.SetHeraReviver(fr.reviver())

	// Worker sends with no explicit "to" → defaults to the (live) coordinator.
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_send",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"body":"status","tldr":"status","status":"working"
		}`, workerWt)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("expected success, got error: %s", cr.Content[0].Text)
	}
	testutil.Equal(t, fr.called, false)
	if strings.Contains(cr.Content[0].Text, "- **revive**:") {
		t.Fatalf("expected no revive line on the default worker->coordinator route, got: %s", cr.Content[0].Text)
	}
}

func TestHeraSend_AutoRevive_SelfSendNeverTriggers(t *testing.T) {
	s, d := testHeraServer(t)
	coordWt, _ := setupOrchWithWorker(t, s, d)

	fr := &fakeHeraReviver{outcome: "restarted_dead"}
	s.SetHeraReviver(fr.reviver())

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_send",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"to":"coord","body":"hi","tldr":"hi"
		}`, coordWt)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	if !cr.IsError {
		t.Fatal("expected the existing self-send rejection")
	}
	testutil.Contains(t, cr.Content[0].Text, "cannot send a message to self")
	testutil.Equal(t, fr.called, false)
}
