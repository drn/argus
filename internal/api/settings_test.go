package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
)

func TestBuildSettingsUpdates(t *testing.T) {
	t.Run("empty request produces no updates", func(t *testing.T) {
		got := buildSettingsUpdates(updateSettingsReq{})
		testutil.Equal(t, len(got), 0)
	})

	t.Run("sandbox toggle and paths", func(t *testing.T) {
		on := true
		denyRead := []string{"/secrets", "~/.aws"}
		extraWrite := []string{}
		got := buildSettingsUpdates(updateSettingsReq{
			Sandbox: &sandboxUpdate{Enabled: &on, DenyRead: &denyRead, ExtraWrite: &extraWrite},
		})
		testutil.Equal(t, got["sandbox.enabled"], "true")
		testutil.Equal(t, got["sandbox.deny_read"], "/secrets,~/.aws")
		// Empty slice clears the value (joins to "").
		testutil.Equal(t, got["sandbox.extra_write"], "")
	})

	t.Run("auto_start toggle implies auto_create", func(t *testing.T) {
		on := true
		got := buildSettingsUpdates(updateSettingsReq{
			KB: &kbUpdate{AutoStartTodos: &on},
		})
		testutil.Equal(t, got["kb.auto_start_todos"], "true")
		testutil.Equal(t, got["kb.auto_create_tasks"], "true")

		off := false
		got = buildSettingsUpdates(updateSettingsReq{
			KB: &kbUpdate{AutoStartTodos: &off},
		})
		testutil.Equal(t, got["kb.auto_start_todos"], "false")
		testutil.Equal(t, got["kb.auto_create_tasks"], "false")
	})

	t.Run("explicit auto_create overrides implied", func(t *testing.T) {
		startOn := true
		createOff := false
		got := buildSettingsUpdates(updateSettingsReq{
			KB: &kbUpdate{AutoStartTodos: &startOn, AutoCreateTasks: &createOff},
		})
		testutil.Equal(t, got["kb.auto_create_tasks"], "false")
	})

	t.Run("interval rejects zero/negative", func(t *testing.T) {
		zero := 0
		got := buildSettingsUpdates(updateSettingsReq{KB: &kbUpdate{AutoStartInterval: &zero}})
		_, ok := got["kb.auto_start_interval"]
		testutil.Equal(t, ok, false)
	})

	t.Run("defaults flow through", func(t *testing.T) {
		backend := "claude"
		todo := "argus"
		prompt := "/review"
		got := buildSettingsUpdates(updateSettingsReq{
			Defaults: &defaultsUpdate{Backend: &backend, TodoProject: &todo, ReviewPrompt: &prompt},
		})
		testutil.Equal(t, got["defaults.backend"], "claude")
		testutil.Equal(t, got["defaults.todo_project"], "argus")
		testutil.Equal(t, got["defaults.review_prompt"], "/review")
	})
}

func TestHandleSettings_GetReturnsCurrentValues(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()

	testutil.NoError(t, d.SetConfigValue("sandbox.enabled", "true"))
	testutil.NoError(t, d.SetConfigValue("sandbox.deny_read", "/secrets,~/.aws"))
	testutil.NoError(t, d.SetConfigValue("kb.enabled", "true"))
	testutil.NoError(t, d.SetConfigValue("defaults.review_prompt", "/review-strict"))

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authedReq("GET", "/api/settings", ""))
	testutil.Equal(t, w.Code, http.StatusOK)

	var resp settingsResponse
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	testutil.Equal(t, resp.Sandbox.Enabled, true)
	testutil.DeepEqual(t, resp.Sandbox.DenyRead, []string{"/secrets", "~/.aws"})
	testutil.Equal(t, resp.KB.Enabled, true)
	testutil.Equal(t, resp.Defaults.ReviewPrompt, "/review-strict")
}

func TestHandleSettings_PutPersists(t *testing.T) {
	srv, d := testServer(t)
	handler := authMiddleware(srv.token, d, srv.routes())

	body := `{"sandbox": {"enabled": true, "deny_read": ["/etc"]},
	          "kb": {"enabled": true, "metis_vault_path": "/tmp/m"},
	          "defaults": {"review_prompt": "/review"}}`
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, authedReq("PUT", "/api/settings", body))
	testutil.Equal(t, w.Code, http.StatusOK)

	cfg := d.Config()
	testutil.Equal(t, cfg.Sandbox.Enabled, true)
	testutil.DeepEqual(t, cfg.Sandbox.DenyRead, []string{"/etc"})
	testutil.Equal(t, cfg.KB.Enabled, true)
	testutil.Equal(t, cfg.KB.MetisVaultPath, "/tmp/m")
	testutil.Equal(t, cfg.Defaults.ReviewPrompt, "/review")
}

func TestHandleSettings_PutRequiresMaster(t *testing.T) {
	srv, d := testServer(t)
	handler := authMiddleware(srv.token, d, srv.routes())
	plain, _, err := MintToken(d, "phone")
	testutil.NoError(t, err)

	req := httptest.NewRequest("PUT", "/api/settings", strings.NewReader(`{"sandbox":{"enabled":true}}`))
	req.Header.Set("Authorization", "Bearer "+plain)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusForbidden)
}

func TestHandleSettings_GetIsAvailableToDevice(t *testing.T) {
	srv, d := testServer(t)
	handler := authMiddleware(srv.token, d, srv.routes())
	plain, _, err := MintToken(d, "phone")
	testutil.NoError(t, err)

	req := httptest.NewRequest("GET", "/api/settings", nil)
	req.Header.Set("Authorization", "Bearer "+plain)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)
}

func TestHandleGetLog(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("ARGUS_DATA_DIR", "")
	// HOME-rooted ~/.argus/ux.log path.
	dataDir := filepath.Join(dir, ".argus")
	testutil.NoError(t, os.MkdirAll(dataDir, 0o700))
	testutil.NoError(t, os.WriteFile(filepath.Join(dataDir, "ux.log"), []byte("hello\nworld\n"), 0o600))

	srv, d := testServer(t)
	mux := srv.routes()

	t.Run("ux returns content", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, authedReq("GET", "/api/logs/ux", ""))
		testutil.Equal(t, w.Code, http.StatusOK)
		testutil.Contains(t, w.Body.String(), "hello")
	})

	t.Run("unknown rejected", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, authedReq("GET", "/api/logs/secret", ""))
		testutil.Equal(t, w.Code, http.StatusBadRequest)
	})

	t.Run("daemon missing returns empty", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, authedReq("GET", "/api/logs/daemon", ""))
		// Missing log returns 200 with empty body so the SPA can render
		// "(empty)" without special-casing.
		testutil.Equal(t, w.Code, http.StatusOK)
		testutil.Equal(t, w.Body.Len(), 0)
	})

	t.Run("device tokens can read", func(t *testing.T) {
		// Pin the intent: log-tail is read-only and available to device
		// tokens (same policy as GET /api/settings). If logs ever need to
		// be master-only, this test will catch the change.
		handler := authMiddleware(srv.token, d, srv.routes())
		plain, _, err := MintToken(d, "phone")
		testutil.NoError(t, err)

		req := httptest.NewRequest("GET", "/api/logs/ux", nil)
		req.Header.Set("Authorization", "Bearer "+plain)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)
		testutil.Contains(t, w.Body.String(), "hello")
	})
}

func TestHandleProjects_RoundTripsSandboxOverride(t *testing.T) {
	srv, d := testServer(t)
	handler := authMiddleware(srv.token, d, srv.routes())

	body := `{
	  "name": "alpha",
	  "path": "/tmp/alpha",
	  "branch": "main",
	  "sandbox": {"enabled": false, "deny_read": ["/secrets"], "extra_write": ["~/.npm"]}
	}`
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, authedReq("POST", "/api/projects", body))
	testutil.Equal(t, w.Code, http.StatusCreated)

	// Verify it round-trips through the DB and back through GET.
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, authedReq("GET", "/api/projects/full", ""))
	testutil.Equal(t, w.Code, http.StatusOK)
	var resp struct {
		Projects []projectJSON `json:"projects"`
	}
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	testutil.Equal(t, len(resp.Projects), 1)
	p := resp.Projects[0]
	testutil.Equal(t, p.Name, "alpha")
	testutil.Equal(t, p.Path, "/tmp/alpha")
	testutil.Equal(t, p.Branch, "main")
	if p.Sandbox == nil {
		t.Fatalf("expected sandbox override, got nil")
	}
	if p.Sandbox.Enabled == nil || *p.Sandbox.Enabled {
		t.Fatalf("expected sandbox.enabled false, got %#v", p.Sandbox.Enabled)
	}
	testutil.DeepEqual(t, p.Sandbox.DenyRead, []string{"/secrets"})
	testutil.DeepEqual(t, p.Sandbox.ExtraWrite, []string{"~/.npm"})

	// And the DB stored it via the existing config.Project shape.
	projects, err := d.Projects()
	testutil.NoError(t, err)
	stored, ok := projects["alpha"]
	testutil.Equal(t, ok, true)
	if stored.Sandbox.Enabled == nil || *stored.Sandbox.Enabled {
		t.Fatalf("expected stored Sandbox.Enabled false, got %#v", stored.Sandbox.Enabled)
	}
	testutil.DeepEqual(t, stored.Sandbox.DenyRead, []string{"/secrets"})
}

func TestProjectFromJSON_NilSandboxStaysInherit(t *testing.T) {
	got := projectFromJSON(projectJSON{Name: "x", Path: "/tmp/x"})
	if got.Sandbox.Enabled != nil {
		t.Fatalf("expected inherit (nil), got %#v", got.Sandbox.Enabled)
	}
}

// Sanity check that DefaultConfig still returns the expected sandbox shape;
// guards against a refactor that drops the SandboxConfig type while leaving
// API code that references it.
func TestSandboxConfig_IsValueType(t *testing.T) {
	cfg := config.DefaultConfig()
	testutil.Equal(t, cfg.Sandbox.Enabled, false)
}
