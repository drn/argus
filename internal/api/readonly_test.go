package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestReadOnlyGet_Routes(t *testing.T) {
	srv, d := testServer(t)
	task := &model.Task{Name: "ro-task", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))

	t.Run("list tasks", func(t *testing.T) {
		res, err := srv.ReadOnlyGet(context.Background(), "/api/tasks")
		testutil.NoError(t, err)
		testutil.Equal(t, res.Status, http.StatusOK)
		testutil.Contains(t, string(res.Body), "ro-task")
	})

	t.Run("path forms are equivalent", func(t *testing.T) {
		for _, p := range []string{"tasks", "/tasks", "api/tasks", "/api/tasks"} {
			res, err := srv.ReadOnlyGet(context.Background(), p)
			testutil.NoError(t, err)
			testutil.Equal(t, res.Status, http.StatusOK)
			testutil.Contains(t, string(res.Body), "ro-task")
		}
	})

	t.Run("path parameter route", func(t *testing.T) {
		res, err := srv.ReadOnlyGet(context.Background(), "/api/tasks/"+task.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, res.Status, http.StatusOK)
		testutil.Contains(t, string(res.Body), "ro-task")
	})

	t.Run("query string passes through", func(t *testing.T) {
		res, err := srv.ReadOnlyGet(context.Background(), "/api/tasks?status=in_progress")
		testutil.NoError(t, err)
		testutil.Equal(t, res.Status, http.StatusOK)
	})

	t.Run("handler 404 is a result not an error", func(t *testing.T) {
		res, err := srv.ReadOnlyGet(context.Background(), "/api/tasks/does-not-exist")
		testutil.NoError(t, err)
		testutil.Equal(t, res.Status, http.StatusNotFound)
	})
}

func TestReadOnlyGet_UnroutableIs404(t *testing.T) {
	srv, _ := testServer(t)

	for _, p := range []string{
		"/api/no-such-route",
		"/api/tasks/x/stop",  // POST-only: GET must not reach the handler
		"/api/tasks/x/input", // POST-only
		"/api/hera/roles/1/nuke",
		"/vendor/xterm.js", // outside /api: normalised under /api, still no route
	} {
		res, err := srv.ReadOnlyGet(context.Background(), p)
		testutil.NoError(t, err)
		testutil.Equal(t, res.Status, http.StatusNotFound)
		if strings.Contains(string(res.Body), "<html") {
			t.Fatalf("%s: dashboard catch-all leaked: %s", p, res.Body)
		}
	}
}

func TestReadOnlyGet_NoMutationViaGet(t *testing.T) {
	srv, d := testServer(t)
	task := &model.Task{Name: "keep-me", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))

	// DELETE /api/tasks/{id} exists; a GET to the same path must read, not delete.
	res, err := srv.ReadOnlyGet(context.Background(), "/api/tasks/"+task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, res.Status, http.StatusOK)
	got, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.NotNil(t, got)
}

func TestReadOnlyGet_RejectsBadPaths(t *testing.T) {
	srv, _ := testServer(t)
	cases := map[string]string{
		"empty":             "",
		"blank":             "   ",
		"dotdot":            "/api/tasks/../tokens",
		"dotdot trailing":   "/api/..",
		"encoded dotdot":    "/api/tasks/%2e%2e/tokens",
		"encoded dotdot 2":  "/api/tasks/%2E%2e/tokens",
		"scheme":            "http://127.0.0.1:7743/api/tasks",
		"scheme in query":   "/api/tasks?next=http://evil",
		"protocol-relative": "//evil.example/api/tasks",
		"backslash":         `/api\tasks`,
		"encoded backslash": "/api/%5Ctasks",
		"fragment":          "/api/tasks#x",
		"newline":           "/api/tasks\n/api/tokens",
		"nul":               "/api/tasks\x00",
		"bad escape":        "/api/tasks/%zz",
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := srv.ReadOnlyGet(context.Background(), p)
			if err == nil {
				t.Fatalf("path %q was accepted", p)
			}
		})
	}
}

func TestReadOnlyGet_RefusesStreams(t *testing.T) {
	srv, _ := testServer(t)
	for _, p := range []string{"/api/events/stream", "/api/tasks/abc/stream"} {
		_, err := srv.ReadOnlyGet(context.Background(), p)
		if err == nil {
			t.Fatalf("stream route %s was dispatched", p)
		}
	}
}

func TestReadOnlyGet_MasterOnlyRefuses(t *testing.T) {
	srv, _ := testServer(t)
	res, err := srv.ReadOnlyGet(context.Background(), "/api/tokens")
	testutil.NoError(t, err)
	testutil.Equal(t, res.Status, http.StatusForbidden)
}

// injectRoute adds a GET route to the router ReadOnlyGet dispatches through.
func injectRoute(srv *Server, pattern string, h http.HandlerFunc) {
	mux := srv.routes()
	mux.HandleFunc(pattern, h)
	srv.roOnce.Do(func() { srv.roMux = mux })
}

func TestReadOnlyGet_RedactsToken(t *testing.T) {
	srv, _ := testServer(t)
	injectRoute(srv, "GET /api/test-echo", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"leak":"` + srv.token + `","again":"x-` + srv.token + `"}`))
	})
	res, err := srv.ReadOnlyGet(context.Background(), "/api/test-echo")
	testutil.NoError(t, err)
	if strings.Contains(string(res.Body), srv.token) {
		t.Fatalf("token leaked: %s", res.Body)
	}
	testutil.Contains(t, string(res.Body), "[redacted]")
}

func TestReadOnlyGet_SendsNoCredentialToHandlers(t *testing.T) {
	srv, _ := testServer(t)
	var gotAuth, gotOrigin, gotMethod string
	injectRoute(srv, "GET /api/test-hdr", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotOrigin = r.Header.Get("X-Argus-Auth")
		gotMethod = r.Method
		_, _ = w.Write([]byte("{}"))
	})
	_, err := srv.ReadOnlyGet(context.Background(), "/api/test-hdr")
	testutil.NoError(t, err)
	testutil.Equal(t, gotAuth, "")
	testutil.Equal(t, gotOrigin, "device")
	testutil.Equal(t, gotMethod, http.MethodGet)
}

func TestReadOnlyGet_TruncatesLargeBodies(t *testing.T) {
	srv, _ := testServer(t)
	injectRoute(srv, "GET /api/test-big", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("a", readOnlyMaxBody+100)))
	})
	res, err := srv.ReadOnlyGet(context.Background(), "/api/test-big")
	testutil.NoError(t, err)
	testutil.Equal(t, len(res.Body), readOnlyMaxBody)
	testutil.Equal(t, res.Truncated, true)
}

func TestReadOnlyGet_RecoversHandlerPanic(t *testing.T) {
	srv, _ := testServer(t)
	injectRoute(srv, "GET /api/test-panic", func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	_, err := srv.ReadOnlyGet(context.Background(), "/api/test-panic")
	if err == nil {
		t.Fatal("expected error from panicking handler")
	}
}
