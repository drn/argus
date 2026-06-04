package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// Tests hit srv.routes() directly (skipping authMiddleware) and use
// masterReq / deviceReq from messages_test.go to pre-stamp X-Argus-Auth.
// Under the single-tier auth model the raw endpoints are open to any
// authenticated token (no credentials in the model.Task shape); these tests
// pin that both master and device tokens are accepted.

func TestHandleListTasksRaw_AnyToken(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()
	testutil.NoError(t, d.Add(&model.Task{Name: "alpha", Status: model.StatusInProgress, Project: "p", SessionID: "sess-xyz"}))

	t.Run("master sees full task incl SessionID", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, masterReq("GET", "/api/tasks-raw", ""))
		testutil.Equal(t, w.Code, http.StatusOK)
		testutil.Contains(t, w.Body.String(), "sess-xyz")
	})

	t.Run("device token also sees full task", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, deviceReq("GET", "/api/tasks-raw", ""))
		testutil.Equal(t, w.Code, http.StatusOK)
		testutil.Contains(t, w.Body.String(), "sess-xyz")
	})
}

func TestHandleGetTaskRaw_AnyToken(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()
	testutil.NoError(t, d.Add(&model.Task{ID: "t1", Name: "alpha", Status: model.StatusInProgress, Project: "p", SessionID: "sess-xyz"}))

	t.Run("master sees full task", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, masterReq("GET", "/api/tasks/t1/raw", ""))
		testutil.Equal(t, w.Code, http.StatusOK)
		testutil.Contains(t, w.Body.String(), "sess-xyz")
	})

	t.Run("device token also allowed", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, deviceReq("GET", "/api/tasks/t1/raw", ""))
		testutil.Equal(t, w.Code, http.StatusOK)
		testutil.Contains(t, w.Body.String(), "sess-xyz")
	})

	t.Run("missing task returns 404", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, masterReq("GET", "/api/tasks/nope/raw", ""))
		testutil.Equal(t, w.Code, http.StatusNotFound)
	})
}

func TestHandleUpdateTaskRaw(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()
	testutil.NoError(t, d.Add(&model.Task{ID: "t1", Name: "alpha", Status: model.StatusInProgress, Project: "p"}))

	t.Run("path id and body id must match", func(t *testing.T) {
		body := `{"id":"different","name":"alpha","status":"in_progress","project":"p","branch":"","prompt":"","created_at":"2026-05-22T00:00:00Z"}`
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, masterReq("PUT", "/api/tasks/t1/raw", body))
		testutil.Equal(t, w.Code, http.StatusBadRequest)
		testutil.Contains(t, w.Body.String(), "body id does not match")
	})

	t.Run("matching ids apply the update", func(t *testing.T) {
		body := `{"id":"t1","name":"renamed","status":"in_review","project":"p","branch":"","prompt":"","created_at":"2026-05-22T00:00:00Z"}`
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, masterReq("PUT", "/api/tasks/t1/raw", body))
		testutil.Equal(t, w.Code, http.StatusOK)
		got, _ := d.Get("t1")
		testutil.Equal(t, got.Name, "renamed")
		testutil.Equal(t, got.Status, model.StatusInReview)
	})

	t.Run("device token also applies the update", func(t *testing.T) {
		body := `{"id":"t1","name":"viadevice","status":"in_review","project":"p","branch":"","prompt":"","created_at":"2026-05-22T00:00:00Z"}`
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, deviceReq("PUT", "/api/tasks/t1/raw", body))
		testutil.Equal(t, w.Code, http.StatusOK)
		got, _ := d.Get("t1")
		testutil.Equal(t, got.Name, "viadevice")
	})
}

func TestHandleAddTaskRaw(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()

	t.Run("inserts and returns assigned ID", func(t *testing.T) {
		body := `{"name":"new","status":"pending","project":"p","branch":"","prompt":"hi","created_at":"2026-05-22T00:00:00Z"}`
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, masterReq("POST", "/api/tasks-raw", body))
		testutil.Equal(t, w.Code, http.StatusCreated)

		var got model.Task
		testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
		if got.ID == "" {
			t.Fatal("response missing server-assigned ID")
		}
		_, gerr := d.Get(got.ID)
		testutil.NoError(t, gerr)
	})

	t.Run("device token also inserts", func(t *testing.T) {
		body := `{"name":"viadevice","status":"pending","project":"p","branch":"","prompt":"hi","created_at":"2026-05-22T00:00:00Z"}`
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, deviceReq("POST", "/api/tasks-raw", body))
		testutil.Equal(t, w.Code, http.StatusCreated)
	})
}

// TestHandleAddTaskRaw_WorktreeGuard pins the trust boundary on the now-open
// raw insert: a body Worktree outside the canonical worktrees root must be
// rejected (it would later flow to os.RemoveAll on delete), while an in-root
// path is accepted. Empty Worktree (the common case) is covered above.
func TestHandleAddTaskRaw_WorktreeGuard(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // db.DataDir() roots under HOME/.argus
	srv, d := testServer(t)
	mux := srv.routes()
	root := filepath.Join(db.DataDir(), "worktrees")

	t.Run("out-of-root worktree rejected for device token", func(t *testing.T) {
		body := `{"name":"evil","status":"pending","project":"p","worktree":"/tmp/evil","created_at":"2026-05-22T00:00:00Z"}`
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, deviceReq("POST", "/api/tasks-raw", body))
		testutil.Equal(t, w.Code, http.StatusBadRequest)
		testutil.Contains(t, w.Body.String(), "worktrees root")
	})

	t.Run("worktrees root itself rejected", func(t *testing.T) {
		body := `{"name":"evil3","status":"pending","project":"p","worktree":"` + root + `","created_at":"2026-05-22T00:00:00Z"}`
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, deviceReq("POST", "/api/tasks-raw", body))
		testutil.Equal(t, w.Code, http.StatusBadRequest)
	})

	t.Run("traversal escaping root rejected", func(t *testing.T) {
		body := `{"name":"evil2","status":"pending","project":"p","worktree":"` +
			filepath.Join(root, "..", "..", "escape") + `","created_at":"2026-05-22T00:00:00Z"}`
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, deviceReq("POST", "/api/tasks-raw", body))
		testutil.Equal(t, w.Code, http.StatusBadRequest)
	})

	t.Run("in-root worktree accepted", func(t *testing.T) {
		wt := filepath.Join(root, "p", "ok")
		body := `{"name":"ok","status":"pending","project":"p","worktree":"` + wt + `","created_at":"2026-05-22T00:00:00Z"}`
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, deviceReq("POST", "/api/tasks-raw", body))
		testutil.Equal(t, w.Code, http.StatusCreated)
		var got model.Task
		testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
		stored, err := d.Get(got.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, stored.Worktree, wt)
	})
}

func TestHandleGetScheduleRaw_AnyToken(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	t.Run("device token allowed (404 for unknown id, not 403)", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, deviceReq("GET", "/api/schedules/nope/raw", ""))
		testutil.Equal(t, w.Code, http.StatusNotFound)
	})

	t.Run("master gets 404 for unknown id", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, masterReq("GET", "/api/schedules/nope/raw", ""))
		testutil.Equal(t, w.Code, http.StatusNotFound)
	})
}

func TestHandleGetConfig_AnyToken(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	t.Run("device token allowed", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, deviceReq("GET", "/api/config", ""))
		testutil.Equal(t, w.Code, http.StatusOK)
	})

	t.Run("master allowed", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, masterReq("GET", "/api/config", ""))
		testutil.Equal(t, w.Code, http.StatusOK)
	})
}
