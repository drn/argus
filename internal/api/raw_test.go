package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// Tests hit srv.routes() directly (skipping authMiddleware) and use
// masterReq / deviceReq from messages_test.go to pre-stamp X-Argus-Auth.
// That mirrors how the requireMaster guard works in production (it reads
// the header that authMiddleware writes after token resolution).

func TestHandleListTasksRaw_Master(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()
	testutil.NoError(t, d.Add(&model.Task{Name: "alpha", Status: model.StatusInProgress, Project: "p", SessionID: "sess-xyz"}))

	t.Run("master sees full task incl SessionID", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, masterReq("GET", "/api/tasks-raw", ""))
		testutil.Equal(t, w.Code, http.StatusOK)
		testutil.Contains(t, w.Body.String(), "sess-xyz")
	})

	t.Run("device token rejected with 403", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, deviceReq("GET", "/api/tasks-raw", ""))
		testutil.Equal(t, w.Code, http.StatusForbidden)
	})
}

func TestHandleGetTaskRaw_Master(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()
	testutil.NoError(t, d.Add(&model.Task{ID: "t1", Name: "alpha", Status: model.StatusInProgress, Project: "p", SessionID: "sess-xyz"}))

	t.Run("master sees full task", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, masterReq("GET", "/api/tasks/t1/raw", ""))
		testutil.Equal(t, w.Code, http.StatusOK)
		testutil.Contains(t, w.Body.String(), "sess-xyz")
	})

	t.Run("device token rejected", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, deviceReq("GET", "/api/tasks/t1/raw", ""))
		testutil.Equal(t, w.Code, http.StatusForbidden)
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

	t.Run("device token rejected", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, deviceReq("PUT", "/api/tasks/t1/raw", `{"id":"t1"}`))
		testutil.Equal(t, w.Code, http.StatusForbidden)
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

	t.Run("device token rejected", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, deviceReq("POST", "/api/tasks-raw", `{}`))
		testutil.Equal(t, w.Code, http.StatusForbidden)
	})
}

func TestHandleGetScheduleRaw_Master(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	t.Run("device token rejected", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, deviceReq("GET", "/api/schedules/nope/raw", ""))
		testutil.Equal(t, w.Code, http.StatusForbidden)
	})

	t.Run("master gets 404 for unknown id", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, masterReq("GET", "/api/schedules/nope/raw", ""))
		testutil.Equal(t, w.Code, http.StatusNotFound)
	})
}

func TestHandleGetConfig_RequireMaster(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	t.Run("device token rejected", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, deviceReq("GET", "/api/config", ""))
		testutil.Equal(t, w.Code, http.StatusForbidden)
	})

	t.Run("master allowed", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, masterReq("GET", "/api/config", ""))
		testutil.Equal(t, w.Code, http.StatusOK)
	})
}
