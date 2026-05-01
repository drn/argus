package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/drn/argus/internal/clipboard"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// clipboardServer returns a server with the given task IDs seeded in the DB
// so the IDOR-prevention 404 check in the handlers passes through to the
// clipboard logic. Without seeded tasks the handlers (correctly) reject
// unknown IDs with 404, masking what the test is trying to assert.
func clipboardServer(t *testing.T, taskIDs ...string) (*Server, *db.DB) {
	t.Helper()
	srv, d := testServer(t)
	srv.SetClipboard(clipboard.New())
	for _, id := range taskIDs {
		if err := d.Add(&model.Task{ID: id, Name: id, Status: model.StatusInProgress}); err != nil {
			t.Fatalf("seed task %s: %v", id, err)
		}
	}
	return srv, d
}

func TestClipboard_GetEmpty(t *testing.T) {
	srv, _ := clipboardServer(t, "task1")
	mux := srv.routes()

	req := authedReq("GET", "/api/tasks/task1/clipboard", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	testutil.Equal(t, w.Code, http.StatusNoContent)
}

func TestClipboard_SetAndGet(t *testing.T) {
	srv, _ := clipboardServer(t, "task1")
	mux := srv.routes()

	// POST text.
	req := authedReq("POST", "/api/tasks/task1/clipboard", `{"text":"hello"}`)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	// GET it back.
	req = authedReq("GET", "/api/tasks/task1/clipboard", "")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	var resp clipboardGetResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	testutil.Equal(t, resp.Text, "hello")
}

func TestClipboard_Clear(t *testing.T) {
	srv, _ := clipboardServer(t, "task1")
	mux := srv.routes()

	// Stage.
	req := authedReq("POST", "/api/tasks/task1/clipboard", `{"text":"hi"}`)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	// DELETE.
	req = authedReq("DELETE", "/api/tasks/task1/clipboard", "")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	// Confirm cleared.
	req = authedReq("GET", "/api/tasks/task1/clipboard", "")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusNoContent)
}

func TestClipboard_PerTaskIsolation(t *testing.T) {
	srv, _ := clipboardServer(t, "task1", "task2")
	mux := srv.routes()

	for _, tc := range []struct {
		task string
		text string
	}{
		{"task1", "alpha"},
		{"task2", "bravo"},
	} {
		req := authedReq("POST", "/api/tasks/"+tc.task+"/clipboard", `{"text":"`+tc.text+`"}`)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)
	}

	for _, tc := range []struct {
		task string
		want string
	}{
		{"task1", "alpha"},
		{"task2", "bravo"},
	} {
		req := authedReq("GET", "/api/tasks/"+tc.task+"/clipboard", "")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		var resp clipboardGetResp
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		testutil.Equal(t, resp.Text, tc.want)
	}
}

func TestClipboard_TooLarge(t *testing.T) {
	srv, _ := clipboardServer(t, "task1")
	mux := srv.routes()

	// 1 MiB + 1 byte → rejected by the handler-side MaxBytesReader before
	// the store-side cap, so the client sees 400. Either layer rejecting
	// is acceptable; we assert on the user-visible status code only.
	body := `{"text":"` + strings.Repeat("a", clipboard.MaxTextSize+1) + `"}`
	req := authedReq("POST", "/api/tasks/task1/clipboard", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusBadRequest)
}

func TestClipboard_UnknownTaskReturns404(t *testing.T) {
	srv, _ := clipboardServer(t) // no seeded tasks
	mux := srv.routes()

	cases := []struct {
		method string
		body   string
	}{
		{"GET", ""},
		{"POST", `{"text":"x"}`},
		{"DELETE", ""},
	}
	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			req := authedReq(tc.method, "/api/tasks/missing/clipboard", tc.body)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			testutil.Equal(t, w.Code, http.StatusNotFound)
		})
	}
}

func TestClipboard_NoStoreReturns503OrEmpty(t *testing.T) {
	srv, _ := testServer(t)
	// Don't call SetClipboard — store is nil.
	mux := srv.routes()

	t.Run("GET returns 204", func(t *testing.T) {
		req := authedReq("GET", "/api/tasks/task1/clipboard", "")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusNoContent)
	})

	t.Run("POST returns 503", func(t *testing.T) {
		req := authedReq("POST", "/api/tasks/task1/clipboard", `{"text":"hi"}`)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusServiceUnavailable)
	})

	t.Run("DELETE returns 503", func(t *testing.T) {
		req := authedReq("DELETE", "/api/tasks/task1/clipboard", "")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusServiceUnavailable)
	})
}

func TestClipboard_BadJSONRejected(t *testing.T) {
	srv, _ := clipboardServer(t, "task1")
	mux := srv.routes()

	req := authedReq("POST", "/api/tasks/task1/clipboard", `not json`)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusBadRequest)
}

func TestClipboard_AuthRequired(t *testing.T) {
	// Auth is wrapped at ListenAndServe, not at routes(); routes() returns the
	// inner mux without auth. This test verifies the route is registered;
	// auth is exercised by the existing auth_test.go suite.
	srv, _ := clipboardServer(t, "task1")
	mux := srv.routes()

	req := httptest.NewRequest("GET", "/api/tasks/task1/clipboard", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	// 204 — route handler runs, no auth at this layer.
	testutil.Equal(t, w.Code, http.StatusNoContent)
}

func TestEncodeClipboardEvent(t *testing.T) {
	t.Run("present with text", func(t *testing.T) {
		got := encodeClipboardEvent("hi", true)
		var m map[string]any
		if err := json.Unmarshal([]byte(got), &m); err != nil {
			t.Fatal(err)
		}
		testutil.Equal(t, m["text"], "hi")
	})
	t.Run("absent renders cleared sentinel", func(t *testing.T) {
		got := encodeClipboardEvent("", false)
		testutil.Equal(t, got, `{"cleared":true}`)
	})
	t.Run("present but empty text still renders text key", func(t *testing.T) {
		// Edge case: caller asserts present=true with empty string.
		// Today's callers never do this (Set rejects empty taskID; subscriber
		// emits text="" only when present=false), but the encoder should
		// still emit the present-shape rather than collapsing to cleared.
		got := encodeClipboardEvent("", true)
		var m map[string]any
		if err := json.Unmarshal([]byte(got), &m); err != nil {
			t.Fatal(err)
		}
		if _, hasText := m["text"]; !hasText {
			t.Errorf("expected text key when present=true, got %s", got)
		}
	})
}
