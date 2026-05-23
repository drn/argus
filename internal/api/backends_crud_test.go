package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
)

// TestHandleCreateBackend pins the new POST /api/backends route — without it
// the TUI's settings tab cannot add a backend over the remote-TUI path.
func TestHandleCreateBackend(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()

	t.Run("rejects non-master", func(t *testing.T) {
		req := authedReq("POST", "/api/backends", `{"name":"foo","command":"cmd"}`)
		// authedReq does NOT stamp X-Argus-Auth, so requireMaster denies.
		req.Header.Set("X-Argus-Auth", "device")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusForbidden)
	})

	t.Run("creates a backend", func(t *testing.T) {
		req := masterReq("POST", "/api/backends", `{"name":"foo","command":"cmd","prompt_flag":"-p"}`)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusCreated)

		got, _ := d.Backends()
		b, ok := got["foo"]
		testutil.True(t, ok)
		testutil.Equal(t, b.Command, "cmd")
		testutil.Equal(t, b.PromptFlag, "-p")
	})

	t.Run("rejects empty name", func(t *testing.T) {
		req := masterReq("POST", "/api/backends", `{"name":"","command":"x"}`)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusBadRequest)
	})
}

func TestHandleUpdateBackend(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()
	testutil.NoError(t, d.SetBackend("existing", config.Backend{Command: "old", PromptFlag: "-x"}))

	req := masterReq("PUT", "/api/backends/existing", `{"command":"new","prompt_flag":"-p"}`)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	got, _ := d.Backends()
	testutil.Equal(t, got["existing"].Command, "new")
}

func TestHandleDeleteBackend(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()
	testutil.NoError(t, d.SetBackend("doomed", config.Backend{Command: "x"}))

	req := masterReq("DELETE", "/api/backends/doomed", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	got, _ := d.Backends()
	_, exists := got["doomed"]
	testutil.False(t, exists)
}

func TestHandleGetConfig(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	req := masterReq("GET", "/api/config", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	var cfg map[string]any
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &cfg))
	// Sanity: defaults, backends, projects all show up in the snapshot.
	if _, ok := cfg["Defaults"]; !ok {
		t.Fatalf("expected Defaults in config response, got keys: %v", mapKeys(cfg))
	}
	if _, ok := cfg["Backends"]; !ok {
		t.Fatalf("expected Backends in config response, got keys: %v", mapKeys(cfg))
	}
}

func TestHandleSessionState(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	req := authedReq("GET", "/api/sessions/state", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	var resp struct {
		Running []string `json:"running"`
		Idle    []string `json:"idle"`
	}
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	// No sessions started in the test, so both lists should be empty (but
	// not nil — the runner returns empty slices, not nil).
	testutil.Equal(t, len(resp.Running), 0)
	testutil.Equal(t, len(resp.Idle), 0)
}

func TestHandleHasPendingRestart(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	req := authedReq("GET", "/api/sessions/t-does-not-exist/pending-restart", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	var resp struct {
		Pending bool `json:"pending"`
	}
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	testutil.False(t, resp.Pending)
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
