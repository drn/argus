package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
)

func TestHeraBoardTools(t *testing.T) {
	s, d := testHeraServer(t)
	task := seedCoordinator(t, s, d, "board", "/wt/board")
	b, err := d.HeraLiveBindingByTask(task.ID)
	testutil.NoError(t, err)
	call := func(name string, args string) ToolCallResult {
		t.Helper()
		return callResult(t, doRequest(t, s, "tools/call", ToolCallParams{Name: name, Arguments: json.RawMessage(args)}))
	}
	post := call("hera_board_post", `{"cwd":"/wt/board","kind":"decision","subject":"Choose rollout","body":"Context"}`)
	testutil.Equal(t, post.IsError, false)
	items, err := d.ListHeraBoardItems(b.RoleID, false)
	testutil.NoError(t, err)
	testutil.Equal(t, len(items), 1)
	id := items[0].ID
	testutil.NoError(t, d.ReadHeraBoardItem(b.RoleID, id))
	update := call("hera_board_update", fmt.Sprintf(`{"cwd":"/wt/board","id":%d,"kind":"decision","subject":"Choose rollout","body":"Updated"}`, id))
	testutil.Equal(t, update.IsError, false)
	list := call("hera_board_list", `{"cwd":"/wt/board"}`)
	testutil.Equal(t, list.IsError, false)
	testutil.Contains(t, list.Content[0].Text, "Updated")
	for _, tool := range []string{"hera_join", "hera_status"} {
		t.Run(tool, func(t *testing.T) {
			got := call(tool, `{"cwd":"/wt/board","status":"working"}`)
			testutil.Contains(t, got.Content[0].Text, fmt.Sprintf("#%d", id))
			testutil.Contains(t, got.Content[0].Text, "Choose rollout")
		})
	}
	worker := addHeraTestTask(t, d, "/wt/worker")
	role, _, err := d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{OrchestratorID: b.OrchestratorID, Name: "worker", Kind: db.HeraKindWorker}, worker.ID, worker.Worktree)
	testutil.NoError(t, err)
	testutil.Equal(t, role.Kind, db.HeraKindWorker)
	for _, verb := range []string{"post", "update", "resolve", "list"} {
		t.Run("worker_"+verb, func(t *testing.T) {
			got := call("hera_board_"+verb, fmt.Sprintf(`{"cwd":"/wt/worker","id":%d,"kind":"fyi","subject":"bad","body":"x"}`, id))
			testutil.Equal(t, got.IsError, true)
			testutil.Contains(t, got.Content[0].Text, "coordinator binding")
		})
	}
	resolved := call("hera_board_resolve", fmt.Sprintf(`{"cwd":"/wt/board","id":%d,"note":"Settled"}`, id))
	testutil.Equal(t, resolved.IsError, false)
	all, err := d.ListHeraBoardItems(b.RoleID, true)
	testutil.NoError(t, err)
	testutil.Equal(t, all[0].ResolvedBy, "agent")
	testutil.Equal(t, all[0].ResolutionNote, "Settled")
}

// Embedding only HeraStore deliberately hides the optional board capability.
type boardUnavailableStore struct{ HeraStore }

func TestHeraBoardToolErrors(t *testing.T) {
	s, d := testHeraServer(t)
	seedCoordinator(t, s, d, "board", "/wt/board")
	for _, tc := range []struct{ name, args, want string }{
		{"invalid JSON", `{`, "invalid board arguments"},
		{"missing cwd", `{}`, "cwd is required"},
		{"unbound", `{"cwd":"/wt/missing"}`, "no task matches cwd"},
		{"invalid kind", `{"cwd":"/wt/board","kind":"bad","subject":"s","body":"b"}`, "invalid item"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := callResult(t, s.toolHeraBoard(1, json.RawMessage(tc.args), "post"))
			testutil.Equal(t, got.IsError, true)
			testutil.Contains(t, got.Content[0].Text, tc.want)
		})
	}
	original := s.heraStore
	s.heraStore = boardUnavailableStore{original}
	got := callResult(t, s.toolHeraBoard(1, json.RawMessage(`{"cwd":"/wt/board"}`), "list"))
	testutil.Equal(t, got.IsError, true)
	testutil.Contains(t, got.Content[0].Text, "board store unavailable")
	s.heraStore = nil
	s.heraSvc = nil
	got = callResult(t, s.toolHeraBoard(1, json.RawMessage(`{"cwd":"/wt/board"}`), "list"))
	testutil.Equal(t, got.IsError, true)
	testutil.Contains(t, got.Content[0].Text, "hera not configured")
}

func TestHeraBoardDismissalInboxSender(t *testing.T) {
	s, d := testHeraServer(t)
	task := seedCoordinator(t, s, d, "board", "/wt/board")
	b, err := d.HeraLiveBindingByTask(task.ID)
	testutil.NoError(t, err)
	i, err := d.PostHeraBoardItem(b.RoleID, "decision", "Choose", "body")
	testutil.NoError(t, err)
	testutil.NoError(t, d.ResolveHeraBoardItem(b.RoleID, i.ID, "human", ""))
	got := callResult(t, doRequest(t, s, "tools/call", ToolCallParams{Name: "hera_inbox", Arguments: json.RawMessage(`{"cwd":"/wt/board"}`)}))
	testutil.False(t, got.IsError)
	testutil.Contains(t, got.Content[0].Text, "from: Aaron (board)")
	testutil.False(t, strings.Contains(got.Content[0].Text, "from: coord"))
}
