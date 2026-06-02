package apiclient

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// allOK mounts a single catch-all handler that returns 200 + `body` for any
// request. Use it when the test only cares that the Client builds a valid
// request and decodes the response — not which specific URL was hit.
func allOK(t *testing.T, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, "tok", WithHTTPClient(srv.Client()))
}

func TestClient_WithTimeout(t *testing.T) {
	c := New("http://example", "t", WithTimeout(5*time.Second))
	testutil.Equal(t, c.HTTPClient().Timeout, 5*time.Second)
}

func TestClient_TokenAndHTTPClient_Accessors(t *testing.T) {
	hc := &http.Client{}
	c := New("http://example", "secret", WithHTTPClient(hc))
	testutil.Equal(t, c.Token(), "secret")
	if c.HTTPClient() != hc {
		t.Fatal("HTTPClient should return injected client")
	}
}

func TestError_String(t *testing.T) {
	e := &Error{Status: 500, Method: "GET", Path: "/x", Message: "boom"}
	testutil.Contains(t, e.Error(), "500")
	testutil.Contains(t, e.Error(), "boom")
	e2 := &Error{Status: 404, Method: "GET", Path: "/x"}
	testutil.Contains(t, e2.Error(), "404")
}

// --- tasks.go ---

func TestClient_StopResumeArchiveUnarchiveDelete(t *testing.T) {
	hit := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit[r.Method+" "+r.URL.Path]++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","pid":1}`))
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, "t", WithHTTPClient(srv.Client()))

	testutil.NoError(t, c.StopTask(t.Context(), "t1"))
	_, err := c.ResumeTask(t.Context(), "t1")
	testutil.NoError(t, err)
	testutil.NoError(t, c.DeleteTask(t.Context(), "t1"))
	testutil.NoError(t, c.ArchiveTask(t.Context(), "t1"))
	testutil.NoError(t, c.UnarchiveTask(t.Context(), "t1"))
	testutil.NoError(t, c.SetStatus(t.Context(), "t1", "in_review"))
	testutil.NoError(t, c.SetPlanSlug(t.Context(), "t1", "slug"))
	testutil.NoError(t, c.LinkTask(t.Context(), "child", "parent"))
	testutil.NoError(t, c.UnlinkTask(t.Context(), "child", "parent"))
	_, err = c.HaltDownstream(t.Context(), "t1")
	testutil.NoError(t, err)
	_, err = c.ForkTask(t.Context(), "t1", ForkReq{Name: "fork"})
	testutil.NoError(t, err)
	_, err = c.StopAll(t.Context())
	testutil.NoError(t, err)
	_, err = c.PruneCompleted(t.Context())
	testutil.NoError(t, err)
	_, err = c.GetDAG(t.Context(), DAGFilter{Project: "p", PlanSlug: "s", IncludeArchived: true})
	testutil.NoError(t, err)
	_, err = c.GetDeps(t.Context(), "t1")
	testutil.NoError(t, err)
}

func TestClient_AddTaskRaw_MutatesCallerStruct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"server-id","name":"echoed","project":"p","branch":"","prompt":"","status":"pending","created_at":"2026-05-22T00:00:00Z"}`))
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, "t", WithHTTPClient(srv.Client()))

	task := &model.Task{Name: "echoed", Project: "p"}
	testutil.NoError(t, c.AddTaskRaw(t.Context(), task))
	testutil.Equal(t, task.ID, "server-id")
}

func TestClient_UpdateTaskRaw_GetTaskRaw_GetScheduleRaw_ListTasksRaw(t *testing.T) {
	c := allOK(t, `{"id":"t1","tasks":[]}`)
	testutil.NoError(t, c.UpdateTaskRaw(t.Context(), &model.Task{ID: "t1"}))
	_, err := c.GetTaskRaw(t.Context(), "t1")
	testutil.NoError(t, err)
	_, err = c.GetScheduleRaw(t.Context(), "s1")
	testutil.NoError(t, err)
	_, err = c.ListTasksRaw(t.Context())
	testutil.NoError(t, err)
}

// --- projects.go ---

func TestClient_Projects(t *testing.T) {
	c := allOK(t, `{"projects":[{"name":"p","path":"/tmp"}],"backends":[{"name":"b","command":"c"}]}`)
	_, err := c.ListProjectsFull(t.Context())
	testutil.NoError(t, err)
	testutil.NoError(t, c.CreateProject(t.Context(), ProjectJSON{Name: "p"}))
	testutil.NoError(t, c.UpdateProject(t.Context(), "p", ProjectJSON{Name: "p"}))
	testutil.NoError(t, c.DeleteProject(t.Context(), "p"))
	_, err = c.ListBackends(t.Context())
	testutil.NoError(t, err)
	testutil.NoError(t, c.CreateBackend(t.Context(), BackendJSON{Name: "b"}))
	testutil.NoError(t, c.UpdateBackend(t.Context(), "b", BackendJSON{Name: "b"}))
	testutil.NoError(t, c.DeleteBackend(t.Context(), "b"))
}

// --- terminal.go ---

func TestClient_TerminalEndpoints(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tasks/t1/output" {
			w.Header().Set("X-Output-Total", "100")
			w.Header().Set("X-Source", "log")
			_, _ = w.Write([]byte("hello"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"cols":80,"rows":24,"rerendered":false}`))
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, "t", WithHTTPClient(srv.Client()))

	out, err := c.GetOutput(t.Context(), "t1", 1024, true)
	testutil.NoError(t, err)
	testutil.Equal(t, string(out.Data), "hello")
	testutil.Equal(t, out.OutputTotal, uint64(100))

	_, err = c.GetSize(t.Context(), "t1")
	testutil.NoError(t, err)
	_, err = c.Resize(t.Context(), "t1", 24, 80)
	testutil.NoError(t, err)
}

// --- git.go ---

func TestClient_GitEndpoints(t *testing.T) {
	c := allOK(t, `{"links":[],"branch":"main"}`)
	_, err := c.GitStatus(t.Context(), "t1")
	testutil.NoError(t, err)
	_, err = c.GitDiff(t.Context(), "t1", "foo.go")
	testutil.NoError(t, err)
	_, err = c.FileTree(t.Context(), "t1", "")
	testutil.NoError(t, err)
	_, err = c.GetClipboard(t.Context(), "t1")
	testutil.NoError(t, err)
	testutil.NoError(t, c.SetClipboard(t.Context(), "t1", "txt"))
	testutil.NoError(t, c.ClearClipboard(t.Context(), "t1"))
	_, err = c.GetLinks(t.Context(), "t1")
	testutil.NoError(t, err)
}

// --- messages.go ---

func TestClient_MessagesEndpoints(t *testing.T) {
	c := allOK(t, `{"messages":[],"unread_count":0,"id":"m1","created_at":"2026-05-22T00:00:00Z","acked":0}`)
	_, err := c.ListInbox(t.Context(), "t1", InboxFilter{UnreadOnly: false, Sender: "x", Since: "2026-05-22T00:00:00Z", Limit: 10})
	testutil.NoError(t, err)
	_, err = c.SendMessage(t.Context(), "t1", SendMessageReq{To: "t2", Body: "hi", Kind: "note"})
	testutil.NoError(t, err)
	_, err = c.AckInbox(t.Context(), "t1", []string{"m1"})
	testutil.NoError(t, err)
}

// --- schedules.go ---

func TestClient_ScheduleEndpoints(t *testing.T) {
	c := allOK(t, `{"schedules":[],"id":"s1","task_id":"t1"}`)
	_, err := c.ListSchedules(t.Context())
	testutil.NoError(t, err)
	_, err = c.CreateSchedule(t.Context(), ScheduleReq{})
	testutil.NoError(t, err)
	_, err = c.UpdateSchedule(t.Context(), "s1", ScheduleReq{})
	testutil.NoError(t, err)
	testutil.NoError(t, c.DeleteSchedule(t.Context(), "s1"))
	_, err = c.RunSchedule(t.Context(), "s1")
	testutil.NoError(t, err)
}

// --- settings.go ---

func TestClient_SettingsEndpoints(t *testing.T) {
	c := allOK(t, `{"sandbox":{"enabled":false,"available":true,"deny_read":[],"extra_write":[],"allow_apple_events":[]},"kb":{"enabled":false,"metis_vault_path":""},"api":{"enabled":false,"http_port":0},"defaults":{"backend":""},"skills":[],"path":"/x","ok":true,"output":"","tokens":[],"token":"new","label":"l","id":"id"}`)
	_, err := c.GetSettings(t.Context())
	testutil.NoError(t, err)
	_, err = c.UpdateSettings(t.Context(), SettingsUpdate{})
	testutil.NoError(t, err)
	_, err = c.ListSkills(t.Context(), "p", "f")
	testutil.NoError(t, err)
	_, err = c.GetSourcePath(t.Context())
	testutil.NoError(t, err)
	testutil.NoError(t, c.SetSourcePath(t.Context(), "/x"))
	_, err = c.UpdateSelf(t.Context())
	testutil.NoError(t, err)
	_, err = c.ListTokens(t.Context())
	testutil.NoError(t, err)
	_, err = c.CreateToken(t.Context(), "label")
	testutil.NoError(t, err)
	testutil.NoError(t, c.RevokeToken(t.Context(), "id"))
	_, err = c.GetConfig(t.Context())
	testutil.NoError(t, err)
	_, err = c.GetSessionState(t.Context())
	testutil.NoError(t, err)
}

func TestClient_GetLog(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("log line\n"))
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, "t", WithHTTPClient(srv.Client()))
	data, err := c.GetLog(t.Context(), "ux", 4096)
	testutil.NoError(t, err)
	if !strings.HasPrefix(string(data), "log line") {
		t.Fatalf("unexpected log body: %q", string(data))
	}
}

func TestClient_HasPendingRestart(t *testing.T) {
	c := allOK(t, `{"pending":true}`)
	got, err := c.HasPendingRestart(t.Context(), "t1")
	testutil.NoError(t, err)
	testutil.True(t, got)
}

func TestClient_ListPluginSections(t *testing.T) {
	c := allOK(t, `{"sections":[{"scope":"a","title":"Hello","type":"form","callback_url":"http://x","fields":[{"key":"k","label":"L","type":"bool"}]}]}`)
	got, err := c.ListPluginSections(t.Context())
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 1)
	testutil.Equal(t, got[0].Scope, "a")
	testutil.Equal(t, got[0].Title, "Hello")
	testutil.Equal(t, len(got[0].Fields), 1)
}

// --- provider.go: zero-coverage methods ---

func TestSession_LocalAccessors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/sessions/state":
			_, _ = w.Write([]byte(`{"running":["t1"],"idle":["t1"]}`))
		case "/api/tasks/t1":
			_, _ = w.Write([]byte(`{"id":"t1","name":"alpha","status":"in_progress","project":"p","created_at":"2026-05-22T00:00:00Z","worktree_path":"/wt/t1"}`))
		case "/api/tasks/t1/size":
			_, _ = w.Write([]byte(`{"cols":80,"rows":24}`))
		case "/api/tasks/t1/stop":
			_, _ = w.Write([]byte(`{"status":"stopped"}`))
		}
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, "t", WithHTTPClient(srv.Client()))
	p := NewProvider(c)
	defer p.Close()
	s := p.getOrCreateSession("t1")
	defer s.close()

	testutil.Equal(t, s.PID(), 0)
	testutil.Equal(t, len(s.RecentOutputTail(100)), 0)
	tail, total := s.RecentOutputTailWithTotal(100)
	testutil.Equal(t, len(tail), 0)
	testutil.Equal(t, total, uint64(0))
	testutil.True(t, s.IsIdle())
	testutil.True(t, s.Alive())
	c0, r0 := s.PTYSize()
	testutil.Equal(t, c0, 80)
	testutil.Equal(t, r0, 24)
	// InitialPTYSize falls back to PTYSize when init values are zero.
	ic, ir := s.InitialPTYSize()
	testutil.Equal(t, ic, 80)
	testutil.Equal(t, ir, 24)
	if s.Done() == nil {
		t.Fatal("Done() should return a channel")
	}
	testutil.NoError(t, s.Err())
	testutil.Equal(t, s.WorkDir(), "/wt/t1")
	// Second WorkDir call hits the cache.
	testutil.Equal(t, s.WorkDir(), "/wt/t1")
	testutil.NoError(t, s.Stop())
	// AddWriter et al. are no-ops; just call them for coverage.
	s.AddWriter(nil)
	s.AddWriterFrom(nil, 0)
	s.AddWriterFromTolerant(nil, 0)
	s.RemoveWriter(nil)
}

func TestSession_RunStream_ExitEventClosesSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tasks/t1/stream" {
			w.Header().Set("Content-Type", "text/event-stream")
			fl, ok := w.(http.Flusher)
			if !ok {
				return
			}
			// One ignored keepalive, one clipboard event (also ignored), then exit.
			_, _ = w.Write([]byte(": ping\n\n"))
			_, _ = w.Write([]byte("event: clipboard\ndata: {}\n\n"))
			_, _ = w.Write([]byte("event: exit\ndata: {}\n\n"))
			fl.Flush()
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"running":["t1"],"idle":[]}`))
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, "t", WithHTTPClient(srv.Client()))
	p := NewProvider(c)
	defer p.Close()
	exited := make(chan SessionExitInfo, 1)
	p.OnSessionExit(func(taskID string, info SessionExitInfo) { exited <- info })
	s := p.getOrCreateSession("t1")
	select {
	case <-exited:
		// good — event:exit fired the callback
	case <-time.After(2 * time.Second):
		t.Fatal("OnSessionExit never fired after server `event: exit`")
	}
	select {
	case <-s.Done():
		// done channel closed as expected
	case <-time.After(time.Second):
		t.Fatal("Session.Done not closed after exit")
	}
}

func TestErrString(t *testing.T) {
	testutil.Equal(t, errString(nil), "")
	testutil.Contains(t, errString(errorsNewLocal("boom")), "boom")
}

type plainErr struct{ msg string }

func (e *plainErr) Error() string { return e.msg }

func errorsNewLocal(msg string) error { return &plainErr{msg} }

func TestProvider_GetIdleRunningStopAll(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/sessions/state":
			_, _ = w.Write([]byte(`{"running":["t1"],"idle":["t1"]}`))
		case "/api/sessions/stop-all":
			_, _ = w.Write([]byte(`{"stopped":1}`))
		case "/api/sessions/t1/pending-restart":
			_, _ = w.Write([]byte(`{"pending":true}`))
		case "/api/tasks/t1":
			_, _ = w.Write([]byte(`{"id":"t1","name":"alpha","status":"in_progress","project":"p","created_at":"2026-05-22T00:00:00Z","worktree_path":"/tmp/t1"}`))
		}
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, "t", WithHTTPClient(srv.Client()))
	p := NewProvider(c)
	defer p.Close()

	p.OnSessionExit(func(taskID string, info SessionExitInfo) {})
	testutil.DeepEqual(t, p.Running(), []string{"t1"})
	testutil.DeepEqual(t, p.Idle(), []string{"t1"})
	p.StopAll()
	if p.Get("t1") == nil {
		t.Fatal("Get should return handle for running task")
	}
	testutil.True(t, p.HasPendingRestart("t1"))
	testutil.Equal(t, p.WorkDir("t1"), "/tmp/t1")
}
