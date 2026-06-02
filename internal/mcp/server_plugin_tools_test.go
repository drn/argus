package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
)

func newMCPWithRegistry(t *testing.T) (*Server, *Registry, *db.DB) {
	t.Helper()
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	reg := NewRegistry(d)
	s := testServer()
	s.SetPluginRegistry(reg)
	return s, reg, d
}

func TestToolsList_IncludesPluginTools(t *testing.T) {
	s, reg, _ := newMCPWithRegistry(t)
	testutil.NoError(t, reg.Register("ludwig", ToolRegistration{
		Name:        "ludwig_decision_add",
		Description: "Record a decision.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
		CallbackURL: "http://127.0.0.1/cb",
	}))

	resp := doRequest(t, s, "tools/list", nil)
	testutil.NoError(t, respErr(resp))

	result, _ := json.Marshal(resp.Result)
	var list ToolsListResult
	testutil.NoError(t, json.Unmarshal(result, &list))

	names := make(map[string]Tool)
	for _, tool := range list.Tools {
		names[tool.Name] = tool
	}
	plugin, ok := names["ludwig_decision_add"]
	if !ok {
		t.Fatal("plugin tool missing from tools/list response")
	}
	testutil.Equal(t, plugin.Description, "Record a decision.")

	// Built-in KB tools must still be present so the registry is additive.
	if _, ok := names["kb_search"]; !ok {
		t.Fatal("built-in kb_search dropped after plugin registration")
	}
}

func TestToolsCall_ProxiesToPluginCallback(t *testing.T) {
	s, reg, _ := newMCPWithRegistry(t)

	var capturedAuth string
	var capturedBody []byte
	plugin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"hello from plugin"}],"isError":false}`))
	}))
	defer plugin.Close()

	testutil.NoError(t, reg.Register("ludwig", ToolRegistration{
		Name:        "ludwig_decision_add",
		InputSchema: json.RawMessage(`{}`),
		CallbackURL: plugin.URL,
		AuthHeader:  "Bearer plugin-secret",
	}))

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "ludwig_decision_add",
		Arguments: json.RawMessage(`{"text":"agreed"}`),
	})
	testutil.NoError(t, respErr(resp))

	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("unexpected error: %s", cr.Content[0].Text)
	}
	testutil.Equal(t, cr.Content[0].Text, "hello from plugin")
	testutil.Equal(t, capturedAuth, "Bearer plugin-secret")
	testutil.Contains(t, string(capturedBody), `"tool":"ludwig_decision_add"`)
	testutil.Contains(t, string(capturedBody), `"input":{"text":"agreed"}`)
	// Built-in tool dispatch should still work after a plugin proxy round-trip.
	resp2 := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "kb_list",
		Arguments: json.RawMessage(`{}`),
	})
	testutil.NoError(t, respErr(resp2))
}

func TestToolsCall_PluginUnknownNameWithRegistryWired(t *testing.T) {
	s, _, _ := newMCPWithRegistry(t)
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "ludwig_unregistered",
		Arguments: json.RawMessage(`{}`),
	})
	if resp.Error == nil || resp.Error.Code != -32601 {
		t.Fatalf("expected -32601 unknown tool error, got %+v", resp.Error)
	}
}

func TestToolsCall_PluginCallbackHTTPError(t *testing.T) {
	s, reg, _ := newMCPWithRegistry(t)
	plugin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusBadGateway)
	}))
	defer plugin.Close()

	testutil.NoError(t, reg.Register("ludwig", ToolRegistration{
		Name: "ludwig_boom", InputSchema: json.RawMessage(`{}`), CallbackURL: plugin.URL,
	}))

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "ludwig_boom",
		Arguments: json.RawMessage(`{}`),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "plugin returned 502")
}

func TestToolsList_NoRegistryWired(t *testing.T) {
	// With no plugin registry set, tools/list MUST behave exactly as the
	// default-UX contract requires: same length, same names as today.
	s := testServer()
	resp := doRequest(t, s, "tools/list", nil)
	testutil.NoError(t, respErr(resp))

	result, _ := json.Marshal(resp.Result)
	var list ToolsListResult
	testutil.NoError(t, json.Unmarshal(result, &list))
	testutil.Equal(t, len(list.Tools), 5) // only KB built-ins
}
