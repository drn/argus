package mcp

import (
	"encoding/json"
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func sandboxCreateServer(t *testing.T) (*Server, *TaskCreateInput, *int) {
	t.Helper()
	s := testServer()
	tdb := &mockTaskDB{tasks: []*model.Task{
		{ID: "boxed1", Name: "boxed", Project: "myapp", Sandboxed: true, Worktree: "/tmp/worktrees/myapp/boxed"},
		{ID: "open1", Name: "open", Project: "myapp", Worktree: "/tmp/worktrees/myapp/open"},
		{ID: "gone1", Name: "gone", Project: "myapp", Archived: true, Worktree: "/tmp/worktrees/myapp/gone"},
		{ID: "dupbox", Name: "dupbox", Project: "myapp", Sandboxed: true, Worktree: "/tmp/worktrees/myapp/dupbox"},
		{ID: "dupopen", Name: "dupopen", Project: "myapp", Worktree: "/tmp/worktrees/myapp/dupopen"},
	}}
	got := &TaskCreateInput{}
	calls := new(int)
	creator := func(in TaskCreateInput) (*model.Task, error) {
		*calls++
		*got = in
		return &model.Task{ID: "new", Name: in.Name, Project: in.Project, Status: model.StatusInProgress}, nil
	}
	s.SetTaskManager(creator, tdb, &mockStopper{})
	return s, got, calls
}

func TestTaskCreate_SandboxOverride(t *testing.T) {
	for _, tc := range []struct {
		name, args   string
		wantOverride string
		wantErr      string
	}{
		{name: "omitted inherits", args: `{}`},
		{name: "empty inherits", args: `{"sandbox_override":""}`},
		{name: "force enable", args: `{"sandbox_override":"enabled"}`, wantOverride: "enabled"},
		{name: "force enable from sandboxed caller", args: `{"sandbox_override":"enabled","caller_id":"boxed1"}`, wantOverride: "enabled"},
		{name: "whitespace trimmed", args: `{"sandbox_override":" enabled "}`, wantOverride: "enabled"},
		{name: "invalid value", args: `{"sandbox_override":"maybe"}`, wantErr: "is not one of"},
		{name: "disabled from sandboxed caller_id", args: `{"sandbox_override":"disabled","caller_id":"boxed1"}`, wantErr: "not allowed from sandboxed task boxed1"},
		{name: "disabled from sandboxed cwd", args: `{"sandbox_override":"disabled","cwd":"/tmp/worktrees/myapp/boxed/sub"}`, wantErr: "not allowed from sandboxed task"},
		{name: "disabled from unsandboxed caller_id", args: `{"sandbox_override":"disabled","caller_id":"open1"}`, wantOverride: "disabled"},
		{name: "disabled from unsandboxed cwd", args: `{"sandbox_override":"disabled","cwd":"/tmp/worktrees/myapp/open"}`, wantOverride: "disabled"},
		{name: "disabled with no caller", args: `{"sandbox_override":"disabled"}`, wantErr: "pass caller_id or cwd"},
		{name: "disabled from archived caller", args: `{"sandbox_override":"disabled","caller_id":"gone1"}`, wantErr: "requires a live calling task; gone1 is archived"},
		{name: "caller_id wins over cwd", args: `{"sandbox_override":"disabled","caller_id":"boxed1","cwd":"/tmp/worktrees/myapp/open"}`, wantErr: "not allowed from sandboxed task boxed1"},
		{name: "disabled with unresolvable caller", args: `{"sandbox_override":"disabled","cwd":"/nowhere"}`, wantErr: "requires an identified calling task"},
		{name: "disabled with unknown caller_id", args: `{"sandbox_override":"disabled","caller_id":"ghost"}`, wantErr: "requires an identified calling task"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, got, calls := sandboxCreateServer(t)
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
				testutil.Equal(t, *calls, 0)
				testutil.Equal(t, s.creating, 0)
				return
			}
			testutil.False(t, cr.IsError)
			testutil.Equal(t, *calls, 1)
			testutil.Equal(t, got.SandboxOverride, tc.wantOverride)
		})
	}
}

func TestTaskCreate_SchemaDocumentsSandboxOverride(t *testing.T) {
	for _, def := range taskToolDefs {
		if def.Name != "task_create" {
			continue
		}
		props := def.InputSchema.(map[string]interface{})["properties"].(map[string]interface{})
		prop, ok := props["sandbox_override"].(map[string]interface{})
		testutil.True(t, ok)
		testutil.Equal(t, prop["type"], "string")
		_, hasEnum := prop["enum"]
		testutil.True(t, !hasEnum)
		testutil.Contains(t, prop["description"].(string), "disabled")
		testutil.Contains(t, def.Description, "sandbox_override")
		return
	}
	t.Fatal("task_create not found")
}

func TestTaskCreate_SandboxOverrideUpsert(t *testing.T) {
	for _, tc := range []struct {
		name, taskName, override string
		wantErr                  bool
	}{
		{name: "matching enabled", taskName: "dupbox", override: "enabled"},
		{name: "no override accepts any", taskName: "dupbox", override: ""},
		{name: "enabled on unsandboxed conflicts", taskName: "dupopen", override: "enabled", wantErr: true},
		{name: "disabled on sandboxed conflicts", taskName: "dupbox", override: "disabled", wantErr: true},
		{name: "matching disabled", taskName: "dupopen", override: "disabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, calls := sandboxCreateServer(t)
			args := map[string]any{"prompt": "do it", "project": "myapp", "name": tc.taskName, "upsert": true, "caller_id": "open1"}
			if tc.override != "" {
				args["sandbox_override"] = tc.override
			}
			raw, _ := json.Marshal(args)
			resp := doRequest(t, s, "tools/call", ToolCallParams{Name: "task_create", Arguments: raw})
			testutil.NoError(t, respErr(resp))
			cr := callResult(t, resp)
			testutil.Equal(t, cr.IsError, tc.wantErr)
			testutil.Equal(t, *calls, 0)
			if tc.wantErr {
				testutil.Contains(t, cr.Content[0].Text, "conflicts with sandbox_override")
			}
		})
	}
}
