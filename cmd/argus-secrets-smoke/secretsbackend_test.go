package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
)

func newSmokeForTest(srv *httptest.Server) *smoke {
	return &smoke{
		baseURL:     srv.URL,
		scopeToken:  "scope",
		masterToken: "master",
		project:     "ARGUS",
		httpClient:  &http.Client{Timeout: 5 * time.Second},
	}
}

// --- ensureSecretsBackendREST ---

func TestEnsureSecretsBackendREST_CreatedOwnsCleanup(t *testing.T) {
	var gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)
	s := newSmokeForTest(srv)

	owned, err := s.ensureSecretsBackendREST("secrets-smoke", secretsSmokeCommand)
	testutil.NoError(t, err)
	if !owned {
		t.Fatal("expected owned=true on 201")
	}
	testutil.Equal(t, gotAuth, "Bearer master")
	testutil.Contains(t, gotBody, `"name":"secrets-smoke"`)
}

func TestEnsureSecretsBackendREST_ConflictReusesWithoutOwnership(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "already exists", http.StatusConflict)
	}))
	t.Cleanup(srv.Close)
	s := newSmokeForTest(srv)

	owned, err := s.ensureSecretsBackendREST("secrets-smoke", secretsSmokeCommand)
	testutil.NoError(t, err)
	if owned {
		t.Fatal("expected owned=false on 409 so this call alone doesn't claim cleanup")
	}
}

func TestEnsureSecretsBackendREST_OtherErrorSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	s := newSmokeForTest(srv)

	_, err := s.ensureSecretsBackendREST("secrets-smoke", secretsSmokeCommand)
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected 500 error, got %v", err)
	}
}

// --- createSmokeTask ---

func TestCreateSmokeTask_ParsesID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		testutil.Contains(t, string(body), `"project":"ARGUS"`)
		testutil.Contains(t, string(body), `"backend":"secrets-smoke"`)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"abc123","name":"x"}`))
	}))
	t.Cleanup(srv.Close)
	s := newSmokeForTest(srv)

	id, err := s.createSmokeTask("x", "secrets-smoke")
	testutil.NoError(t, err)
	testutil.Equal(t, id, "abc123")
}

func TestCreateSmokeTask_EmptyIDSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	s := newSmokeForTest(srv)

	_, err := s.createSmokeTask("x", "secrets-smoke")
	if err == nil || !strings.Contains(err.Error(), "empty task id") {
		t.Fatalf("expected empty-id error, got %v", err)
	}
}

// --- fetchTaskStatus / fetchOutput ---

func TestFetchTaskStatus_UsesScopeTokenAndParsesStatus(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"id":"t1","status":"complete"}`))
	}))
	t.Cleanup(srv.Close)
	s := newSmokeForTest(srv)

	status, err := s.fetchTaskStatus("t1")
	testutil.NoError(t, err)
	testutil.Equal(t, status, "complete")
	testutil.Equal(t, gotAuth, "Bearer scope")
}

func TestFetchOutput_ReturnsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testutil.Equal(t, r.URL.Query().Get("clean"), "1")
		_, _ = w.Write([]byte("SMOKE_KEYCHAIN_OK=yes\nSMOKE_KEYCHAIN_BAD=no\n"))
	}))
	t.Cleanup(srv.Close)
	s := newSmokeForTest(srv)

	out, err := s.fetchOutput("t1")
	testutil.NoError(t, err)
	testutil.Contains(t, out, "SMOKE_KEYCHAIN_OK=yes")
	testutil.Contains(t, out, "SMOKE_KEYCHAIN_BAD=no")
}

func TestFetchOutput_NonOKSurfacesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	s := newSmokeForTest(srv)

	_, err := s.fetchOutput("t1")
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected 404 error, got %v", err)
	}
}

// --- awaitTaskDone polling ---

func TestAwaitTaskDone_ReturnsTerminalStatusAfterPolling(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		status := "in_progress"
		if calls >= 3 {
			status = "complete"
		}
		_, _ = w.Write([]byte(`{"id":"t1","status":"` + status + `"}`))
	}))
	t.Cleanup(srv.Close)
	s := newSmokeForTest(srv)

	status, err := s.awaitTaskDone("t1", 5*time.Second)
	testutil.NoError(t, err)
	testutil.Equal(t, status, "complete")
	if calls < 3 {
		t.Fatalf("expected at least 3 polls, got %d", calls)
	}
}

func TestAwaitTaskDone_TimesOutOnStuckInProgress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"t1","status":"in_progress"}`))
	}))
	t.Cleanup(srv.Close)
	s := newSmokeForTest(srv)

	_, err := s.awaitTaskDone("t1", 250*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "did not leave status") {
		t.Fatalf("expected timeout error, got %v", err)
	}
}

// --- fetchOutputUntilMarkers ---

func TestFetchOutputUntilMarkers_ReturnsOnceBothPresent(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 2 {
			_, _ = w.Write([]byte("SMOKE_KEYCHAIN_OK=yes\n")) // BAD marker not yet flushed
			return
		}
		_, _ = w.Write([]byte("SMOKE_KEYCHAIN_OK=yes\nSMOKE_KEYCHAIN_BAD=no\n"))
	}))
	t.Cleanup(srv.Close)
	s := newSmokeForTest(srv)

	out, err := s.fetchOutputUntilMarkers("t1", 2*time.Second)
	testutil.NoError(t, err)
	testutil.Contains(t, out, "SMOKE_KEYCHAIN_BAD=no")
}

func TestFetchOutputUntilMarkers_ReturnsLastSeenOnTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("SMOKE_KEYCHAIN_OK=yes\n")) // BAD marker never appears
	}))
	t.Cleanup(srv.Close)
	s := newSmokeForTest(srv)

	out, err := s.fetchOutputUntilMarkers("t1", 250*time.Millisecond)
	testutil.NoError(t, err) // times out silently — caller does the precise assertion
	testutil.Contains(t, out, "SMOKE_KEYCHAIN_OK=yes")
	if strings.Contains(out, "SMOKE_KEYCHAIN_BAD") {
		t.Fatalf("did not expect BAD marker in output: %q", out)
	}
}

// --- assertOutputContains (pure) ---

func TestAssertOutputContains_PassesOnMatch(t *testing.T) {
	testutil.NoError(t, assertOutputContains("SMOKE_KEYCHAIN_OK=yes\n", "SMOKE_KEYCHAIN_OK=yes", "success path"))
}

func TestAssertOutputContains_FailsWithFullOutputInDetail(t *testing.T) {
	err := assertOutputContains("SMOKE_KEYCHAIN_OK=no\n", "SMOKE_KEYCHAIN_OK=yes", "success path")
	if err == nil {
		t.Fatal("expected error")
	}
	testutil.Contains(t, err.Error(), "success path")
	testutil.Contains(t, err.Error(), "SMOKE_KEYCHAIN_OK=yes")
	testutil.Contains(t, err.Error(), "SMOKE_KEYCHAIN_OK=no")
}

// --- resolveKnownGoodSource / attachSecretsEnvVars (real temp-file DB) ---

func TestResolveKnownGoodSource_ReadsConfiguredBootstrapSource(t *testing.T) {
	dir := t.TempDir()
	tomlPath := filepath.Join(dir, "config.toml")
	testutil.NoError(t, os.WriteFile(tomlPath, []byte("[secrets.op]\nbootstrap_source = \"keychain://test-item\"\n"), 0o600))
	dbPath := filepath.Join(dir, "data.sql")

	got, err := resolveKnownGoodSource(dbPath)
	testutil.NoError(t, err)
	testutil.Equal(t, got, "keychain://test-item")
}

func TestResolveKnownGoodSource_ErrorsWhenUnconfigured(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "data.sql")

	_, err := resolveKnownGoodSource(dbPath)
	if err == nil || !strings.Contains(err.Error(), "bootstrap_source") {
		t.Fatalf("expected an unconfigured-bootstrap-source error, got %v", err)
	}
}

func TestAttachSecretsEnvVars_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "data.sql")
	envVars := map[string]string{
		"SMOKE_KEYCHAIN_OK":  "keychain://good-item",
		"SMOKE_KEYCHAIN_BAD": bogusKeychainSource,
	}

	testutil.NoError(t, attachSecretsEnvVars(dbPath, "secrets-smoke", secretsSmokeCommand, envVars))

	d, err := db.Open(dbPath)
	testutil.NoError(t, err)
	defer d.Close() //nolint:errcheck

	backends, err := d.Backends()
	testutil.NoError(t, err)
	b, ok := backends["secrets-smoke"]
	if !ok {
		t.Fatal("expected backend \"secrets-smoke\" to exist after attachSecretsEnvVars")
	}
	testutil.Equal(t, b.Command, secretsSmokeCommand)
	testutil.Equal(t, len(b.EnvVars), 2)
	testutil.Equal(t, b.EnvVars["SMOKE_KEYCHAIN_OK"], "keychain://good-item")
	testutil.Equal(t, b.EnvVars["SMOKE_KEYCHAIN_BAD"], bogusKeychainSource)
}
