package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/todo"
)

// fakeTodoBackend is a controllable todo.Backend for MCP tool tests. It is
// registered once (in init) under a fixed, test-only name; each test resets
// it via reset() before use rather than depending on test ordering.
type fakeTodoBackend struct {
	createErr, listErr, updateErr, completeErr, deleteErr error
	items                                                 []todo.Item
	nextID                                                int
}

func (f *fakeTodoBackend) reset() { *f = fakeTodoBackend{} }

func (f *fakeTodoBackend) Create(_ context.Context, in todo.CreateInput) (todo.Item, error) {
	if f.createErr != nil {
		return todo.Item{}, f.createErr
	}
	f.nextID++
	item := todo.Item{ID: fmt.Sprintf("fake-%d", f.nextID), Title: in.Title, Notes: in.Notes}
	f.items = append(f.items, item)
	return item, nil
}

func (f *fakeTodoBackend) List(context.Context) ([]todo.Item, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.items, nil
}

func (f *fakeTodoBackend) Update(_ context.Context, id string, in todo.UpdateInput) (todo.Item, error) {
	if f.updateErr != nil {
		return todo.Item{}, f.updateErr
	}
	for i := range f.items {
		if f.items[i].ID == id {
			if in.Title != nil {
				f.items[i].Title = *in.Title
			}
			if in.Notes != nil {
				f.items[i].Notes = *in.Notes
			}
			return f.items[i], nil
		}
	}
	return todo.Item{}, fmt.Errorf("fake: %q not found", id)
}

func (f *fakeTodoBackend) Complete(_ context.Context, id string) (todo.Item, error) {
	if f.completeErr != nil {
		return todo.Item{}, f.completeErr
	}
	for i := range f.items {
		if f.items[i].ID == id {
			f.items[i].Done = true
			return f.items[i], nil
		}
	}
	return todo.Item{}, fmt.Errorf("fake: %q not found", id)
}

func (f *fakeTodoBackend) Delete(_ context.Context, id string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	for i := range f.items {
		if f.items[i].ID == id {
			f.items = append(f.items[:i], f.items[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("fake: %q not found", id)
}

var sharedFakeTodo = &fakeTodoBackend{}

func init() {
	todo.Register("mcptest", func(config.TodoConfig) (todo.Backend, error) { return sharedFakeTodo, nil })
}

// fakeTodoConfigStore lets a test mutate the "live" config between calls, to
// exercise the no-restart-required requirement directly.
type fakeTodoConfigStore struct {
	cfg config.Config
}

func (f *fakeTodoConfigStore) Config() config.Config { return f.cfg }

func testServerWithTodo(t *testing.T) (*Server, *fakeTodoBackend) {
	t.Helper()
	sharedFakeTodo.reset()
	s := testServer()
	s.SetTodoManager(&fakeTodoConfigStore{cfg: config.Config{Todo: config.TodoConfig{Backend: "mcptest"}}})
	return s, sharedFakeTodo
}

func TestToolsList_TodoActive(t *testing.T) {
	s, _ := testServerWithTodo(t)
	resp := doRequest(t, s, "tools/list", nil)
	testutil.NoError(t, respErr(resp))

	result, _ := json.Marshal(resp.Result)
	var list ToolsListResult
	json.Unmarshal(result, &list) //nolint:errcheck

	names := make(map[string]bool)
	for _, tool := range list.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"todo_create", "todo_list", "todo_update", "todo_complete", "todo_delete"} {
		if !names[want] {
			t.Errorf("missing tool: %s", want)
		}
	}
}

func TestToolsList_TodoAbsentWhenUnwired(t *testing.T) {
	s := testServer() // SetTodoManager never called
	resp := doRequest(t, s, "tools/list", nil)
	result, _ := json.Marshal(resp.Result)
	var list ToolsListResult
	json.Unmarshal(result, &list) //nolint:errcheck
	for _, tool := range list.Tools {
		if tool.Name == "todo_create" {
			t.Fatal("todo_create should not be listed when SetTodoManager was never called")
		}
	}
}

func TestToolsList_TodoAbsentWhenBackendEmpty(t *testing.T) {
	s := testServer()
	s.SetTodoManager(&fakeTodoConfigStore{cfg: config.Config{}}) // Todo.Backend == ""
	resp := doRequest(t, s, "tools/list", nil)
	result, _ := json.Marshal(resp.Result)
	var list ToolsListResult
	json.Unmarshal(result, &list) //nolint:errcheck
	for _, tool := range list.Tools {
		if tool.Name == "todo_create" {
			t.Fatal("todo_create should not be listed when no backend is configured")
		}
	}
}

func TestToolsList_TodoAbsentWhenBackendUnknown(t *testing.T) {
	s := testServer()
	s.SetTodoManager(&fakeTodoConfigStore{cfg: config.Config{Todo: config.TodoConfig{Backend: "does-not-exist"}}})
	resp := doRequest(t, s, "tools/list", nil)
	result, _ := json.Marshal(resp.Result)
	var list ToolsListResult
	json.Unmarshal(result, &list) //nolint:errcheck
	for _, tool := range list.Tools {
		if tool.Name == "todo_create" {
			t.Fatal("todo_create should not be listed when the configured backend name is unregistered")
		}
	}
}

// TestToolsList_TodoAppearsLiveNoRestart is the key requirement from the
// spec: a backend selected in Settings (i.e. the config a live TodoConfigStore
// resolves) must show up on the very next tools/list call — no daemon
// restart, no re-wiring of the MCP server.
func TestToolsList_TodoAppearsLiveNoRestart(t *testing.T) {
	sharedFakeTodo.reset()
	s := testServer()
	store := &fakeTodoConfigStore{cfg: config.Config{}} // starts with no backend
	s.SetTodoManager(store)

	resp := doRequest(t, s, "tools/list", nil)
	result, _ := json.Marshal(resp.Result)
	var before ToolsListResult
	json.Unmarshal(result, &before) //nolint:errcheck
	for _, tool := range before.Tools {
		if tool.Name == "todo_create" {
			t.Fatal("todo_create should not yet be listed")
		}
	}

	// Simulate a Settings save: the config the store resolves changes, with
	// no call back into the MCP server at all.
	store.cfg.Todo.Backend = "mcptest"

	resp2 := doRequest(t, s, "tools/list", nil)
	result2, _ := json.Marshal(resp2.Result)
	var after ToolsListResult
	json.Unmarshal(result2, &after) //nolint:errcheck
	found := false
	for _, tool := range after.Tools {
		if tool.Name == "todo_create" {
			found = true
		}
	}
	if !found {
		t.Fatal("todo_create should be listed immediately after the config store reports a backend, with no restart")
	}
}

// TestToolsList_TodoDisappearsLiveNoRestart is the mirror-image spec
// scenario: clearing the backend selection must hide todo_* tools on the
// very next tools/list call, with no restart — the empty→active direction is
// covered above, this covers active→empty.
func TestToolsList_TodoDisappearsLiveNoRestart(t *testing.T) {
	sharedFakeTodo.reset()
	s := testServer()
	store := &fakeTodoConfigStore{cfg: config.Config{Todo: config.TodoConfig{Backend: "mcptest"}}}
	s.SetTodoManager(store)

	resp := doRequest(t, s, "tools/list", nil)
	result, _ := json.Marshal(resp.Result)
	var before ToolsListResult
	json.Unmarshal(result, &before) //nolint:errcheck
	found := false
	for _, tool := range before.Tools {
		if tool.Name == "todo_create" {
			found = true
		}
	}
	if !found {
		t.Fatal("todo_create should be listed while a backend is active")
	}

	// Simulate a Settings clear: the config the store resolves changes back
	// to empty, with no call back into the MCP server at all.
	store.cfg.Todo.Backend = ""

	resp2 := doRequest(t, s, "tools/list", nil)
	result2, _ := json.Marshal(resp2.Result)
	var after ToolsListResult
	json.Unmarshal(result2, &after) //nolint:errcheck
	for _, tool := range after.Tools {
		if tool.Name == "todo_create" {
			t.Fatal("todo_create should no longer be listed immediately after the config store reports no backend, with no restart")
		}
	}
}

// TestTodoToolsActive_LogDedup exercises the lastTodoErr dedup state
// directly (rather than capturing the global log package's output): an
// unknown backend name records the error, and clearing the backend resets
// it — the underlying data the dedup log line in todoToolsActive reads from.
func TestTodoToolsActive_LogDedup(t *testing.T) {
	store := &fakeTodoConfigStore{cfg: config.Config{Todo: config.TodoConfig{Backend: "unknown-backend"}}}
	s := testServer()
	s.SetTodoManager(store)

	testutil.Equal(t, s.todoToolsActive(), false)
	testutil.Contains(t, s.lastTodoErr, "unknown-backend")

	store.cfg.Todo.Backend = ""
	testutil.Equal(t, s.todoToolsActive(), false)
	testutil.Equal(t, s.lastTodoErr, "")
}

func TestToolsCall_TodoUnknownWhenInactive(t *testing.T) {
	s := testServer() // no todo manager wired at all
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "todo_create",
		Arguments: json.RawMessage(`{"title":"x"}`),
	})
	if resp.Error == nil || resp.Error.Code != -32601 {
		t.Fatalf("expected -32601 unknown tool error, got %+v", resp.Error)
	}
}

func TestTodoCreate(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		s, fake := testServerWithTodo(t)
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "todo_create",
			Arguments: json.RawMessage(`{"title":"Buy milk","notes":"2%"}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		if cr.IsError {
			t.Fatalf("unexpected error: %s", cr.Content[0].Text)
		}
		testutil.Contains(t, cr.Content[0].Text, "Buy milk")
		testutil.Equal(t, len(fake.items), 1)
	})

	t.Run("missing title", func(t *testing.T) {
		s, _ := testServerWithTodo(t)
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "todo_create",
			Arguments: json.RawMessage(`{}`),
		})
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "title is required")
	})

	t.Run("backend error surfaces as tool error", func(t *testing.T) {
		s, fake := testServerWithTodo(t)
		fake.createErr = fmt.Errorf("things3: not installed")
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "todo_create",
			Arguments: json.RawMessage(`{"title":"x"}`),
		})
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "not installed")
	})

	t.Run("title over the size cap is rejected", func(t *testing.T) {
		s, fake := testServerWithTodo(t)
		args, _ := json.Marshal(map[string]string{"title": strings.Repeat("x", maxTodoTitleRunes+1)})
		resp := doRequest(t, s, "tools/call", ToolCallParams{Name: "todo_create", Arguments: args})
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "title exceeds")
		testutil.Equal(t, len(fake.items), 0)
	})

	t.Run("notes over the size cap is rejected", func(t *testing.T) {
		s, fake := testServerWithTodo(t)
		args, _ := json.Marshal(map[string]string{"title": "x", "notes": strings.Repeat("x", maxTodoNotesBytes+1)})
		resp := doRequest(t, s, "tools/call", ToolCallParams{Name: "todo_create", Arguments: args})
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "notes exceeds")
		testutil.Equal(t, len(fake.items), 0)
	})
}

func TestTodoList(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		s, _ := testServerWithTodo(t)
		resp := doRequest(t, s, "tools/call", ToolCallParams{Name: "todo_list", Arguments: json.RawMessage(`{}`)})
		cr := callResult(t, resp)
		testutil.Contains(t, cr.Content[0].Text, "No open items")
	})

	t.Run("populated", func(t *testing.T) {
		s, fake := testServerWithTodo(t)
		fake.items = []todo.Item{{ID: "1", Title: "First"}, {ID: "2", Title: "Second", Notes: "n"}}
		resp := doRequest(t, s, "tools/call", ToolCallParams{Name: "todo_list", Arguments: json.RawMessage(`{}`)})
		cr := callResult(t, resp)
		testutil.Contains(t, cr.Content[0].Text, "First")
		testutil.Contains(t, cr.Content[0].Text, "Second")
	})
}

func TestTodoUpdate(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		s, fake := testServerWithTodo(t)
		fake.items = []todo.Item{{ID: "1", Title: "Old"}}
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "todo_update",
			Arguments: json.RawMessage(`{"id":"1","title":"New"}`),
		})
		cr := callResult(t, resp)
		if cr.IsError {
			t.Fatalf("unexpected error: %s", cr.Content[0].Text)
		}
		testutil.Contains(t, cr.Content[0].Text, "New")
	})

	t.Run("missing id", func(t *testing.T) {
		s, _ := testServerWithTodo(t)
		resp := doRequest(t, s, "tools/call", ToolCallParams{Name: "todo_update", Arguments: json.RawMessage(`{"title":"x"}`)})
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "id is required")
	})

	t.Run("unknown id", func(t *testing.T) {
		s, _ := testServerWithTodo(t)
		resp := doRequest(t, s, "tools/call", ToolCallParams{Name: "todo_update", Arguments: json.RawMessage(`{"id":"missing","title":"x"}`)})
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
	})

	t.Run("title over the size cap is rejected", func(t *testing.T) {
		s, fake := testServerWithTodo(t)
		fake.items = []todo.Item{{ID: "1", Title: "Old"}}
		args, _ := json.Marshal(map[string]string{"id": "1", "title": strings.Repeat("x", maxTodoTitleRunes+1)})
		resp := doRequest(t, s, "tools/call", ToolCallParams{Name: "todo_update", Arguments: args})
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "title exceeds")
		testutil.Equal(t, fake.items[0].Title, "Old")
	})

	t.Run("notes over the size cap is rejected", func(t *testing.T) {
		s, fake := testServerWithTodo(t)
		fake.items = []todo.Item{{ID: "1", Title: "Old"}}
		args, _ := json.Marshal(map[string]string{"id": "1", "notes": strings.Repeat("x", maxTodoNotesBytes+1)})
		resp := doRequest(t, s, "tools/call", ToolCallParams{Name: "todo_update", Arguments: args})
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "notes exceeds")
	})
}

func TestTodoComplete(t *testing.T) {
	s, fake := testServerWithTodo(t)
	fake.items = []todo.Item{{ID: "1", Title: "Task"}}
	resp := doRequest(t, s, "tools/call", ToolCallParams{Name: "todo_complete", Arguments: json.RawMessage(`{"id":"1"}`)})
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("unexpected error: %s", cr.Content[0].Text)
	}
	testutil.Contains(t, cr.Content[0].Text, "(done)")
}

func TestTodoDelete(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		s, fake := testServerWithTodo(t)
		fake.items = []todo.Item{{ID: "1", Title: "Task"}}
		resp := doRequest(t, s, "tools/call", ToolCallParams{Name: "todo_delete", Arguments: json.RawMessage(`{"id":"1"}`)})
		cr := callResult(t, resp)
		if cr.IsError {
			t.Fatalf("unexpected error: %s", cr.Content[0].Text)
		}
		testutil.Equal(t, len(fake.items), 0)
	})

	t.Run("missing id", func(t *testing.T) {
		s, _ := testServerWithTodo(t)
		resp := doRequest(t, s, "tools/call", ToolCallParams{Name: "todo_delete", Arguments: json.RawMessage(`{}`)})
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
	})
}
