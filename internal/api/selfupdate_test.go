package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestHandleGetSourcePath_DeviceTokenForbidden(t *testing.T) {
	srv, d := testServer(t)
	handler := authMiddleware(srv.token, d, nil, srv.routes())

	plain, _, err := MintToken(d, "phone")
	testutil.NoError(t, err)

	req := httptest.NewRequest("GET", "/api/source-path", nil)
	req.Header.Set("Authorization", "Bearer "+plain)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusForbidden)
}

func TestHandleGetSourcePath_MasterReturnsConfig(t *testing.T) {
	srv, d := testServer(t)
	handler := authMiddleware(srv.token, d, nil, srv.routes())

	testutil.NoError(t, d.SetConfigValue("argus.source_path", "/path/to/argus"))

	req := authedReq("GET", "/api/source-path", "")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	var resp map[string]string
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	testutil.Equal(t, resp["path"], "/path/to/argus")
}

func TestHandleSetSourcePath_PersistsValue(t *testing.T) {
	srv, d := testServer(t)
	handler := authMiddleware(srv.token, d, nil, srv.routes())

	req := authedReq("PUT", "/api/source-path", `{"path":"/foo/bar"}`)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)
	testutil.Equal(t, d.Config().Argus.SourcePath, "/foo/bar")
}

func TestHandleSetSourcePath_TrimsWhitespace(t *testing.T) {
	srv, d := testServer(t)
	handler := authMiddleware(srv.token, d, nil, srv.routes())

	req := authedReq("PUT", "/api/source-path", `{"path":"  /foo/bar  "}`)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)
	testutil.Equal(t, d.Config().Argus.SourcePath, "/foo/bar")
}

func TestHandleUpdateSelf_NoSourcePath(t *testing.T) {
	srv, d := testServer(t)
	handler := authMiddleware(srv.token, d, nil, srv.routes())

	req := authedReq("POST", "/api/update", "")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusInternalServerError)

	var resp map[string]any
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	if !strings.Contains(resp["error"].(string), "source path") {
		t.Errorf("expected source-path error, got %v", resp["error"])
	}
	if got, _ := resp["restart"].(bool); got {
		t.Error("restart should be false on failure")
	}
}

func TestHandleUpdateSelf_DeviceTokenForbidden(t *testing.T) {
	srv, d := testServer(t)
	handler := authMiddleware(srv.token, d, nil, srv.routes())

	plain, _, err := MintToken(d, "phone-upd")
	testutil.NoError(t, err)

	req := httptest.NewRequest("POST", "/api/update", nil)
	req.Header.Set("Authorization", "Bearer "+plain)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusForbidden)
}
