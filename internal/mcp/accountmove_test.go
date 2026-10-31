package mcp

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func callMove(t *testing.T, s *Server, args string) ToolCallResult {
	t.Helper()
	resp := doRequest(t, s, "tools/call", ToolCallParams{Name: "task_move_account", Arguments: json.RawMessage(args)})
	testutil.NoError(t, respErr(resp))
	return callResult(t, resp)
}

func TestTaskMoveAccount_ListedOnlyWhenWired(t *testing.T) {
	s := testServer()
	testutil.False(t, toolNames(t, s)["task_move_account"])
	s.SetAccountMover(func(agent.AccountMoveInput) (agent.AccountMoveResult, error) { return agent.AccountMoveResult{}, nil })
	testutil.True(t, toolNames(t, s)["task_move_account"])
}

func TestTaskMoveAccount_UnwiredCallIsError(t *testing.T) {
	cr := callMove(t, testServer(), `{"task_id":"w1","account":"auto"}`)
	testutil.True(t, cr.IsError)
}

func TestTaskMoveAccount_Call(t *testing.T) {
	var got agent.AccountMoveInput
	s := testServer()
	s.SetTaskManager(func(TaskCreateInput) (*model.Task, error) { return nil, nil }, &mockTaskDB{tasks: []*model.Task{
		{ID: "coord1", Name: "coord", Project: "p", Worktree: "/tmp/worktrees/p/coord"},
	}}, &mockStopper{})
	s.SetAccountMover(func(in agent.AccountMoveInput) (agent.AccountMoveResult, error) {
		got = in
		if in.TaskID == "busy" {
			return agent.AccountMoveResult{}, agent.ErrAccountMoveBusy
		}
		return agent.AccountMoveResult{From: "default", To: "personal", Resumed: true, Started: true}, nil
	})

	t.Run("coordinator moves a worker", func(t *testing.T) {
		cr := callMove(t, s, `{"task_id":"w1","account":"auto","caller_id":"coord1","force":true}`)
		testutil.False(t, cr.IsError)
		testutil.Equal(t, got, agent.AccountMoveInput{TaskID: "w1", Account: "auto", Force: true})
		testutil.Contains(t, cr.Content[0].Text, `"to":"personal"`)
	})
	t.Run("self-move by caller_id rejected", func(t *testing.T) {
		got = agent.AccountMoveInput{}
		cr := callMove(t, s, `{"task_id":"coord1","account":"personal","caller_id":"coord1"}`)
		testutil.True(t, cr.IsError)
		testutil.Contains(t, cr.Content[0].Text, "cannot move itself")
		testutil.Equal(t, got.TaskID, "")
	})
	t.Run("self-move by cwd rejected", func(t *testing.T) {
		cr := callMove(t, s, `{"task_id":"coord1","account":"personal","cwd":"/tmp/worktrees/p/coord/sub"}`)
		testutil.True(t, cr.IsError)
		testutil.Contains(t, cr.Content[0].Text, "cannot move itself")
	})
	t.Run("missing arguments", func(t *testing.T) {
		testutil.True(t, callMove(t, s, `{"account":"auto"}`).IsError)
		testutil.True(t, callMove(t, s, `{"task_id":"w1"}`).IsError)
	})
	t.Run("move error surfaces", func(t *testing.T) {
		cr := callMove(t, s, `{"task_id":"busy","account":"auto"}`)
		testutil.True(t, cr.IsError)
		testutil.Contains(t, cr.Content[0].Text, "force")
	})
	t.Run("generic error surfaces", func(t *testing.T) {
		s.SetAccountMover(func(agent.AccountMoveInput) (agent.AccountMoveResult, error) {
			return agent.AccountMoveResult{}, errors.New("boom")
		})
		cr := callMove(t, s, `{"task_id":"w1","account":"auto"}`)
		testutil.True(t, cr.IsError)
		testutil.Contains(t, cr.Content[0].Text, "boom")
	})
}

func TestTaskMoveAccount_Description(t *testing.T) {
	d := accountMoveToolDefs[0].Description
	testutil.Contains(t, d, "copies the conversation")
	testutil.Contains(t, d, "/api/accounts")
}
