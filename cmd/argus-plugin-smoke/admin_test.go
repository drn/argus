package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

func newSmokeForTest(srv *httptest.Server) *smoke {
	return &smoke{
		baseURL:     srv.URL,
		scopeToken:  "scope",
		masterToken: "master",
		scope:       "smoke",
		project:     "ARGUS",
		httpClient:  &http.Client{Timeout: 5 * time.Second},
	}
}

func TestAdminPOST_SendsMasterToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)
	s := newSmokeForTest(srv)
	resp, err := s.adminPOST("/foo", `{"a":1}`)
	testutil.NoError(t, err)
	_ = resp.Body.Close()
	testutil.Equal(t, gotAuth, "Bearer master")
}

func TestAdminDELETE_StatusMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testutil.Equal(t, r.Method, http.MethodDelete)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	s := newSmokeForTest(srv)
	testutil.NoError(t, s.adminDELETE("/foo", http.StatusOK))
}

func TestAdminDELETE_StatusMismatchSurfacesBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "task not found", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	s := newSmokeForTest(srv)
	err := s.adminDELETE("/foo", http.StatusOK)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "task not found") {
		t.Fatalf("expected status + body in error, got %v", err)
	}
}

func TestEnsureBashBackend_CreatedOwnsCleanup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		testutil.Contains(t, string(body), `"command":"sh -c cat"`)
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)
	s := newSmokeForTest(srv)
	owned, err := s.ensureBashBackend("smoke-cat")
	testutil.NoError(t, err)
	if !owned {
		t.Fatal("expected owned=true on 201")
	}
}

func TestEnsureBashBackend_ConflictReusesWithoutOwnership(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "already exists", http.StatusConflict)
	}))
	t.Cleanup(srv.Close)
	s := newSmokeForTest(srv)
	owned, err := s.ensureBashBackend("bash-smoke")
	testutil.NoError(t, err)
	if owned {
		t.Fatal("expected owned=false on 409 so cleanup leaves it alone")
	}
}

func TestEnsureBashBackend_OtherErrorSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	s := newSmokeForTest(srv)
	_, err := s.ensureBashBackend("bash-smoke")
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected 500 error, got %v", err)
	}
}

func TestCreateBashTask_ParsesID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		testutil.Contains(t, string(body), `"project":"ARGUS"`)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"abc123","name":"x"}`))
	}))
	t.Cleanup(srv.Close)
	s := newSmokeForTest(srv)
	id, err := s.createBashTask("x", "bash-smoke")
	testutil.NoError(t, err)
	testutil.Equal(t, id, "abc123")
}

func TestCreateBashTask_EmptyIDSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	s := newSmokeForTest(srv)
	_, err := s.createBashTask("x", "bash-smoke")
	if err == nil || !strings.Contains(err.Error(), "empty task id") {
		t.Fatalf("expected empty-id error, got %v", err)
	}
}
