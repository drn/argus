package mcp

import (
	"encoding/json"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// configTaskDB adds the Config() seam account validation reads.
type configTaskDB struct {
	*mockTaskDB
	cfg config.Config
}

func (c configTaskDB) Config() config.Config { return c.cfg }

func accountCreateServer(t *testing.T) (*Server, *TaskCreateInput) {
	t.Helper()
	s := testServer()
	tdb := configTaskDB{
		mockTaskDB: &mockTaskDB{tasks: []*model.Task{
			{ID: "caller1", Name: "caller", Project: "myapp", Account: "work", Worktree: "/tmp/worktrees/myapp/caller"},
			{ID: "plain1", Name: "plain", Project: "myapp", Worktree: "/tmp/worktrees/myapp/plain"},
		}},
		cfg: config.Config{Accounts: map[string]config.Account{
			"work":     {ClaudeConfigDir: "/tmp/work"},
			"personal": {ClaudeConfigDir: "/tmp/personal"},
		}},
	}
	got := &TaskCreateInput{}
	creator := func(in TaskCreateInput) (*model.Task, error) {
		*got = in
		return &model.Task{ID: "new", Name: in.Name, Project: in.Project, Status: model.StatusInProgress}, nil
	}
	s.SetTaskManager(creator, tdb, &mockStopper{})
	return s, got
}

func TestTaskCreate_Account(t *testing.T) {
	for _, tc := range []struct {
		name, args    string
		wantAccount   string
		wantInherited bool
		wantErr       string
	}{
		{name: "no caller, no account", args: `{}`},
		{name: "caller_id inherits", args: `{"caller_id":"caller1"}`, wantAccount: "work", wantInherited: true},
		{name: "cwd inherits", args: `{"cwd":"/tmp/worktrees/myapp/caller/sub"}`, wantAccount: "work", wantInherited: true},
		{name: "explicit overrides caller", args: `{"caller_id":"caller1","account":"personal"}`, wantAccount: "personal"},
		{name: "explicit default overrides caller", args: `{"caller_id":"caller1","account":"default"}`, wantAccount: "default"},
		{name: "default-account caller pins default", args: `{"caller_id":"plain1"}`, wantAccount: "default", wantInherited: true},
		{name: "unresolvable caller inherits nothing", args: `{"cwd":"/nowhere"}`},
		{name: "unknown explicit account rejected", args: `{"account":"nope"}`, wantErr: "unknown account"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, got := accountCreateServer(t)
			var args map[string]any
			testutil.NoError(t, json.Unmarshal([]byte(tc.args), &args))
			args["prompt"], args["project"] = "do it", "myapp"
			raw, _ := json.Marshal(args)
			resp := doRequest(t, s, "tools/call", ToolCallParams{Name: "task_create", Arguments: raw})
			testutil.NoError(t, respErr(resp))
			cr := callResult(t, resp)
			if tc.wantErr != "" {
				testutil.True(t, cr.IsError)
				testutil.Contains(t, cr.Content[0].Text, tc.wantErr)
				return
			}
			testutil.False(t, cr.IsError)
			testutil.Equal(t, got.Account, tc.wantAccount)
			testutil.Equal(t, got.InheritedAccount, tc.wantInherited)
		})
	}
}

func TestTaskCreate_SchemaDocumentsAccount(t *testing.T) {
	for _, def := range taskToolDefs {
		if def.Name != "task_create" {
			continue
		}
		props := def.InputSchema.(map[string]interface{})["properties"].(map[string]interface{})
		for _, k := range []string{"account", "caller_id", "cwd"} {
			_, ok := props[k]
			testutil.True(t, ok)
		}
		testutil.Contains(t, def.Description, "inherits the calling task's account")
		return
	}
	t.Fatal("task_create not found")
}
