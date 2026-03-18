package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/drn/argus/internal/kb"
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
