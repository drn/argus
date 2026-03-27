package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/kb"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// mockDB implements KBQuerier for testing.
type mockDB struct {
	docs []kb.Document
}

func (m *mockDB) KBSearch(query string, limit int) ([]kb.SearchResult, error) {
	var results []kb.SearchResult
	for _, d := range m.docs {
		results = append(results, kb.SearchResult{Document: d, Snippet: "...", Rank: -1.0})
	}
	return results, nil
}

func (m *mockDB) KBGet(path string) (*kb.Document, error) {
	for _, d := range m.docs {
		if d.Path == path {
			return &d, nil
		}
	}
	return nil, &notFoundErr{path}
}

func (m *mockDB) KBList(prefix string, limit int) ([]kb.Document, error) {
	return m.docs, nil
}

func (m *mockDB) KBUpsert(doc *kb.Document) error {
	m.docs = append(m.docs, *doc)
	return nil
}

func (m *mockDB) KBDocumentCount() int {
	return len(m.docs)
}

type notFoundErr struct{ path string }

func (e *notFoundErr) Error() string { return "not found: " + e.path }

func testServer() *Server {
	db := &mockDB{
		docs: []kb.Document{
			{
				Path:       "notes/test.md",
				Title:      "Test Document",
				Body:       "Full body content here.",
				Tags:       []string{"test"},
				Tier:       "hot",
				WordCount:  4,
				ModifiedAt: time.Now(),
				IngestedAt: time.Now(),
			},
		},
	}
	return New(db, 7742)
}

func doRequest(t *testing.T, s *Server, method string, params interface{}) *Response {
	t.Helper()
	reqBody := Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  method,
	}
	if params != nil {
		raw, _ := json.Marshal(params)
		reqBody.Params = raw
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return &resp
}

func TestInitialize(t *testing.T) {
	s := testServer()
	resp := doRequest(t, s, "initialize", map[string]interface{}{
		"protocolVersion": "2025-06-18",
		"clientInfo":      map[string]string{"name": "test", "version": "1.0"},
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("result is not a map: %T", resp.Result)
	}
	// Should echo client's protocol version (Codex workaround).
	if result["protocolVersion"] != "2025-06-18" {
		t.Errorf("protocolVersion: got %v, want 2025-06-18", result["protocolVersion"])
	}
}

func TestToolsList(t *testing.T) {
	s := testServer()
	resp := doRequest(t, s, "tools/list", nil)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	result, _ := json.Marshal(resp.Result)
	var list ToolsListResult
	if err := json.Unmarshal(result, &list); err != nil {
		t.Fatalf("unmarshal ToolsListResult: %v", err)
	}
	if len(list.Tools) != 4 {
		t.Errorf("tools count: got %d, want 4", len(list.Tools))
	}

	names := make(map[string]bool)
	for _, tool := range list.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"kb_search", "kb_read", "kb_list", "kb_ingest"} {
		if !names[want] {
			t.Errorf("missing tool: %s", want)
		}
	}
}

func TestToolsCall_KBSearch(t *testing.T) {
	s := testServer()
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "kb_search",
		Arguments: json.RawMessage(`{"query": "test", "limit": 5}`),
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	if resp.Result == nil {
		t.Fatal("result is nil")
	}
}

func TestToolsCall_KBRead(t *testing.T) {
	s := testServer()
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "kb_read",
		Arguments: json.RawMessage(`{"path": "notes/test.md"}`),
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	result, _ := json.Marshal(resp.Result)
	var callResult ToolCallResult
	json.Unmarshal(result, &callResult) //nolint:errcheck
	if callResult.IsError {
		t.Errorf("unexpected error result: %v", callResult.Content)
	}
}

func TestToolsCall_KBList(t *testing.T) {
	s := testServer()
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "kb_list",
		Arguments: json.RawMessage(`{}`),
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
}

func TestToolsCall_KBIngest(t *testing.T) {
	s := testServer()
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "kb_ingest",
		Arguments: json.RawMessage(`{"path": "new/doc.md", "content": "# New Doc\n\nContent here."}`),
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
}

func TestNotificationsInitialized(t *testing.T) {
	s := testServer()
	resp := doRequest(t, s, "notifications/initialized", nil)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
}

func TestUnknownMethod(t *testing.T) {
	s := testServer()
	resp := doRequest(t, s, "unknown/method", nil)

	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("error code: got %d, want -32601", resp.Error.Code)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	s := testServer()
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	// GET is allowed (returns SSE), check status.
	if w.Code != http.StatusOK {
		// Also allow 200 for SSE endpoint.
		t.Logf("GET /mcp returned %d", w.Code)
	}
}

// --- Task tool mocks ---

type mockTaskDB struct {
	tasks []*model.Task
}

func (m *mockTaskDB) Tasks() []*model.Task { return m.tasks }

func (m *mockTaskDB) Get(id string) (*model.Task, error) {
	for _, t := range m.tasks {
		if t.ID == id {
			return t, nil
		}
	}
	return nil, fmt.Errorf("not found")
}

type mockStopper struct {
	stopped []string
}

func (m *mockStopper) Stop(taskID string) error {
	m.stopped = append(m.stopped, taskID)
	return nil
}

func testServerWithTasks() (*Server, *mockTaskDB, *mockStopper) {
	s := testServer()
	taskDB := &mockTaskDB{
		tasks: []*model.Task{
			{
				ID:      "abc123",
				Name:    "fix-login",
				Status:  model.StatusInProgress,
				Project: "myapp",
				Branch:  "argus/fix-login",
				Backend: "claude",
				Prompt:  "Fix the login bug",
			},
			{
				ID:      "def456",
				Name:    "add-tests",
				Status:  model.StatusComplete,
				Project: "myapp",
				Branch:  "argus/add-tests",
			},
			{
				ID:       "ghi789",
				Name:     "old-task",
				Status:   model.StatusComplete,
				Project:  "myapp",
				Archived: true,
			},
		},
	}
	stopper := &mockStopper{}

	var createCount int
	creator := func(name, prompt, project, todoPath string) (*model.Task, error) {
		createCount++
		task := &model.Task{
			ID:      fmt.Sprintf("new-%d", createCount),
			Name:    name,
			Status:  model.StatusInProgress,
			Project: project,
			Branch:  "argus/" + name,
			Prompt:  prompt,
		}
		taskDB.tasks = append(taskDB.tasks, task)
		return task, nil
	}

	s.SetTaskManager(creator, taskDB, stopper)
	return s, taskDB, stopper
}

// --- Task tool tests ---

func TestToolsList_WithTasks(t *testing.T) {
	s, _, _ := testServerWithTasks()
	resp := doRequest(t, s, "tools/list", nil)
	testutil.NoError(t, respErr(resp))

	result, _ := json.Marshal(resp.Result)
	var list ToolsListResult
	json.Unmarshal(result, &list) //nolint:errcheck

	// 4 KB tools + 4 task tools = 8
	testutil.Equal(t, len(list.Tools), 8)

	names := make(map[string]bool)
	for _, tool := range list.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"task_create", "task_list", "task_get", "task_stop"} {
		if !names[want] {
			t.Errorf("missing tool: %s", want)
		}
	}
}

func TestToolsList_WithoutTasks(t *testing.T) {
	s := testServer() // no SetTaskManager
	resp := doRequest(t, s, "tools/list", nil)
	testutil.NoError(t, respErr(resp))

	result, _ := json.Marshal(resp.Result)
	var list ToolsListResult
	json.Unmarshal(result, &list) //nolint:errcheck

	// Only 4 KB tools
	testutil.Equal(t, len(list.Tools), 4)
}

func TestTaskCreate(t *testing.T) {
	s, _, _ := testServerWithTasks()

	t.Run("success", func(t *testing.T) {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "task_create",
			Arguments: json.RawMessage(`{"name": "new-feature", "prompt": "Add a feature", "project": "myapp"}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		if cr.IsError {
			t.Fatalf("unexpected error: %s", cr.Content[0].Text)
		}
		testutil.Contains(t, cr.Content[0].Text, "new-feature")
		testutil.Contains(t, cr.Content[0].Text, "myapp")
	})

	t.Run("auto name from prompt", func(t *testing.T) {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "task_create",
			Arguments: json.RawMessage(`{"prompt": "Fix the broken auth flow", "project": "myapp"}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		if cr.IsError {
			t.Fatalf("unexpected error: %s", cr.Content[0].Text)
		}
		testutil.Contains(t, cr.Content[0].Text, "Fix the broken auth flow")
	})

	t.Run("missing project", func(t *testing.T) {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "task_create",
			Arguments: json.RawMessage(`{"prompt": "do stuff"}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "project is required")
	})

	t.Run("missing prompt", func(t *testing.T) {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "task_create",
			Arguments: json.RawMessage(`{"project": "myapp"}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "prompt is required")
	})
}

func TestTaskList(t *testing.T) {
	s, _, _ := testServerWithTasks()

	t.Run("all", func(t *testing.T) {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "task_list",
			Arguments: json.RawMessage(`{}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		if cr.IsError {
			t.Fatalf("unexpected error: %s", cr.Content[0].Text)
		}
		testutil.Contains(t, cr.Content[0].Text, "fix-login")
		testutil.Contains(t, cr.Content[0].Text, "add-tests")
		// Archived tasks should be excluded.
		if strings.Contains(cr.Content[0].Text, "old-task") {
			t.Error("archived task should be excluded")
		}
	})

	t.Run("filter by status", func(t *testing.T) {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "task_list",
			Arguments: json.RawMessage(`{"status": "in_progress"}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		testutil.Contains(t, cr.Content[0].Text, "fix-login")
		if strings.Contains(cr.Content[0].Text, "add-tests") {
			t.Error("complete task should be filtered out")
		}
	})

	t.Run("filter by project no match", func(t *testing.T) {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "task_list",
			Arguments: json.RawMessage(`{"project": "nonexistent"}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		testutil.Contains(t, cr.Content[0].Text, "No tasks found")
	})
}

func TestTaskGet(t *testing.T) {
	s, _, _ := testServerWithTasks()

	t.Run("found", func(t *testing.T) {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "task_get",
			Arguments: json.RawMessage(`{"id": "abc123"}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		if cr.IsError {
			t.Fatalf("unexpected error: %s", cr.Content[0].Text)
		}
		testutil.Contains(t, cr.Content[0].Text, "fix-login")
		testutil.Contains(t, cr.Content[0].Text, "abc123")
		testutil.Contains(t, cr.Content[0].Text, "Fix the login bug")
	})

	t.Run("not found", func(t *testing.T) {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "task_get",
			Arguments: json.RawMessage(`{"id": "nonexistent"}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "task not found")
	})

	t.Run("missing id", func(t *testing.T) {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "task_get",
			Arguments: json.RawMessage(`{}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
	})
}

func TestTaskStop(t *testing.T) {
	s, _, stopper := testServerWithTasks()

	t.Run("stop running", func(t *testing.T) {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "task_stop",
			Arguments: json.RawMessage(`{"id": "abc123"}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		if cr.IsError {
			t.Fatalf("unexpected error: %s", cr.Content[0].Text)
		}
		testutil.Contains(t, cr.Content[0].Text, "Stop signal sent")
		testutil.DeepEqual(t, stopper.stopped, []string{"abc123"})
	})

	t.Run("missing id", func(t *testing.T) {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "task_stop",
			Arguments: json.RawMessage(`{}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "id is required")
	})
}

func TestTaskCreate_RateLimit(t *testing.T) {
	s := testServer()
	taskDB := &mockTaskDB{}
	stopper := &mockStopper{}

	// Creator that blocks until released.
	gate := make(chan struct{})
	creator := func(name, prompt, project, todoPath string) (*model.Task, error) {
		<-gate
		return &model.Task{ID: "x", Name: name, Status: model.StatusInProgress, Project: project}, nil
	}
	s.SetTaskManager(creator, taskDB, stopper)

	// Fill up the concurrent create slots.
	for i := 0; i < maxConcurrentCreates; i++ {
		go func() {
			doRequest(t, s, "tools/call", ToolCallParams{
				Name:      "task_create",
				Arguments: json.RawMessage(`{"prompt": "test", "project": "p"}`),
			})
		}()
	}

	// Wait for all slots to fill.
	time.Sleep(50 * time.Millisecond)

	// The next request should be rejected.
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "task_create",
		Arguments: json.RawMessage(`{"prompt": "overflow", "project": "p"}`),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "too many concurrent")

	// Unblock the waiting creators.
	close(gate)
}

func TestTaskTools_NotConfigured(t *testing.T) {
	s := testServer() // no SetTaskManager

	for _, tool := range []string{"task_create", "task_list", "task_get", "task_stop"} {
		t.Run(tool, func(t *testing.T) {
			resp := doRequest(t, s, "tools/call", ToolCallParams{
				Name:      tool,
				Arguments: json.RawMessage(`{"id": "x", "prompt": "y", "project": "z"}`),
			})
			testutil.NoError(t, respErr(resp))
			cr := callResult(t, resp)
			testutil.Equal(t, cr.IsError, true)
			testutil.Contains(t, cr.Content[0].Text, "not configured")
		})
	}
}

// --- test helpers ---

func respErr(resp *Response) error {
	if resp.Error != nil {
		return fmt.Errorf("rpc error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	return nil
}

func callResult(t *testing.T, resp *Response) ToolCallResult {
	t.Helper()
	raw, _ := json.Marshal(resp.Result)
	var cr ToolCallResult
	if err := json.Unmarshal(raw, &cr); err != nil {
		t.Fatalf("unmarshal ToolCallResult: %v", err)
	}
	return cr
}
