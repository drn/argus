package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func callAPIGet(t *testing.T, s *Server, args string) ToolCallResult {
	t.Helper()
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "argus_api_get",
		Arguments: json.RawMessage(args),
	})
	testutil.NoError(t, respErr(resp))
	return callResult(t, resp)
}

func toolNames(t *testing.T, s *Server) map[string]bool {
	t.Helper()
	resp := doRequest(t, s, "tools/list", nil)
	testutil.NoError(t, respErr(resp))
	raw, _ := json.Marshal(resp.Result)
	var list ToolsListResult
	testutil.NoError(t, json.Unmarshal(raw, &list))
	names := map[string]bool{}
	for _, tool := range list.Tools {
		names[tool.Name] = true
	}
	return names
}

func TestAPIGet_ListedOnlyWhenWired(t *testing.T) {
	s := testServer()
	testutil.Equal(t, toolNames(t, s)["argus_api_get"], false)

	s.SetAPIGetter(func(context.Context, string) (APIGetResult, error) { return APIGetResult{}, nil })
	testutil.Equal(t, toolNames(t, s)["argus_api_get"], true)
}

func TestAPIGet_UnwiredCallIsError(t *testing.T) {
	cr := callAPIGet(t, testServer(), `{"path":"/api/tasks"}`)
	testutil.Equal(t, cr.IsError, true)
}

func TestAPIGet_Call(t *testing.T) {
	var gotPath string
	s := testServer()
	s.SetAPIGetter(func(_ context.Context, p string) (APIGetResult, error) {
		gotPath = p
		switch p {
		case "/api/tasks":
			return APIGetResult{Status: 200, Body: []byte(`[{"id":"1"}]`)}, nil
		case "/api/big":
			return APIGetResult{Status: 200, Body: []byte("xx"), Truncated: true}, nil
		case "/api/missing":
			return APIGetResult{Status: 404, Body: []byte(`{"error":"nope"}`)}, nil
		}
		return APIGetResult{}, errors.New("path must not contain \"..\"")
	})

	t.Run("2xx returns body", func(t *testing.T) {
		cr := callAPIGet(t, s, `{"path":"/api/tasks"}`)
		testutil.Equal(t, cr.IsError, false)
		testutil.Equal(t, cr.Content[0].Text, `[{"id":"1"}]`)
		testutil.Equal(t, gotPath, "/api/tasks")
	})

	t.Run("truncation marker appended", func(t *testing.T) {
		cr := callAPIGet(t, s, `{"path":"/api/big"}`)
		testutil.Equal(t, cr.IsError, false)
		testutil.Contains(t, cr.Content[0].Text, "[truncated")
	})

	t.Run("non-2xx is a tool error with status", func(t *testing.T) {
		cr := callAPIGet(t, s, `{"path":"/api/missing"}`)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "HTTP 404")
	})

	t.Run("validation error surfaces", func(t *testing.T) {
		cr := callAPIGet(t, s, `{"path":"/api/../x"}`)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "..")
	})

	t.Run("missing path", func(t *testing.T) {
		for _, args := range []string{`{}`, `{"path":"  "}`} {
			cr := callAPIGet(t, s, args)
			testutil.Equal(t, cr.IsError, true)
		}
	})
}

func TestAPIGet_DescriptionListsRoutes(t *testing.T) {
	desc := apiGetToolDefs[0].Description
	for _, r := range []string{"/api/tasks", "/bridge", "/bridge/ui", "/api/bridge/shadow"} {
		testutil.Contains(t, desc, r)
	}
}
