package apiclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// fixture is a tiny httptest.Server with a couple of canned routes that
// every test can dial. Each test registers handlers via the fixture's mux
// inside t.Run subtests so the routes stay focused per case.
type fixture struct {
	mux *http.ServeMux
	srv *httptest.Server
	c   *Client
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := New(srv.URL, "test-token", WithHTTPClient(srv.Client()))
	return &fixture{mux: mux, srv: srv, c: c}
}

// requireAuth wraps a handler with the same bearer-token check the real API
// uses, so tests can assert that the Client sends the Authorization header.
func requireAuth(t *testing.T, next http.HandlerFunc) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func TestClient_BaseURLNormalised(t *testing.T) {
	c := New("http://example.com/", "tok")
	testutil.Equal(t, c.BaseURL(), "http://example.com")
}

func TestClient_Status(t *testing.T) {
	f := newFixture(t)
	f.mux.HandleFunc("/api/status", requireAuth(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"sessions":{"running":2,"idle":1},"tasks":{"pending":3,"in_progress":2,"in_review":1,"complete":4}}`))
	}))

	got, err := f.c.Status(context.Background())
	testutil.NoError(t, err)
	testutil.True(t, got.OK)
	testutil.Equal(t, got.Sessions.Running, 2)
	testutil.Equal(t, got.Sessions.Idle, 1)
	testutil.Equal(t, got.Tasks.Pending, 3)
}

func TestClient_ListTasks_WithFilters(t *testing.T) {
	f := newFixture(t)
	var lastURL string
	f.mux.HandleFunc("/api/tasks", requireAuth(t, func(w http.ResponseWriter, r *http.Request) {
		lastURL = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tasks":[{"id":"t1","name":"alpha","status":"in_progress","project":"proj1","created_at":"2026-05-22T00:00:00Z"}]}`))
	}))

	tasks, err := f.c.ListTasks(context.Background(), ListTasksFilter{Status: "in_progress", Project: "proj1"})
	testutil.NoError(t, err)
	testutil.Equal(t, len(tasks), 1)
	testutil.Equal(t, tasks[0].ID, "t1")
	testutil.Contains(t, lastURL, "status=in_progress")
	testutil.Contains(t, lastURL, "project=proj1")
}

func TestClient_GetTask_NotFound(t *testing.T) {
	f := newFixture(t)
	f.mux.HandleFunc("/api/tasks/missing", requireAuth(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"task not found"}`, http.StatusNotFound)
	}))

	_, err := f.c.GetTask(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !IsNotFound(err) {
		t.Fatalf("expected IsNotFound true, got %v", err)
	}
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *Error, got %T", err)
	}
	testutil.Equal(t, apiErr.Status, http.StatusNotFound)
	testutil.Equal(t, apiErr.Message, "task not found")
}

func TestClient_Unauthorized(t *testing.T) {
	f := newFixture(t)
	f.mux.HandleFunc("/api/status", requireAuth(t, func(w http.ResponseWriter, r *http.Request) {
		// requireAuth handles the 401
	}))

	bad := New(f.srv.URL, "wrong-token", WithHTTPClient(f.srv.Client()))
	_, err := bad.Status(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsUnauthorized(err) {
		t.Fatalf("expected IsUnauthorized true, got %v", err)
	}
}

func TestClient_CreateTask_RoundTrip(t *testing.T) {
	f := newFixture(t)
	f.mux.HandleFunc("/api/tasks", requireAuth(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "wrong method", http.StatusMethodNotAllowed)
			return
		}
		var req CreateTaskReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(CreateTaskResp{ID: "new", Name: req.Name, Status: "in_progress"})
	}))

	resp, err := f.c.CreateTask(context.Background(), CreateTaskReq{Name: "foo", Prompt: "bar", Project: "p"})
	testutil.NoError(t, err)
	testutil.Equal(t, resp.ID, "new")
	testutil.Equal(t, resp.Name, "foo")
}

func TestClient_WriteInput(t *testing.T) {
	f := newFixture(t)
	var got []byte
	f.mux.HandleFunc("/api/tasks/t1/input", requireAuth(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","bytes":"3"}`))
	}))

	err := f.c.WriteInput(context.Background(), "t1", []byte("hey"))
	testutil.NoError(t, err)
	testutil.Equal(t, string(got), "hey")
}

func TestClient_GetOutput_ParsesHeaders(t *testing.T) {
	f := newFixture(t)
	f.mux.HandleFunc("/api/tasks/t1/output", requireAuth(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Output-Total", "12345")
		w.Header().Set("X-Source", "log")
		_, _ = w.Write([]byte("hello"))
	}))

	res, err := f.c.GetOutput(context.Background(), "t1", 0, false)
	testutil.NoError(t, err)
	testutil.Equal(t, string(res.Data), "hello")
	testutil.Equal(t, res.OutputTotal, uint64(12345))
	testutil.Equal(t, res.Source, "log")
}

func TestClient_StreamOutput(t *testing.T) {
	f := newFixture(t)
	f.mux.HandleFunc("/api/tasks/t1/stream", requireAuth(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flush", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("data: aGVsbG8=\n\n"))
		flusher.Flush()
	}))

	resp, err := f.c.StreamOutput(t.Context(), "t1", 0)
	testutil.NoError(t, err)
	defer resp.Body.Close()

	buf := make([]byte, 128)
	n, _ := resp.Body.Read(buf)
	if !strings.Contains(string(buf[:n]), "aGVsbG8=") {
		t.Fatalf("expected base64 payload in stream body, got %q", string(buf[:n]))
	}
}

func TestClient_ListProjects(t *testing.T) {
	f := newFixture(t)
	f.mux.HandleFunc("/api/projects", requireAuth(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"projects":["alpha","beta"]}`))
	}))

	names, err := f.c.ListProjects(context.Background())
	testutil.NoError(t, err)
	testutil.DeepEqual(t, names, []string{"alpha", "beta"})
}

func TestClient_RenameTask(t *testing.T) {
	f := newFixture(t)
	var seenBody string
	f.mux.HandleFunc("/api/tasks/t1/rename", requireAuth(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seenBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"new-name"}`))
	}))

	err := f.c.RenameTask(context.Background(), "t1", "new-name")
	testutil.NoError(t, err)
	testutil.Contains(t, seenBody, `"name":"new-name"`)
}

func TestClient_ErrorPaths(t *testing.T) {
	// A server that 500s every route exercises the error-return arm of each
	// Client method uniformly — the happy paths are covered by the dedicated
	// tests above, but the `if err != nil { return ..., err }` branches were
	// previously uncovered.
	f := newFixture(t)
	f.mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
	})
	ctx := context.Background()
	c := f.c

	checks := []struct {
		name string
		call func() error
	}{
		{"GitStatus", func() error { _, e := c.GitStatus(ctx, "t1"); return e }},
		{"GitDiff", func() error { _, e := c.GitDiff(ctx, "t1", "f"); return e }},
		{"FileTree", func() error { _, e := c.FileTree(ctx, "t1", ""); return e }},
		{"GetClipboard", func() error { _, e := c.GetClipboard(ctx, "t1"); return e }},
		{"GetLinks", func() error { _, e := c.GetLinks(ctx, "t1"); return e }},
		{"ListProjects", func() error { _, e := c.ListProjects(ctx); return e }},
		{"ListProjectsFull", func() error { _, e := c.ListProjectsFull(ctx); return e }},
		{"ListBackends", func() error { _, e := c.ListBackends(ctx); return e }},
		{"ListInbox", func() error { _, e := c.ListInbox(ctx, "t1", InboxFilter{}); return e }},
		{"SendMessage", func() error { _, e := c.SendMessage(ctx, "t1", SendMessageReq{To: "x", Body: "y"}); return e }},
		{"AckInbox", func() error { _, e := c.AckInbox(ctx, "t1", []string{"m1"}); return e }},
		{"ListSchedules", func() error { _, e := c.ListSchedules(ctx); return e }},
		{"CreateSchedule", func() error { _, e := c.CreateSchedule(ctx, ScheduleReq{}); return e }},
		{"RunSchedule", func() error { _, e := c.RunSchedule(ctx, "s1"); return e }},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestQueryHelper(t *testing.T) {
	t.Run("returns empty when all values empty", func(t *testing.T) {
		testutil.Equal(t, query("a", "", "b", ""), "")
	})
	t.Run("skips empty values", func(t *testing.T) {
		got := query("a", "x", "b", "")
		testutil.Equal(t, got, "?a=x")
	})
	t.Run("encodes special chars", func(t *testing.T) {
		got := query("name", "hello world")
		testutil.Contains(t, got, "name=hello+world")
	})
	t.Run("panics on odd count", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic on odd args")
			}
		}()
		// Build via variadic slice so staticcheck SA5012 doesn't flag the
		// literal odd-count call as a programming error — the panic IS
		// the contract under test.
		odd := []string{"a"}
		_ = query(odd...) //nolint:staticcheck // SA5012: the odd-count panic IS the contract under test
	})
}
