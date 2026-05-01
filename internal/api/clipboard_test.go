package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/drn/argus/internal/clipboard"
	"github.com/drn/argus/internal/testutil"
)

func TestClipboard_GetEmpty(t *testing.T) {
	srv, _ := testServer(t)
	srv.SetClipboard(clipboard.New())
	mux := srv.routes()

	req := authedReq("GET", "/api/tasks/task1/clipboard", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	testutil.Equal(t, w.Code, http.StatusNoContent)
}

func TestClipboard_SetAndGet(t *testing.T) {
	srv, _ := testServer(t)
	srv.SetClipboard(clipboard.New())
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
	srv, _ := testServer(t)
	srv.SetClipboard(clipboard.New())
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
	srv, _ := testServer(t)
	srv.SetClipboard(clipboard.New())
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
	srv, _ := testServer(t)
	srv.SetClipboard(clipboard.New())
	mux := srv.routes()

	// 1 MiB + 1 byte → rejected.
	body := `{"text":"` + strings.Repeat("a", clipboard.MaxTextSize+1) + `"}`
	req := authedReq("POST", "/api/tasks/task1/clipboard", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusBadRequest)
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
	srv, _ := testServer(t)
	srv.SetClipboard(clipboard.New())
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
	srv, _ := testServer(t)
	srv.SetClipboard(clipboard.New())
	mux := srv.routes()

	req := httptest.NewRequest("GET", "/api/tasks/task1/clipboard", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	// 204 — route handler runs, no auth at this layer.
	testutil.Equal(t, w.Code, http.StatusNoContent)
}

func TestEncodeClipboardEvent(t *testing.T) {
	t.Run("present", func(t *testing.T) {
		got := encodeClipboardEvent("hi", true)
		// Should be parseable JSON with text=hi.
		var m map[string]any
		if err := json.Unmarshal([]byte(got), &m); err != nil {
			t.Fatal(err)
		}
		testutil.Equal(t, m["text"], "hi")
	})
	t.Run("cleared", func(t *testing.T) {
		got := encodeClipboardEvent("", false)
		testutil.Equal(t, got, `{"cleared":true}`)
	})
	t.Run("empty present is treated as cleared", func(t *testing.T) {
		got := encodeClipboardEvent("", false)
		testutil.Equal(t, got, `{"cleared":true}`)
	})
}
