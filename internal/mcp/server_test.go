package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/db"
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

func (m *mockDB) KBDelete(path string) error {
	for i, d := range m.docs {
		if d.Path == path {
			m.docs = append(m.docs[:i], m.docs[i+1:]...)
			return nil
		}
	}
	return db.ErrKBNotFound
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
	return New(db, 7742, "")
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
	testutil.Equal(t, result["protocolVersion"], "2025-06-18")

	// Should include KB instructions.
	instructions, ok := result["instructions"].(string)
	if !ok || instructions == "" {
		t.Error("initialize response missing instructions field")
	}
	testutil.Contains(t, instructions, "YAML frontmatter")
	testutil.Contains(t, instructions, "kb_search")
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
	if len(list.Tools) != 5 {
		t.Errorf("tools count: got %d, want 5", len(list.Tools))
	}

	names := make(map[string]bool)
	for _, tool := range list.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"kb_search", "kb_read", "kb_list", "kb_delete", "kb_ingest"} {
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

func TestToolsCall_KBIngest_VaultWriteBack(t *testing.T) {
	vaultDir := t.TempDir()
	db := &mockDB{}
	s := New(db, 7742, vaultDir)

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "kb_ingest",
		Arguments: json.RawMessage(`{"path": "notes/test.md", "content": "---\ntitle: Vault Test\ntags: [alpha]\n---\n\nHello vault."}`),
	})
	testutil.Nil(t, resp.Error)

	// Verify file was written to vault.
	absPath := filepath.Join(vaultDir, "notes", "test.md")
	data, err := os.ReadFile(absPath)
	testutil.NoError(t, err)

	content := string(data)
	testutil.Contains(t, content, "title: \"Vault Test\"")
	testutil.Contains(t, content, "tags: [alpha]")
	testutil.Contains(t, content, "Hello vault.")
}

func TestToolsCall_KBIngest_NoVaultPath(t *testing.T) {
	// When vaultPath is empty, no file should be written.
	db := &mockDB{}
	s := New(db, 7742, "")

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "kb_ingest",
		Arguments: json.RawMessage(`{"path": "test.md", "content": "# Test\n\nBody."}`),
	})
	testutil.Nil(t, resp.Error)

	// Document should be in DB.
	if len(db.docs) == 0 {
		t.Fatal("document not in DB")
	}
}

func TestToolsCall_KBDelete(t *testing.T) {
	s := testServer()

	t.Run("success", func(t *testing.T) {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "kb_delete",
			Arguments: json.RawMessage(`{"path": "notes/test.md"}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		if cr.IsError {
			t.Fatalf("unexpected error: %s", cr.Content[0].Text)
		}
		testutil.Contains(t, cr.Content[0].Text, "Deleted notes/test.md")
	})

	t.Run("not found", func(t *testing.T) {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "kb_delete",
			Arguments: json.RawMessage(`{"path": "nonexistent.md"}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "Delete failed")
	})

	t.Run("missing path", func(t *testing.T) {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "kb_delete",
			Arguments: json.RawMessage(`{}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "path is required")
	})

	t.Run("path traversal blocked", func(t *testing.T) {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "kb_delete",
			Arguments: json.RawMessage(`{"path": "../etc/passwd"}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "invalid path")
	})

	t.Run("nested path traversal blocked", func(t *testing.T) {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "kb_delete",
			Arguments: json.RawMessage(`{"path": "notes/../../etc/passwd"}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "invalid path")
	})

	t.Run("absolute path blocked", func(t *testing.T) {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "kb_delete",
			Arguments: json.RawMessage(`{"path": "/etc/passwd"}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "invalid path")
	})
}

func TestToolsCall_KBDelete_VaultRemoval(t *testing.T) {
	vaultDir := t.TempDir()
	db := &mockDB{
		docs: []kb.Document{
			{Path: "notes/delete-me.md", Title: "Delete Me", Body: "body", Tier: "hot"},
		},
	}
	s := New(db, 7742, vaultDir)

	// Create the vault file.
	notesDir := filepath.Join(vaultDir, "notes")
	if err := os.MkdirAll(notesDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	vaultFile := filepath.Join(notesDir, "delete-me.md")
	if err := os.WriteFile(vaultFile, []byte("# Delete Me\n\nbody"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "kb_delete",
		Arguments: json.RawMessage(`{"path": "notes/delete-me.md"}`),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("unexpected error: %s", cr.Content[0].Text)
	}

	// Verify file was removed from vault.
	if _, err := os.Stat(vaultFile); !os.IsNotExist(err) {
		t.Error("vault file should have been deleted")
	}

	// Verify document was removed from mock DB.
	if len(db.docs) != 0 {
		t.Errorf("db should be empty, got %d docs", len(db.docs))
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

// withShortKeepalive shrinks sseKeepaliveInterval for the duration of a
// test so the SSE handler emits its first comment frame within a few tens
// of milliseconds. Tests assert on actual data flow rather than waiting
// out a 30 s production interval, eliminating timing flakiness.
func withShortKeepalive(t *testing.T) {
	t.Helper()
	prev := sseKeepaliveInterval
	sseKeepaliveInterval = 25 * time.Millisecond
	t.Cleanup(func() { sseKeepaliveInterval = prev })
}

// TestGET_SSEStaysOpen verifies the GET handler holds the SSE stream open
// until the client disconnects. Closing it immediately on first response
// is what tripped Codex rmcp with "Transport channel closed". The test is
// timing-deterministic: it waits for an actual keepalive byte to confirm
// the stream is live, then cancels the request context and confirms the
// handler returns.
func TestGET_SSEStaysOpen(t *testing.T) {
	withShortKeepalive(t)

	s := testServer()
	srv := httptest.NewServer(s)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/mcp", nil)
	testutil.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	testutil.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck

	testutil.Equal(t, resp.StatusCode, http.StatusOK)
	testutil.Equal(t, resp.Header.Get("Content-Type"), "text/event-stream")

	// Block until at least one keepalive frame arrives. With a 25 ms
	// interval this completes in well under a second on any CI; if it
	// times out at 5 s the handler closed without ever streaming.
	buf := make([]byte, 64)
	readResult := make(chan readOutcome, 1)
	go func() { readResult <- readOnce(resp.Body, buf) }()

	select {
	case got := <-readResult:
		testutil.NoError(t, got.err)
		if got.n == 0 {
			t.Fatal("GET handler closed the SSE stream without streaming")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no keepalive frame arrived — handler may not be streaming")
	}

	// Cancel the client context; the server must observe it and return so
	// subsequent reads error out.
	cancel()
	go func() { readResult <- readOnce(resp.Body, buf) }()
	select {
	case got := <-readResult:
		if got.err == nil {
			t.Fatal("expected read error after client disconnect")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GET handler did not return after client disconnect")
	}
}

// TestGET_SSEUnblocksOnShutdown verifies the contract that makes
// httpSrv.Shutdown finish: Server.Shutdown cancels shutdownCtx, the GET
// handler observes it via select, and returns. Uses httptest.NewServer so
// s.httpSrv stays nil (we are testing the cancellation contract, not Go's
// own http.Server.Shutdown). No private-field mutation, no race surface.
func TestGET_SSEUnblocksOnShutdown(t *testing.T) {
	withShortKeepalive(t)

	s := testServer()
	srv := httptest.NewServer(s)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/mcp") //nolint:gosec // loopback test URL
	testutil.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck

	// Wait for a real keepalive so we know the handler is inside the
	// select loop before we call Shutdown.
	buf := make([]byte, 64)
	readResult := make(chan readOutcome, 1)
	go func() { readResult <- readOnce(resp.Body, buf) }()
	select {
	case got := <-readResult:
		testutil.NoError(t, got.err)
	case <-time.After(5 * time.Second):
		t.Fatal("no keepalive arrived — handler not in select loop")
	}

	// Subsequent read must unblock once the handler returns.
	go func() { readResult <- readOnce(resp.Body, buf) }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	select {
	case <-readResult:
	case <-time.After(2 * time.Second):
		t.Fatal("GET handler did not return after Shutdown")
	}
}

type readOutcome struct {
	n   int
	err error
}

func readOnce(r io.Reader, buf []byte) readOutcome {
	n, err := r.Read(buf)
	return readOutcome{n: n, err: err}
}

// TestPOST_NotificationReturns202 verifies pure JSON-RPC notifications (no
// "id" field) get HTTP 202 Accepted with an empty body, per the Streamable
// HTTP spec. Returning a JSON-RPC response with `"id": null` is malformed
// and rejected by strict clients like Codex rmcp.
func TestPOST_NotificationReturns202(t *testing.T) {
	s := testServer()

	// Notification body: no "id" field.
	body := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	testutil.Equal(t, w.Code, http.StatusAccepted)
	testutil.Equal(t, w.Body.Len(), 0)
}

// TestPOST_RequestReturnsJSON verifies normal request/response (with id)
// still returns a JSON-RPC response. Guards against the notification-202
// path swallowing real requests.
func TestPOST_RequestReturnsJSON(t *testing.T) {
	s := testServer()

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	testutil.Equal(t, w.Code, http.StatusOK)
	testutil.Equal(t, w.Header().Get("Content-Type"), "application/json")

	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	testutil.Equal(t, resp.JSONRPC, "2.0")
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
}

// --- Task tool mocks ---

type mockTaskDB struct {
	tasks []*model.Task
}

func (m *mockTaskDB) Tasks() ([]*model.Task, error) { return m.tasks, nil }

func (m *mockTaskDB) Get(id string) (*model.Task, error) {
	for _, t := range m.tasks {
		if t.ID == id {
			return t, nil
		}
	}
	return nil, fmt.Errorf("not found")
}

func (m *mockTaskDB) Update(t *model.Task) error {
	for i, existing := range m.tasks {
		if existing.ID == t.ID {
			m.tasks[i] = t
			return nil
		}
	}
	return fmt.Errorf("not found")
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
				ID:       "abc123",
				Name:     "fix-login",
				Status:   model.StatusInProgress,
				Project:  "myapp",
				Branch:   "argus/fix-login",
				Backend:  "claude",
				Prompt:   "Fix the login bug",
				Worktree: "/tmp/worktrees/myapp/fix-login",
			},
			{
				ID:       "def456",
				Name:     "add-tests",
				Status:   model.StatusComplete,
				Project:  "myapp",
				Branch:   "argus/add-tests",
				Worktree: "/tmp/worktrees/myapp/add-tests",
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
	creator := func(name, prompt, project string, _ bool) (*model.Task, error) {
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

	// 5 KB tools + 6 task tools = 11
	testutil.Equal(t, len(list.Tools), 11)

	names := make(map[string]bool)
	for _, tool := range list.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"task_create", "task_list", "task_get", "task_stop", "task_archive", "task_complete"} {
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

	// Only 5 KB tools
	testutil.Equal(t, len(list.Tools), 5)
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
	creator := func(name, prompt, project string, _ bool) (*model.Task, error) {
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

	for _, tool := range []string{"task_create", "task_list", "task_get", "task_stop", "task_archive", "task_complete"} {
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

func TestTaskArchive(t *testing.T) {
	t.Run("by id toggle archive", func(t *testing.T) {
		s, taskDB, _ := testServerWithTasks()
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "task_archive",
			Arguments: json.RawMessage(`{"id": "abc123"}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		if cr.IsError {
			t.Fatalf("unexpected error: %s", cr.Content[0].Text)
		}
		testutil.Contains(t, cr.Content[0].Text, "Archived")
		got, _ := taskDB.Get("abc123")
		testutil.Equal(t, got.Archived, true)
	})

	t.Run("by id explicit unarchive", func(t *testing.T) {
		s, taskDB, _ := testServerWithTasks()
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "task_archive",
			Arguments: json.RawMessage(`{"id": "ghi789", "archived": false}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		if cr.IsError {
			t.Fatalf("unexpected error: %s", cr.Content[0].Text)
		}
		testutil.Contains(t, cr.Content[0].Text, "Unarchived")
		got, _ := taskDB.Get("ghi789")
		testutil.Equal(t, got.Archived, false)
	})

	t.Run("by cwd exact worktree match", func(t *testing.T) {
		s, taskDB, _ := testServerWithTasks()
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "task_archive",
			Arguments: json.RawMessage(`{"cwd": "/tmp/worktrees/myapp/fix-login"}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		if cr.IsError {
			t.Fatalf("unexpected error: %s", cr.Content[0].Text)
		}
		got, _ := taskDB.Get("abc123")
		testutil.Equal(t, got.Archived, true)
	})

	t.Run("by cwd nested subdirectory", func(t *testing.T) {
		s, taskDB, _ := testServerWithTasks()
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "task_archive",
			Arguments: json.RawMessage(`{"cwd": "/tmp/worktrees/myapp/fix-login/internal/foo"}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		if cr.IsError {
			t.Fatalf("unexpected error: %s", cr.Content[0].Text)
		}
		got, _ := taskDB.Get("abc123")
		testutil.Equal(t, got.Archived, true)
	})

	t.Run("cwd does not match sibling worktree with shared prefix", func(t *testing.T) {
		s, taskDB, _ := testServerWithTasks()
		// "/tmp/worktrees/myapp/add-tests" must not match a cwd under
		// "/tmp/worktrees/myapp/add-tests-extra".
		taskDB.tasks = append(taskDB.tasks, &model.Task{
			ID: "zzz", Name: "sibling", Worktree: "/tmp/worktrees/myapp/add-tests-extra",
		})
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "task_archive",
			Arguments: json.RawMessage(`{"cwd": "/tmp/worktrees/myapp/add-tests-extra/x"}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		if cr.IsError {
			t.Fatalf("unexpected error: %s", cr.Content[0].Text)
		}
		// add-tests should still be unarchived — only the sibling flipped.
		addTests, _ := taskDB.Get("def456")
		testutil.Equal(t, addTests.Archived, false)
		sibling, _ := taskDB.Get("zzz")
		testutil.Equal(t, sibling.Archived, true)
	})

	t.Run("archiving clears waiting_review", func(t *testing.T) {
		s, taskDB, _ := testServerWithTasks()
		// Set WaitingReview via Update so the test does not depend on
		// mockTaskDB.Get returning the slice's pointer.
		seed, _ := taskDB.Get("abc123")
		seed.WaitingReview = true
		if err := taskDB.Update(seed); err != nil {
			t.Fatalf("seed Update: %v", err)
		}
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "task_archive",
			Arguments: json.RawMessage(`{"id": "abc123", "archived": true}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		if cr.IsError {
			t.Fatalf("unexpected error: %s", cr.Content[0].Text)
		}
		got, _ := taskDB.Get("abc123")
		testutil.Equal(t, got.Archived, true)
		testutil.Equal(t, got.WaitingReview, false)
	})

	t.Run("no-op when already in desired state", func(t *testing.T) {
		s, taskDB, _ := testServerWithTasks()
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "task_archive",
			Arguments: json.RawMessage(`{"id": "ghi789", "archived": true}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		if cr.IsError {
			t.Fatalf("unexpected error: %s", cr.Content[0].Text)
		}
		testutil.Contains(t, cr.Content[0].Text, "already")
		got, _ := taskDB.Get("ghi789")
		testutil.Equal(t, got.Archived, true)
	})

	t.Run("missing id and cwd", func(t *testing.T) {
		s, _, _ := testServerWithTasks()
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "task_archive",
			Arguments: json.RawMessage(`{}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "provide id or cwd")
	})

	t.Run("unknown id", func(t *testing.T) {
		s, _, _ := testServerWithTasks()
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "task_archive",
			Arguments: json.RawMessage(`{"id": "nope"}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "not found")
	})

	t.Run("cwd with no matching worktree", func(t *testing.T) {
		s, _, _ := testServerWithTasks()
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "task_archive",
			Arguments: json.RawMessage(`{"cwd": "/nowhere/special"}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "no task matches cwd")
	})
}

func TestTaskComplete(t *testing.T) {
	t.Run("by id", func(t *testing.T) {
		s, taskDB, _ := testServerWithTasks()
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "task_complete",
			Arguments: json.RawMessage(`{"id": "abc123"}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		if cr.IsError {
			t.Fatalf("unexpected error: %s", cr.Content[0].Text)
		}
		testutil.Contains(t, cr.Content[0].Text, "Marked task")
		got, _ := taskDB.Get("abc123")
		testutil.Equal(t, got.Status, model.StatusComplete)
		if got.EndedAt.IsZero() {
			t.Error("EndedAt should be set when transitioning to complete")
		}
	})

	t.Run("by cwd", func(t *testing.T) {
		s, taskDB, _ := testServerWithTasks()
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "task_complete",
			Arguments: json.RawMessage(`{"cwd": "/tmp/worktrees/myapp/fix-login/internal/foo"}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		if cr.IsError {
			t.Fatalf("unexpected error: %s", cr.Content[0].Text)
		}
		got, _ := taskDB.Get("abc123")
		testutil.Equal(t, got.Status, model.StatusComplete)
	})

	t.Run("already complete is no-op", func(t *testing.T) {
		s, taskDB, _ := testServerWithTasks()
		// def456 is seeded as StatusComplete; capture EndedAt to ensure it
		// is not re-stamped on the no-op path.
		seed, _ := taskDB.Get("def456")
		seed.EndedAt = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
		_ = taskDB.Update(seed)

		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "task_complete",
			Arguments: json.RawMessage(`{"id": "def456"}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		if cr.IsError {
			t.Fatalf("unexpected error: %s", cr.Content[0].Text)
		}
		testutil.Contains(t, cr.Content[0].Text, "already complete")
		got, _ := taskDB.Get("def456")
		testutil.Equal(t, got.EndedAt.Year(), 2020)
	})

	t.Run("completing clears waiting_review", func(t *testing.T) {
		s, taskDB, _ := testServerWithTasks()
		seed, _ := taskDB.Get("abc123")
		seed.WaitingReview = true
		if err := taskDB.Update(seed); err != nil {
			t.Fatalf("seed Update: %v", err)
		}
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "task_complete",
			Arguments: json.RawMessage(`{"id": "abc123"}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		if cr.IsError {
			t.Fatalf("unexpected error: %s", cr.Content[0].Text)
		}
		got, _ := taskDB.Get("abc123")
		testutil.Equal(t, got.Status, model.StatusComplete)
		testutil.Equal(t, got.WaitingReview, false)
	})

	t.Run("missing id and cwd", func(t *testing.T) {
		s, _, _ := testServerWithTasks()
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "task_complete",
			Arguments: json.RawMessage(`{}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "provide id or cwd")
	})

	t.Run("unknown id", func(t *testing.T) {
		s, _, _ := testServerWithTasks()
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "task_complete",
			Arguments: json.RawMessage(`{"id": "nope"}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "not found")
	})
}

// --- Clipboard tool tests ---

type mockClipboard struct {
	last map[string]string
	err  error
}

func newMockClipboard() *mockClipboard {
	return &mockClipboard{last: make(map[string]string)}
}

func (m *mockClipboard) Set(taskID, text string) error {
	if m.err != nil {
		return m.err
	}
	m.last[taskID] = text
	return nil
}

func TestToolsList_WithClipboard(t *testing.T) {
	s, _, _ := testServerWithTasks()
	s.SetClipboard(newMockClipboard())

	resp := doRequest(t, s, "tools/list", nil)
	testutil.NoError(t, respErr(resp))

	result, _ := json.Marshal(resp.Result)
	var list ToolsListResult
	json.Unmarshal(result, &list) //nolint:errcheck

	// 5 KB tools + 6 task tools + 1 clipboard tool = 12
	testutil.Equal(t, len(list.Tools), 12)

	names := make(map[string]bool)
	for _, tool := range list.Tools {
		names[tool.Name] = true
	}
	if !names["argus_clipboard_set"] {
		t.Errorf("missing argus_clipboard_set in tools list")
	}
}

func TestToolsList_ClipboardWithoutTasksHidden(t *testing.T) {
	// Without SetTaskManager, the clipboard tool should NOT appear because
	// cwd-resolution requires task management.
	s := testServer()
	s.SetClipboard(newMockClipboard())

	resp := doRequest(t, s, "tools/list", nil)
	testutil.NoError(t, respErr(resp))

	result, _ := json.Marshal(resp.Result)
	var list ToolsListResult
	json.Unmarshal(result, &list) //nolint:errcheck

	for _, tool := range list.Tools {
		if tool.Name == "argus_clipboard_set" {
			t.Errorf("argus_clipboard_set should not appear without task management")
		}
	}
}

func TestClipboardSet_ByID(t *testing.T) {
	s, _, _ := testServerWithTasks()
	clip := newMockClipboard()
	s.SetClipboard(clip)

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "argus_clipboard_set",
		Arguments: json.RawMessage(`{"id":"abc123","text":"hello world"}`),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, false)

	testutil.Equal(t, clip.last["abc123"], "hello world")
}

func TestClipboardSet_ByCwd(t *testing.T) {
	s, _, _ := testServerWithTasks()
	clip := newMockClipboard()
	s.SetClipboard(clip)

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "argus_clipboard_set",
		Arguments: json.RawMessage(`{"cwd":"/tmp/worktrees/myapp/fix-login","text":"snippet"}`),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, false)

	testutil.Equal(t, clip.last["abc123"], "snippet")
}

func TestClipboardSet_MissingTextRejected(t *testing.T) {
	s, _, _ := testServerWithTasks()
	s.SetClipboard(newMockClipboard())

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "argus_clipboard_set",
		Arguments: json.RawMessage(`{"id":"abc123"}`),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "text is required")
}

func TestClipboardSet_NoTaskMatch(t *testing.T) {
	s, _, _ := testServerWithTasks()
	s.SetClipboard(newMockClipboard())

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "argus_clipboard_set",
		Arguments: json.RawMessage(`{"cwd":"/nowhere","text":"x"}`),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "no task matches cwd")
}

func TestClipboardSet_StoreError(t *testing.T) {
	s, _, _ := testServerWithTasks()
	clip := newMockClipboard()
	clip.err = fmt.Errorf("too large")
	s.SetClipboard(clip)

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "argus_clipboard_set",
		Arguments: json.RawMessage(`{"id":"abc123","text":"x"}`),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "too large")
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

// --- Schedule tool mocks ---

type mockScheduleStore struct {
	schedules []*model.ScheduledTask
	nextID    int
}

func (m *mockScheduleStore) Schedules() ([]*model.ScheduledTask, error) {
	out := make([]*model.ScheduledTask, len(m.schedules))
	copy(out, m.schedules)
	return out, nil
}

func (m *mockScheduleStore) GetSchedule(id string) (*model.ScheduledTask, error) {
	for _, s := range m.schedules {
		if s.ID == id {
			return s, nil
		}
	}
	return nil, db.ErrScheduleNotFound
}

func (m *mockScheduleStore) AddSchedule(s *model.ScheduledTask) error {
	if s.ID == "" {
		m.nextID++
		s.ID = fmt.Sprintf("sched-%d", m.nextID)
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now()
	}
	m.schedules = append(m.schedules, s)
	return nil
}

func (m *mockScheduleStore) UpdateSchedule(s *model.ScheduledTask) error {
	for i, existing := range m.schedules {
		if existing.ID == s.ID {
			m.schedules[i] = s
			return nil
		}
	}
	return db.ErrScheduleNotFound
}

func (m *mockScheduleStore) DeleteSchedule(id string) error {
	for i, s := range m.schedules {
		if s.ID == id {
			m.schedules = append(m.schedules[:i], m.schedules[i+1:]...)
			return nil
		}
	}
	return db.ErrScheduleNotFound
}

type mockScheduleRunner struct {
	store *mockScheduleStore
	fired []string
	err   error
}

func (m *mockScheduleRunner) RunNow(id string) (*model.Task, error) {
	if m.err != nil {
		return nil, m.err
	}
	sch, err := m.store.GetSchedule(id)
	if err != nil {
		return nil, err
	}
	m.fired = append(m.fired, id)
	return &model.Task{ID: "task-from-" + id, Name: sch.Name, Project: sch.Project}, nil
}

func testServerWithSchedules() (*Server, *mockScheduleStore, *mockScheduleRunner) {
	s := testServer()
	store := &mockScheduleStore{
		schedules: []*model.ScheduledTask{
			{
				ID:         "existing-1",
				Name:       "daily-report",
				Project:    "myapp",
				Prompt:     "Summarize yesterday's commits",
				Schedule:   "@daily",
				Enabled:    true,
				CreatedAt:  time.Now(),
				NextRunAt:  time.Now().Add(time.Hour),
				LastRunAt:  time.Now().Add(-23 * time.Hour),
				LastTaskID: "prev-task",
			},
			{
				ID:        "existing-2",
				Name:      "hourly-poll",
				Project:   "myapp",
				Prompt:    "Check the queue",
				Schedule:  "@every 1h",
				Enabled:   false,
				CreatedAt: time.Now(),
				LastError: "project missing on previous fire",
			},
		},
	}
	runner := &mockScheduleRunner{store: store}
	s.SetScheduleManager(store, runner)
	return s, store, runner
}

// --- Schedule tool tests ---

func TestToolsList_WithSchedules(t *testing.T) {
	s, _, _ := testServerWithSchedules()
	resp := doRequest(t, s, "tools/list", nil)
	testutil.NoError(t, respErr(resp))

	result, _ := json.Marshal(resp.Result)
	var list ToolsListResult
	json.Unmarshal(result, &list) //nolint:errcheck

	// 5 KB tools + 5 schedule tools (no task manager wired here) = 10
	testutil.Equal(t, len(list.Tools), 10)
	names := make(map[string]bool)
	for _, tool := range list.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"schedule_list", "schedule_create", "schedule_update", "schedule_delete", "schedule_run_now"} {
		if !names[want] {
			t.Errorf("missing tool: %s", want)
		}
	}
}

func TestScheduleList(t *testing.T) {
	s, _, _ := testServerWithSchedules()

	t.Run("populated", func(t *testing.T) {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "schedule_list",
			Arguments: json.RawMessage(`{}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		if cr.IsError {
			t.Fatalf("unexpected error: %s", cr.Content[0].Text)
		}
		text := cr.Content[0].Text
		testutil.Contains(t, text, "daily-report")
		testutil.Contains(t, text, "hourly-poll")
		testutil.Contains(t, text, "@daily")
		testutil.Contains(t, text, "[disabled]")
		testutil.Contains(t, text, "project missing on previous fire")
	})

	t.Run("empty", func(t *testing.T) {
		emptyServer := testServer()
		emptyServer.SetScheduleManager(&mockScheduleStore{}, &mockScheduleRunner{})
		resp := doRequest(t, emptyServer, "tools/call", ToolCallParams{
			Name:      "schedule_list",
			Arguments: json.RawMessage(`{}`),
		})
		cr := callResult(t, resp)
		testutil.Contains(t, cr.Content[0].Text, "No scheduled tasks")
	})

	t.Run("not configured", func(t *testing.T) {
		bare := testServer()
		resp := doRequest(t, bare, "tools/call", ToolCallParams{
			Name:      "schedule_list",
			Arguments: json.RawMessage(`{}`),
		})
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "schedule management not configured")
	})
}

func TestScheduleCreate(t *testing.T) {
	s, store, _ := testServerWithSchedules()

	t.Run("success", func(t *testing.T) {
		before := len(store.schedules)
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "schedule_create",
			Arguments: json.RawMessage(`{"name":"weekly-cleanup","project":"myapp","prompt":"Run cleanup script","schedule":"@weekly"}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		if cr.IsError {
			t.Fatalf("unexpected error: %s", cr.Content[0].Text)
		}
		testutil.Equal(t, len(store.schedules), before+1)
		created := store.schedules[len(store.schedules)-1]
		testutil.Equal(t, created.Name, "weekly-cleanup")
		testutil.Equal(t, created.Schedule, "@weekly")
		testutil.Equal(t, created.Enabled, true)
		if created.NextRunAt.IsZero() {
			t.Error("expected NextRunAt to be precomputed")
		}
		testutil.Contains(t, cr.Content[0].Text, "weekly-cleanup")
	})

	t.Run("missing required field", func(t *testing.T) {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "schedule_create",
			Arguments: json.RawMessage(`{"name":"x","project":"myapp","schedule":"@daily"}`),
		})
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "prompt is required")
	})

	t.Run("invalid cron expression", func(t *testing.T) {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "schedule_create",
			Arguments: json.RawMessage(`{"name":"x","project":"myapp","prompt":"do thing","schedule":"banana"}`),
		})
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
	})

	t.Run("explicit enabled false", func(t *testing.T) {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "schedule_create",
			Arguments: json.RawMessage(`{"name":"paused","project":"myapp","prompt":"x","schedule":"@daily","enabled":false}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		if cr.IsError {
			t.Fatalf("unexpected error: %s", cr.Content[0].Text)
		}
		var found *model.ScheduledTask
		for _, sch := range store.schedules {
			if sch.Name == "paused" {
				found = sch
				break
			}
		}
		if found == nil {
			t.Fatal("paused schedule not found")
		}
		testutil.Equal(t, found.Enabled, false)
	})

	t.Run("one-shot run_once_at", func(t *testing.T) {
		future := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
		body := fmt.Sprintf(`{"name":"once","project":"myapp","prompt":"go","run_once_at":%q}`, future)
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "schedule_create",
			Arguments: json.RawMessage(body),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		if cr.IsError {
			t.Fatalf("unexpected error: %s", cr.Content[0].Text)
		}
		var found *model.ScheduledTask
		for _, sch := range store.schedules {
			if sch.Name == "once" {
				found = sch
				break
			}
		}
		if found == nil {
			t.Fatal("one-shot schedule not found")
		}
		if !found.IsOneShot() {
			t.Error("expected IsOneShot=true")
		}
		testutil.Equal(t, found.Schedule, "")
		if found.RunOnceAt.IsZero() {
			t.Error("expected RunOnceAt populated")
		}
		testutil.Contains(t, cr.Content[0].Text, "once @")
	})

	t.Run("run_once_at in the past rejected", func(t *testing.T) {
		past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
		body := fmt.Sprintf(`{"name":"old","project":"myapp","prompt":"go","run_once_at":%q}`, past)
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "schedule_create",
			Arguments: json.RawMessage(body),
		})
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "future")
	})

	t.Run("run_once_at malformed rejected", func(t *testing.T) {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "schedule_create",
			Arguments: json.RawMessage(`{"name":"bad","project":"myapp","prompt":"go","run_once_at":"tomorrow"}`),
		})
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "RFC3339")
	})

	t.Run("both schedule and run_once_at rejected", func(t *testing.T) {
		future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		body := fmt.Sprintf(`{"name":"both","project":"myapp","prompt":"go","schedule":"@daily","run_once_at":%q}`, future)
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "schedule_create",
			Arguments: json.RawMessage(body),
		})
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "either")
	})

	t.Run("missing both cadences rejected", func(t *testing.T) {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "schedule_create",
			Arguments: json.RawMessage(`{"name":"x","project":"myapp","prompt":"go"}`),
		})
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "required")
	})
}

func TestScheduleUpdate(t *testing.T) {
	s, store, _ := testServerWithSchedules()

	t.Run("toggle enabled", func(t *testing.T) {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "schedule_update",
			Arguments: json.RawMessage(`{"id":"existing-2","enabled":true}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		if cr.IsError {
			t.Fatalf("unexpected error: %s", cr.Content[0].Text)
		}
		got, _ := store.GetSchedule("existing-2")
		testutil.Equal(t, got.Enabled, true)
		// LastError must be cleared on successful update.
		testutil.Equal(t, got.LastError, "")
	})

	t.Run("change schedule recomputes next_run_at", func(t *testing.T) {
		// Capture pre-update NextRunAt to confirm it changes when the cron
		// expression changes.
		original, _ := store.GetSchedule("existing-1")
		oldNext := original.NextRunAt
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "schedule_update",
			Arguments: json.RawMessage(`{"id":"existing-1","schedule":"@every 30m"}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		if cr.IsError {
			t.Fatalf("unexpected error: %s", cr.Content[0].Text)
		}
		got, _ := store.GetSchedule("existing-1")
		testutil.Equal(t, got.Schedule, "@every 30m")
		if got.NextRunAt.Equal(oldNext) {
			t.Error("expected NextRunAt to be recomputed when schedule changed")
		}
	})

	t.Run("missing id", func(t *testing.T) {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "schedule_update",
			Arguments: json.RawMessage(`{"enabled":true}`),
		})
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "id is required")
	})

	t.Run("not found", func(t *testing.T) {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "schedule_update",
			Arguments: json.RawMessage(`{"id":"does-not-exist","enabled":true}`),
		})
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "schedule not found")
	})

	t.Run("convert recurring to one-shot clears schedule", func(t *testing.T) {
		future := time.Now().Add(3 * time.Hour).UTC().Format(time.RFC3339)
		body := fmt.Sprintf(`{"id":"existing-1","run_once_at":%q}`, future)
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "schedule_update",
			Arguments: json.RawMessage(body),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		if cr.IsError {
			t.Fatalf("unexpected error: %s", cr.Content[0].Text)
		}
		got, _ := store.GetSchedule("existing-1")
		testutil.Equal(t, got.Schedule, "")
		if !got.IsOneShot() {
			t.Error("expected IsOneShot=true after conversion")
		}
	})

	t.Run("convert one-shot to recurring clears run_once_at", func(t *testing.T) {
		// Seed a one-shot row to convert.
		future := time.Now().Add(time.Hour)
		store.schedules = append(store.schedules, &model.ScheduledTask{
			ID:        "to-convert",
			Name:      "to-convert",
			Project:   "myapp",
			Prompt:    "x",
			RunOnceAt: future,
			Enabled:   true,
		})
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "schedule_update",
			Arguments: json.RawMessage(`{"id":"to-convert","schedule":"@daily"}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		if cr.IsError {
			t.Fatalf("unexpected error: %s", cr.Content[0].Text)
		}
		got, _ := store.GetSchedule("to-convert")
		testutil.Equal(t, got.Schedule, "@daily")
		if got.IsOneShot() {
			t.Error("expected one-shot anchor cleared after conversion")
		}
	})

	t.Run("run_once_at past rejected on update", func(t *testing.T) {
		past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
		body := fmt.Sprintf(`{"id":"existing-1","run_once_at":%q}`, past)
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "schedule_update",
			Arguments: json.RawMessage(body),
		})
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "future")
	})
}

func TestScheduleDelete(t *testing.T) {
	s, store, _ := testServerWithSchedules()

	t.Run("success", func(t *testing.T) {
		before := len(store.schedules)
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "schedule_delete",
			Arguments: json.RawMessage(`{"id":"existing-2"}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		if cr.IsError {
			t.Fatalf("unexpected error: %s", cr.Content[0].Text)
		}
		testutil.Equal(t, len(store.schedules), before-1)
	})

	t.Run("missing id", func(t *testing.T) {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "schedule_delete",
			Arguments: json.RawMessage(`{}`),
		})
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
	})
}

func TestScheduleRunNow(t *testing.T) {
	s, _, runner := testServerWithSchedules()

	t.Run("success", func(t *testing.T) {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "schedule_run_now",
			Arguments: json.RawMessage(`{"id":"existing-1"}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		if cr.IsError {
			t.Fatalf("unexpected error: %s", cr.Content[0].Text)
		}
		testutil.Equal(t, len(runner.fired), 1)
		testutil.Equal(t, runner.fired[0], "existing-1")
		testutil.Contains(t, cr.Content[0].Text, "task-from-existing-1")
	})

	t.Run("not found", func(t *testing.T) {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "schedule_run_now",
			Arguments: json.RawMessage(`{"id":"missing"}`),
		})
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "Failed to run schedule")
	})
}
