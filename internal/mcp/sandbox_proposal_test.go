package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/sandboxprop"
	"github.com/drn/argus/internal/testutil"
)

type mockProposer struct {
	task  *model.Task
	items []sandboxprop.Item
	res   *sandboxprop.ProposeResult
	err   error
}

func (m *mockProposer) Propose(task *model.Task, items []sandboxprop.Item) (*sandboxprop.ProposeResult, error) {
	m.task, m.items = task, items
	return m.res, m.err
}

func sbxToolNames(t *testing.T, s *Server) map[string]bool {
	t.Helper()
	resp := doRequest(t, s, "tools/list", nil)
	testutil.NoError(t, respErr(resp))
	raw, _ := json.Marshal(resp.Result)
	var list ToolsListResult
	json.Unmarshal(raw, &list) //nolint:errcheck
	names := map[string]bool{}
	for _, tl := range list.Tools {
		names[tl.Name] = true
	}
	return names
}

func TestSandboxPropose_ListedOnlyWhenWired(t *testing.T) {
	s, _, _ := testServerWithTasks()
	testutil.Equal(t, sbxToolNames(t, s)["sandbox_propose_write_paths"], false)
	s.SetSandboxProposer(&mockProposer{})
	testutil.Equal(t, sbxToolNames(t, s)["sandbox_propose_write_paths"], true)

	// Without task management the tool stays hidden and calling it errors.
	bare := testServer()
	bare.SetSandboxProposer(&mockProposer{})
	testutil.Equal(t, sbxToolNames(t, bare)["sandbox_propose_write_paths"], false)
}

func TestNoSandboxDecisionTool(t *testing.T) {
	s, _, _ := testServerWithTasks()
	s.SetSandboxProposer(&mockProposer{})
	for name := range sbxToolNames(t, s) {
		if strings.Contains(name, "sandbox") {
			testutil.Equal(t, name, "sandbox_propose_write_paths")
		}
		for _, bad := range []string{"approve", "reject", "apply"} {
			if strings.Contains(name, bad) && strings.Contains(name, "sandbox") {
				t.Fatalf("agent-reachable decision tool exposed: %s", name)
			}
		}
	}
}

func TestSandboxPropose_Call(t *testing.T) {
	s, _, _ := testServerWithTasks()
	m := &mockProposer{res: &sandboxprop.ProposeResult{Warnings: []string{"~/.ssh: sensitive"}}}
	m.res.Proposal = newProposalForTest("p1")
	s.SetSandboxProposer(m)
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "sandbox_propose_write_paths",
		Arguments: json.RawMessage(`{"cwd":"/tmp/worktrees/myapp/fix-login","paths":[{"path":"~/.gem","reason":"gems"}]}`),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, false)
	testutil.Equal(t, m.task.ID, "abc123")
	testutil.DeepEqual(t, m.items, []sandboxprop.Item{{Path: "~/.gem", Reason: "gems"}})
	testutil.Contains(t, cr.Content[0].Text, "PENDING")
	testutil.Contains(t, cr.Content[0].Text, "Warnings")

	m.res.Merged = true
	resp = doRequest(t, s, "tools/call", ToolCallParams{Name: "sandbox_propose_write_paths", Arguments: json.RawMessage(`{"id":"abc123","paths":[{"path":"~/.gem"}]}`)})
	testutil.Contains(t, callResult(t, resp).Content[0].Text, "already pending")
}

func TestSandboxPropose_Errors(t *testing.T) {
	s, _, _ := testServerWithTasks()
	m := &mockProposer{err: errBoom}
	s.SetSandboxProposer(m)
	for _, args := range []string{`{"paths":[{"path":"~/.gem"}]}`, `{"id":"abc123","paths":[{"path":"~/.gem"}]}`} {
		resp := doRequest(t, s, "tools/call", ToolCallParams{Name: "sandbox_propose_write_paths", Arguments: json.RawMessage(args)})
		testutil.Equal(t, callResult(t, resp).IsError, true)
	}
}

var errBoom = errorString("boom")

type errorString string

func (e errorString) Error() string { return string(e) }
