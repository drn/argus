package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// seedArtifact writes bytes into the task's durable artifact dir and records a
// manifest row, returning the task ID. Caller must have redirected HOME.
func seedArtifact(t *testing.T, d *db.DB, taskID, filename string, atype model.ArtifactType, content string) {
	t.Helper()
	dir := agent.ArtifactsDir(taskID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.UpsertArtifact(&model.Artifact{
		TaskID:   taskID,
		Name:     filename,
		Filename: filename,
		Type:     atype,
		Size:     int64(len(content)),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestHandleListArtifacts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv, d := testServer(t)
	mux := srv.routes()

	task := &model.Task{Name: "t", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))
	seedArtifact(t, d, task.ID, "a.html", model.ArtifactHTML, "<p>1</p>")
	seedArtifact(t, d, task.ID, "b.pdf", model.ArtifactPDF, "%PDF-1.4")

	req := authedReq("GET", "/api/tasks/"+task.ID+"/artifacts", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	testutil.Equal(t, w.Code, http.StatusOK)
	testutil.Contains(t, w.Body.String(), "a.html")
	testutil.Contains(t, w.Body.String(), "b.pdf")
}

func TestHandleListArtifacts_EmptyIsArray(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv, d := testServer(t)
	mux := srv.routes()
	task := &model.Task{Name: "t", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))

	req := authedReq("GET", "/api/tasks/"+task.ID+"/artifacts", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)
	testutil.Contains(t, w.Body.String(), `"artifacts":[]`)
}

func TestHandleListArtifacts_TaskNotFound(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv, _ := testServer(t)
	mux := srv.routes()
	req := authedReq("GET", "/api/tasks/nope/artifacts", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusNotFound)
}

func TestHandleGetArtifact_ContentTypeAndHeaders(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv, d := testServer(t)
	mux := srv.routes()
	task := &model.Task{Name: "t", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))

	tests := []struct {
		filename string
		atype    model.ArtifactType
		body     string
		wantCT   string
	}{
		{"r.html", model.ArtifactHTML, "<h1>hi</h1>", "text/html; charset=utf-8"},
		{"r.md", model.ArtifactMarkdown, "# hi", "text/markdown; charset=utf-8"},
		{"r.pdf", model.ArtifactPDF, "%PDF", "application/pdf"},
		{"r.png", model.ArtifactImage, "\x89PNG", "image/png"},
		{"r.txt", model.ArtifactText, "plain", "text/plain; charset=utf-8"},
	}
	for _, tc := range tests {
		t.Run(tc.filename, func(t *testing.T) {
			seedArtifact(t, d, task.ID, tc.filename, tc.atype, tc.body)
			req := authedReq("GET", "/api/tasks/"+task.ID+"/artifacts/"+tc.filename, "")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			testutil.Equal(t, w.Code, http.StatusOK)
			testutil.Equal(t, w.Header().Get("Content-Type"), tc.wantCT)
			// X-Frame-Options must be relaxed to SAMEORIGIN (corsMiddleware's
			// global DENY would otherwise block the SPA iframe).
			testutil.Equal(t, w.Header().Get("X-Frame-Options"), "SAMEORIGIN")
			testutil.Equal(t, w.Header().Get("Cache-Control"), "no-store")
			testutil.Equal(t, w.Body.String(), tc.body)
		})
	}
}

func TestHandleGetArtifact_UnregisteredName404(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv, d := testServer(t)
	mux := srv.routes()
	task := &model.Task{Name: "t", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))

	// Physically place a file on disk WITHOUT a manifest row — must not serve.
	dir := agent.ArtifactsDir(task.ID)
	os.MkdirAll(dir, 0o700)                                             //nolint:errcheck
	os.WriteFile(filepath.Join(dir, "secret.html"), []byte("x"), 0o600) //nolint:errcheck

	req := authedReq("GET", "/api/tasks/"+task.ID+"/artifacts/secret.html", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusNotFound)
}

func TestHandleGetArtifact_MissingBytes404(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv, d := testServer(t)
	mux := srv.routes()
	task := &model.Task{Name: "t", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))
	// Manifest row exists but the file was never written / was deleted.
	d.UpsertArtifact(&model.Artifact{TaskID: task.ID, Name: "gone.html", Filename: "gone.html", Type: model.ArtifactHTML}) //nolint:errcheck

	req := authedReq("GET", "/api/tasks/"+task.ID+"/artifacts/gone.html", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusNotFound)
}

// TestHandleGetArtifact_RequiresAuth wires the real auth middleware (the routes
// mux alone has none) and confirms artifact routes are NOT in the skip list.
func TestHandleGetArtifact_RequiresAuth(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv, d := testServer(t)
	task := &model.Task{Name: "t", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))
	seedArtifact(t, d, task.ID, "r.html", model.ArtifactHTML, "<p>x</p>")

	handler := authMiddleware(srv.token, srv.db, srv.push, srv.routes(),
		"/", "/share", "/vendor/", "/manifest.webmanifest", "/sw.js")

	// No token → 401 for both list and raw routes.
	for _, path := range []string{
		"/api/tasks/" + task.ID + "/artifacts",
		"/api/tasks/" + task.ID + "/artifacts/r.html",
	} {
		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusUnauthorized)
	}

	// With token → 200.
	req := httptest.NewRequest("GET", "/api/tasks/"+task.ID+"/artifacts/r.html", nil)
	req.Header.Set("Authorization", "Bearer "+srv.token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)
}

// TestResolveArtifactPath_RejectsTraversalAndSymlinks pins the path-defense
// helper directly (the manifest allowlist is the first gate; this is the
// belt-and-suspenders second one).
func TestResolveArtifactPath_RejectsTraversalAndSymlinks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := agent.ArtifactsDir("task1")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Legit file.
	if err := os.WriteFile(filepath.Join(dir, "ok.html"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("legit basename resolves", func(t *testing.T) {
		got, ok := resolveArtifactPath("task1", "ok.html")
		testutil.True(t, ok)
		testutil.Equal(t, got, filepath.Join(dir, "ok.html"))
	})

	t.Run("traversal rejected", func(t *testing.T) {
		_, ok := resolveArtifactPath("task1", "../../../etc/passwd")
		testutil.True(t, !ok)
	})

	t.Run("nested path rejected", func(t *testing.T) {
		_, ok := resolveArtifactPath("task1", "sub/evil")
		testutil.True(t, !ok)
	})

	t.Run("symlink escape rejected", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlink semantics differ on windows")
		}
		// Create a target outside the artifact dir and a symlink to it inside.
		outside := filepath.Join(t.TempDir(), "secret.txt")
		if err := os.WriteFile(outside, []byte("top secret"), 0o600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "link.html")
		if err := os.Symlink(outside, link); err != nil {
			t.Fatal(err)
		}
		_, ok := resolveArtifactPath("task1", "link.html")
		testutil.True(t, !ok)
	})
}

// TestDeleteTask_RemovesArtifacts covers the cleanup wired into handleDeleteTask.
func TestDeleteTask_RemovesArtifacts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv, d := testServer(t)
	mux := srv.routes()
	task := &model.Task{Name: "t", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))
	seedArtifact(t, d, task.ID, "r.html", model.ArtifactHTML, "<p>x</p>")
	dir := agent.ArtifactsDir(task.ID)

	req := authedReq("DELETE", "/api/tasks/"+task.ID, "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	// Manifest rows gone.
	rows, err := d.Artifacts(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(rows), 0)
	// On-disk dir gone.
	_, statErr := os.Stat(dir)
	testutil.True(t, os.IsNotExist(statErr))
}
