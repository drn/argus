package apiclient

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// fakeServer is a tiny stub for Provider/Session tests. It exposes the
// minimum routes the SessionProvider exercises without pulling the whole
// internal/api package in.
type fakeServer struct {
	mu      sync.Mutex
	tasks   map[string]TaskJSON
	running []string
	idle    []string
	srv     *httptest.Server
	mux     *http.ServeMux
	t       *testing.T

	// streamLines is the canned set of SSE events streamOnce should emit.
	streamLines []string

	// lastResizeConn / lastReleaseConn record the conn ID the most recent
	// /resize and /viewer/release request carried, for viewer-registry tests.
	lastResizeConn  string
	lastReleaseConn string
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	f := &fakeServer{
		tasks: make(map[string]TaskJSON),
		mux:   mux,
		srv:   srv,
		t:     t,
	}
	f.routes()
	return f
}

func (f *fakeServer) addTask(id string, t TaskJSON) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tasks[id] = t
}

func (f *fakeServer) markRunning(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.running = append(f.running, id)
}

func (f *fakeServer) markIdle(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.idle = append(f.idle, id)
}

func (f *fakeServer) client() *Client {
	return New(f.srv.URL, "tok", WithHTTPClient(f.srv.Client()))
}

func (f *fakeServer) routes() {
	f.mux.HandleFunc("/api/sessions/state", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		// Avoid nil running list — caller of GetSessionState relies on a slice.
		running := f.running
		if running == nil {
			running = []string{}
		}
		idle := f.idle
		if idle == nil {
			idle = []string{}
		}
		_, _ = w.Write([]byte(`{"running":` + jsonStringArr(running) + `,"idle":` + jsonStringArr(idle) + `}`))
	})
	f.mux.HandleFunc("/api/tasks/", func(w http.ResponseWriter, r *http.Request) {
		// /api/tasks/{id}, /resume, /stop, /input, /resize, /size, /stream
		path := r.URL.Path
		// Strip the prefix.
		rest := path[len("/api/tasks/"):]
		// Split by first slash to get id and action.
		id := rest
		action := ""
		for i := 0; i < len(rest); i++ {
			if rest[i] == '/' {
				id = rest[:i]
				action = rest[i+1:]
				break
			}
		}
		switch action {
		case "":
			// GET single task
			f.mu.Lock()
			t, ok := f.tasks[id]
			f.mu.Unlock()
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"` + t.ID + `","name":"` + t.Name + `","status":"` + t.Status + `","project":"` + t.Project + `","created_at":"` + t.CreatedAt + `","worktree_path":"` + t.WorktreePath + `"}`))
		case "resume":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"resumed","pid":4242}`))
		case "stop":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"stopped"}`))
		case "input":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "size":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"cols":120,"rows":40}`))
		case "resize":
			var body struct {
				Conn string `json:"conn"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.lastResizeConn = body.Conn
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"cols":120,"rows":40,"rerendered":false}`))
		case "viewer/release":
			f.mu.Lock()
			f.lastReleaseConn = r.URL.Query().Get("conn")
			f.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case "stream":
			f.handleStream(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

func (f *fakeServer) handleStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "no flush", http.StatusInternalServerError)
		return
	}
	f.mu.Lock()
	lines := append([]string(nil), f.streamLines...)
	f.mu.Unlock()
	for _, l := range lines {
		_, _ = w.Write([]byte(l))
		flusher.Flush()
	}
	// Don't auto-close; tests rely on context cancel for clean shutdown.
	<-r.Context().Done()
}

func jsonStringArr(s []string) string {
	out := "["
	for i, v := range s {
		if i > 0 {
			out += ","
		}
		out += `"` + v + `"`
	}
	out += "]"
	return out
}

func TestProvider_RunningAndIdle(t *testing.T) {
	fs := newFakeServer(t)
	fs.markRunning("alpha")
	fs.markRunning("beta")
	fs.markIdle("alpha")

	p := NewProvider(fs.client())
	running, idle := p.RunningAndIdle()
	testutil.DeepEqual(t, running, []string{"alpha", "beta"})
	testutil.DeepEqual(t, idle, []string{"alpha"})
	testutil.True(t, p.HasSession("alpha"))
	testutil.False(t, p.HasSession("missing"))
}

func TestProvider_WorkDir(t *testing.T) {
	fs := newFakeServer(t)
	fs.addTask("t1", TaskJSON{ID: "t1", Name: "t1", Status: "in_progress", Project: "p", CreatedAt: "2026-05-22T00:00:00Z", WorktreePath: "/tmp/wt"})

	p := NewProvider(fs.client())
	testutil.Equal(t, p.WorkDir("t1"), "/tmp/wt")
}

func TestProvider_Stop(t *testing.T) {
	fs := newFakeServer(t)
	p := NewProvider(fs.client())
	testutil.NoError(t, p.Stop("t1"))
}

func TestProvider_ErrorPaths(t *testing.T) {
	// Every route 500s, so each accessor must fall back to its zero value via
	// the (previously uncovered) error branch rather than panicking.
	// newFixture is defined in client_test.go — both files are package apiclient.
	f := newFixture(t)
	f.mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
	})
	p := NewProvider(f.c)

	testutil.Equal(t, len(p.Running()), 0)
	testutil.Equal(t, len(p.Idle()), 0)
	running, idle := p.RunningAndIdle()
	testutil.Equal(t, len(running), 0)
	testutil.Equal(t, len(idle), 0)
	testutil.False(t, p.HasSession("x"))
	testutil.Equal(t, p.WorkDir("x"), "")
	testutil.False(t, p.HasPendingRestart("x"))
}

func TestSession_WriteInput_UpdatesLastInput(t *testing.T) {
	fs := newFakeServer(t)
	p := NewProvider(fs.client())
	s := p.getOrCreateSession("t1")
	defer s.close()

	before := time.Now()
	_, err := s.WriteInput([]byte("hello"))
	testutil.NoError(t, err)
	got := s.LastInput()
	if got.Before(before) {
		t.Fatalf("LastInput should be >= before: got %v want >= %v", got, before)
	}
}

func TestSession_Resize_UpdatesCachedSize(t *testing.T) {
	fs := newFakeServer(t)
	p := NewProvider(fs.client())
	s := p.getOrCreateSession("t1")
	defer s.close()

	testutil.NoError(t, s.Resize(40, 120))
	cols, rows := s.PTYSize()
	testutil.Equal(t, cols, 120)
	testutil.Equal(t, rows, 40)
}

// TestSession_ViewerRegistry asserts a --remote TUI participates in the
// server's active-viewer registry: SetViewerSize bridges to /resize carrying
// the stable conn ID (and caches the effective size the server reports), and
// RemoveViewer drops the claim via /viewer/release?conn=.
func TestSession_ViewerRegistry(t *testing.T) {
	fs := newFakeServer(t)
	p := NewProvider(fs.client())
	s := p.getOrCreateSession("t1")
	defer s.close()

	s.SetViewerSize("remote-1", 120, 40)
	s.mu.Lock()
	cols, rows := s.cols, s.rows
	s.mu.Unlock()
	testutil.Equal(t, cols, 120) // effective size cached from the resize response
	testutil.Equal(t, rows, 40)
	fs.mu.Lock()
	resizeConn := fs.lastResizeConn
	fs.mu.Unlock()
	testutil.Equal(t, resizeConn, "remote-1")

	s.RemoveViewer("remote-1")
	fs.mu.Lock()
	releaseConn := fs.lastReleaseConn
	fs.mu.Unlock()
	testutil.Equal(t, releaseConn, "remote-1")

	// Empty viewer ID is a no-op on both paths (no panic, no request).
	s.SetViewerSize("", 80, 24)
	s.RemoveViewer("")
}

func TestSession_RingBufferReceivesStream(t *testing.T) {
	fs := newFakeServer(t)
	payload := base64.StdEncoding.EncodeToString([]byte("hi there"))
	fs.streamLines = []string{"data: " + payload + "\n\n"}

	p := NewProvider(fs.client())
	defer p.Close()
	s := p.getOrCreateSession("t1")
	defer s.close()

	// Allow the stream goroutine to consume the canned line.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.TotalWritten() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if s.TotalWritten() == 0 {
		t.Fatal("ring buffer never received stream bytes")
	}
	got := string(s.RecentOutput())
	testutil.Contains(t, got, "hi there")
}

func TestSession_SatisfiesInterface(t *testing.T) {
	// Compile-time assertion already exists; this test ensures the unused-
	// import elimination doesn't quietly remove it.
	fs := newFakeServer(t)
	p := NewProvider(fs.client())
	s := p.getOrCreateSession("t1")
	defer s.close()
	_ = s.AddWriter
	_ = s.AddWriterFrom
	_ = s.AddWriterFromTolerant
	_ = s.RemoveWriter
}

// TestSession_CloseConcurrent pins BUG-009: the runStream goroutine and
// Provider.Close both tear a session down and both call close(), so the
// done-channel close must be guarded against a double close. The prior
// check-then-act select panicked ("close of closed channel") when two
// callers observed done still open. Hammering close() from many goroutines
// reproduces that window; with the sync.Once guard it stays panic- and
// race-clean and Done() ends up closed exactly once.
func TestSession_CloseConcurrent(t *testing.T) {
	fs := newFakeServer(t)
	p := NewProvider(fs.client())
	s := newSession("t1", p)

	const closers = 64
	var wg sync.WaitGroup
	wg.Add(closers)
	for i := 0; i < closers; i++ {
		go func() {
			defer wg.Done()
			s.close()
		}()
	}
	wg.Wait()

	select {
	case <-s.Done():
	default:
		t.Fatal("Done() should be closed after close()")
	}
}

func TestProvider_Start_Resume(t *testing.T) {
	fs := newFakeServer(t)
	fs.addTask("t1", TaskJSON{ID: "t1", Status: "in_progress"})
	p := NewProvider(fs.client())
	defer p.Close()

	h, err := p.Start(&model.Task{ID: "t1", SessionID: "sess"}, config.Config{}, 24, 80, true)
	testutil.NoError(t, err)
	testutil.True(t, h != nil)
}

// TestProvider_Clipboard exercises the clipboardAccessor methods that make
// ctrl+y copy work in --remote mode. The mock server mirrors the real API's
// contract: GET returns 204 (no body) when nothing is staged and 200 with
// {"text":...} when a payload exists; DELETE clears. A dedicated mux is used
// instead of the shared fakeServer because fakeServer's catch-all
// /api/tasks/ handler has no clipboard route and the 204-vs-200 presence
// semantics are specific to this test.
//
// Server-mutated state (present, cleared) is guarded by mu because the
// httptest handler runs on its own goroutine; without it the -race detector
// can flag handler writes against test-goroutine reads. The subtests are
// intentionally order-dependent: "clear" must run before "get absent after
// clear" (it flips present=false server-side). t.Run subtests run
// sequentially, so this holds — do not add t.Parallel().
func TestProvider_Clipboard(t *testing.T) {
	var mu sync.Mutex
	cleared := []string{}
	staged := "staged text"
	present := true
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tasks/t1/clipboard", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			if !present {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"text":"` + staged + `"}`))
		case http.MethodDelete:
			cleared = append(cleared, "t1")
			present = false
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	p := NewProvider(New(srv.URL, "tok", WithHTTPClient(srv.Client())))
	defer p.Close()

	t.Run("get present", func(t *testing.T) {
		text, ok := p.ClipboardGet("t1")
		testutil.Equal(t, text, "staged text")
		testutil.Equal(t, ok, true)
	})

	t.Run("clear", func(t *testing.T) {
		testutil.NoError(t, p.ClipboardClear("t1"))
		mu.Lock()
		defer mu.Unlock()
		testutil.DeepEqual(t, cleared, []string{"t1"})
	})

	t.Run("get absent after clear", func(t *testing.T) {
		text, ok := p.ClipboardGet("t1")
		testutil.Equal(t, text, "")
		testutil.Equal(t, ok, false)
	})

	t.Run("get error returns absent", func(t *testing.T) {
		// Unknown task → server 404 → request error → absent.
		text, ok := p.ClipboardGet("nope")
		testutil.Equal(t, text, "")
		testutil.Equal(t, ok, false)
	})
}
