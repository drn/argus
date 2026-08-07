package apistore

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/drn/argus/internal/apiclient"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// fakeAPI is the smallest possible REST stub Store_test needs. Each test
// registers handlers via the mux before exercising the Store methods.
type fakeAPI struct {
	srv *httptest.Server
	mux *http.ServeMux

	cannedTasks    []*model.Task
	configResponse map[string]any
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &fakeAPI{
		srv: srv,
		mux: mux,
		configResponse: map[string]any{
			"Defaults": map[string]any{"Backend": "claude"},
		},
	}
}

func (f *fakeAPI) store() *Store {
	c := apiclient.New(f.srv.URL, "tok", apiclient.WithHTTPClient(f.srv.Client()))
	return New(c)
}

func TestStore_Tasks(t *testing.T) {
	f := newFakeAPI(t)
	f.cannedTasks = []*model.Task{
		{ID: "t1", Name: "alpha", Status: model.StatusInProgress, Project: "proj1"},
		{ID: "t2", Name: "beta", Status: model.StatusComplete, Project: "proj2", BaseBranch: "argus/t1"},
	}
	f.mux.HandleFunc("/api/tasks-raw", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"tasks": f.cannedTasks})
	})

	got, err := f.store().Tasks()
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 2)
	testutil.Equal(t, got[0].ID, "t1")
	testutil.Equal(t, got[1].ID, "t2")
	testutil.Equal(t, got[1].BaseBranch, "argus/t1")
}

func TestStore_Get(t *testing.T) {
	f := newFakeAPI(t)
	f.mux.HandleFunc("/api/tasks/t1/raw", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Task{ID: "t1", Name: "alpha"})
	})

	got, err := f.store().Get("t1")
	testutil.NoError(t, err)
	testutil.Equal(t, got.ID, "t1")
	testutil.Equal(t, got.Name, "alpha")
}

func TestStore_Update_RoundTrip(t *testing.T) {
	f := newFakeAPI(t)
	var got *model.Task
	f.mux.HandleFunc("/api/tasks/t1/raw", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			http.Error(w, "wrong method", http.StatusMethodNotAllowed)
			return
		}
		var t model.Task
		_ = json.NewDecoder(r.Body).Decode(&t)
		got = &t
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&t)
	})

	err := f.store().Update(&model.Task{ID: "t1", Name: "updated"})
	testutil.NoError(t, err)
	if got == nil {
		t.Fatal("server never received body")
	}
	testutil.Equal(t, got.Name, "updated")
}

func TestStore_RefreshConfig(t *testing.T) {
	f := newFakeAPI(t)
	f.mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(f.configResponse)
	})

	s := f.store()
	cfg, err := s.RefreshConfig(context.Background())
	testutil.NoError(t, err)
	testutil.Equal(t, cfg.Defaults.Backend, "claude")
	// Subsequent Config() returns cached value without round-trip.
	testutil.Equal(t, s.Config().Defaults.Backend, "claude")
}

// TestStore_RefreshConfig_ErrorPathsReturnCachedSnapshot pins the dedup'd
// error-path behavior of RefreshConfig: every failure mode (HTTP transport
// error, then a garbage body that fails JSON unmarshal) must return the last
// successfully-cached snapshot alongside the error — never a zero-value
// config that would blank the live UI. The cache read is routed through
// cachedSnapshot(); this exercises both branches that depend on it.
func TestStore_RefreshConfig_ErrorPathsReturnCachedSnapshot(t *testing.T) {
	// mode flips per-subtest so the single /api/config handler can simulate
	// a healthy fetch, a transport-level failure, and a garbage 200 body.
	const (
		modeOK      = "ok"
		modeHTTPErr = "http_err"
		modeGarbage = "garbage"
	)
	f := newFakeAPI(t)
	mode := modeOK
	f.mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		switch mode {
		case modeHTTPErr:
			http.Error(w, "boom", http.StatusInternalServerError)
		case modeGarbage:
			// 200 OK but a JSON shape that can't unmarshal into config.Config
			// (Defaults must be an object, not a string) — drives the
			// json.Unmarshal error path.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Defaults":"not-an-object"}`))
		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(f.configResponse)
		}
	})

	s := f.store()
	// Prime the cache with a good snapshot.
	_, err := s.RefreshConfig(context.Background())
	testutil.NoError(t, err)
	testutil.Equal(t, s.Config().Defaults.Backend, "claude")

	tests := []struct {
		name string
		mode string
	}{
		{"http transport error", modeHTTPErr},
		{"garbage body unmarshal error", modeGarbage},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mode = tc.mode
			cfg, err := s.RefreshConfig(context.Background())
			if err == nil {
				t.Fatal("expected error from RefreshConfig")
			}
			// Returned snapshot is the last-cached value, not the zero config.
			testutil.Equal(t, cfg.Defaults.Backend, "claude")
			// And the cache itself is untouched by the failed refresh.
			testutil.Equal(t, s.Config().Defaults.Backend, "claude")
		})
	}
}

func TestStore_Rename(t *testing.T) {
	f := newFakeAPI(t)
	var captured string
	f.mux.HandleFunc("/api/tasks/t1/rename", func(w http.ResponseWriter, r *http.Request) {
		body := readBody(r)
		captured = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"renamed"}`))
	})

	err := f.store().Rename("t1", "renamed")
	testutil.NoError(t, err)
	testutil.Contains(t, captured, `"name":"renamed"`)
}

// TestStore_SetPinned_PreservesServerName confirms the remote partial-pin
// path re-fetches the authoritative row before writing, so a name a background
// autoname rename already wrote on the server is not clobbered by the TUI's
// stale snapshot. The store never sends a name of its own — it round-trips
// whatever the server returns.
func TestStore_SetPinned_PreservesServerName(t *testing.T) {
	f := newFakeAPI(t)
	var wrote *model.Task
	f.mux.HandleFunc("/api/tasks/t1/raw", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			var tk model.Task
			_ = json.NewDecoder(r.Body).Decode(&tk)
			wrote = &tk
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(&tk)
			return
		}
		// GET: server's current name reflects the autoname rename.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Task{ID: "t1", Name: "haiku-name", Status: model.StatusInProgress})
	})

	testutil.NoError(t, f.store().SetPinned("t1", true))
	if wrote == nil {
		t.Fatal("server never received write")
	}
	testutil.Equal(t, wrote.Name, "haiku-name")
	testutil.Equal(t, wrote.Pinned, true)
	testutil.Equal(t, wrote.Archived, false)
}

func TestStore_SetStatus_PreservesServerName(t *testing.T) {
	f := newFakeAPI(t)
	var wrote *model.Task
	f.mux.HandleFunc("/api/tasks/t1/raw", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			var tk model.Task
			_ = json.NewDecoder(r.Body).Decode(&tk)
			wrote = &tk
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(&tk)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Task{ID: "t1", Name: "haiku-name", Status: model.StatusInProgress})
	})

	testutil.NoError(t, f.store().SetStatus("t1", model.StatusComplete))
	if wrote == nil {
		t.Fatal("server never received write")
	}
	testutil.Equal(t, wrote.Name, "haiku-name")
	testutil.Equal(t, wrote.Status, model.StatusComplete)
}

// TestStore_SetStatus_SameStatusNoWrite confirms the remote no-op fast path
// matches local db.SetStatus: a same-status set must not issue a PUT (which
// would re-stamp ended_at on the server), keeping local and remote in lockstep.
func TestStore_SetStatus_SameStatusNoWrite(t *testing.T) {
	f := newFakeAPI(t)
	getCalled, putCalled := false, false
	f.mux.HandleFunc("/api/tasks/t1/raw", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			putCalled = true
		case http.MethodGet:
			getCalled = true
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Task{ID: "t1", Name: "haiku-name", Status: model.StatusComplete})
	})

	testutil.NoError(t, f.store().SetStatus("t1", model.StatusComplete))
	testutil.Equal(t, getCalled, true) // re-fetch happens; the no-op is decided from the authoritative row
	testutil.Equal(t, putCalled, false)
}

// TestStore_SetPinned_SameValueNoWrite mirrors the SetStatus no-op test: a
// pin set to the already-current value re-fetches but issues no PUT.
func TestStore_SetPinned_SameValueNoWrite(t *testing.T) {
	f := newFakeAPI(t)
	getCalled, putCalled := false, false
	f.mux.HandleFunc("/api/tasks/t1/raw", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			putCalled = true
		case http.MethodGet:
			getCalled = true
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Task{ID: "t1", Name: "haiku-name", Pinned: true})
	})

	testutil.NoError(t, f.store().SetPinned("t1", true))
	testutil.Equal(t, getCalled, true)
	testutil.Equal(t, putCalled, false)
}

func TestStore_SetPinned_GetError(t *testing.T) {
	f := newFakeAPI(t)
	f.mux.HandleFunc("/api/tasks/t1/raw", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	if err := f.store().SetPinned("t1", true); err == nil {
		t.Fatal("expected error when fetch fails")
	}
}

func TestStore_SetStatus_GetError(t *testing.T) {
	f := newFakeAPI(t)
	f.mux.HandleFunc("/api/tasks/t1/raw", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	if err := f.store().SetStatus("t1", model.StatusComplete); err == nil {
		t.Fatal("expected error when fetch fails")
	}
}

func TestStore_PluginSections(t *testing.T) {
	f := newFakeAPI(t)
	f.mux.HandleFunc("/api/plugins/settings/sections", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sections":[{"scope":"a","title":"Hello","type":"form","callback_url":"http://x","fields":[{"key":"k","label":"L","type":"bool","default":true}]}]}`))
	})
	secs, err := f.store().PluginSections()
	testutil.NoError(t, err)
	testutil.Equal(t, len(secs), 1)
	testutil.Equal(t, secs[0].Scope, "a")
	testutil.Equal(t, secs[0].Title, "Hello")
	testutil.Equal(t, secs[0].CallbackURL, "http://x")
	if secs[0].Spec == nil || len(secs[0].Spec.Fields) != 1 {
		t.Fatalf("expected one parsed field, got %+v", secs[0].Spec)
	}
	testutil.Equal(t, secs[0].Spec.Fields[0].Key, "k")
}

func TestStore_PluginSections_ErrorPropagates(t *testing.T) {
	f := newFakeAPI(t)
	f.mux.HandleFunc("/api/plugins/settings/sections", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	_, err := f.store().PluginSections()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestStore_CreateTask(t *testing.T) {
	f := newFakeAPI(t)
	var captured string
	f.mux.HandleFunc("/api/tasks", func(w http.ResponseWriter, r *http.Request) {
		testutil.Equal(t, r.Method, "POST")
		captured = readBody(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"t9","name":"slug","status":"in_progress"}`))
	})
	f.mux.HandleFunc("/api/tasks/t9/raw", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Task{
			ID: "t9", Name: "slug", Status: model.StatusInProgress, Project: "proj", Backend: "claude",
		})
	})

	got, err := f.store().CreateTask(context.Background(), "", "do a thing", "proj", "claude", "opus")
	testutil.NoError(t, err)
	testutil.Equal(t, got.ID, "t9")
	testutil.Equal(t, got.Status, model.StatusInProgress)
	testutil.Equal(t, got.Project, "proj")
	// Request carried the create fields.
	testutil.Contains(t, captured, `"prompt":"do a thing"`)
	testutil.Contains(t, captured, `"project":"proj"`)
	testutil.Contains(t, captured, `"backend":"claude"`)
	testutil.Contains(t, captured, `"model":"opus"`)
}

// When the post-create raw fetch fails, CreateTask still returns a minimal
// task built from the (lossy) create response — the row exists on the server.
func TestStore_CreateTask_RawFetchFallback(t *testing.T) {
	f := newFakeAPI(t)
	f.mux.HandleFunc("/api/tasks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"t9","name":"slug","status":"pending"}`))
	})
	f.mux.HandleFunc("/api/tasks/t9/raw", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})

	got, err := f.store().CreateTask(context.Background(), "n", "p", "proj", "", "")
	testutil.NoError(t, err)
	testutil.Equal(t, got.ID, "t9")
	testutil.Equal(t, got.Name, "slug")
	testutil.Equal(t, got.Status, model.StatusPending)
	testutil.Equal(t, got.Project, "proj")
	testutil.Equal(t, got.Prompt, "p")
}

func TestStore_CreateTask_ErrorPropagates(t *testing.T) {
	f := newFakeAPI(t)
	f.mux.HandleFunc("/api/tasks", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "project not found", http.StatusInternalServerError)
	})
	_, err := f.store().CreateTask(context.Background(), "n", "p", "proj", "", "")
	if err == nil {
		t.Fatal("expected error from CreateTask")
	}
}

func TestStore_PruneCompleted(t *testing.T) {
	f := newFakeAPI(t)
	var method, path string
	f.mux.HandleFunc("/api/maintenance/prune-completed", func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"pruned":3,"worktrees":2,"orphans":1,"skippedHeraBound":4}`))
	})

	pruned, worktrees, orphans, skippedHeraBound, err := f.store().PruneCompleted(context.Background())
	testutil.NoError(t, err)
	testutil.Equal(t, method, "POST")
	testutil.Equal(t, path, "/api/maintenance/prune-completed")
	testutil.Equal(t, pruned, 3)
	testutil.Equal(t, worktrees, 2)
	testutil.Equal(t, orphans, 1)
	testutil.Equal(t, skippedHeraBound, 4)
}

func TestStore_PruneCompleted_ErrorPropagates(t *testing.T) {
	f := newFakeAPI(t)
	f.mux.HandleFunc("/api/maintenance/prune-completed", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	pruned, worktrees, orphans, skippedHeraBound, err := f.store().PruneCompleted(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	testutil.Equal(t, pruned, 0)
	testutil.Equal(t, worktrees, 0)
	testutil.Equal(t, orphans, 0)
	testutil.Equal(t, skippedHeraBound, 0)
}

func TestStore_ForkTask(t *testing.T) {
	f := newFakeAPI(t)
	var captured string
	f.mux.HandleFunc("/api/tasks/src1/fork", func(w http.ResponseWriter, r *http.Request) {
		testutil.Equal(t, r.Method, "POST")
		captured = readBody(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"f1","name":"fork-alpha","status":"in_progress"}`))
	})
	f.mux.HandleFunc("/api/tasks/f1/raw", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&model.Task{
			ID: "f1", Name: "fork-alpha", Status: model.StatusInProgress, Project: "proj",
		})
	})

	got, err := f.store().ForkTask(context.Background(), "src1", "fork-alpha", "", "proj")
	testutil.NoError(t, err)
	testutil.Equal(t, got.ID, "f1")
	testutil.Equal(t, got.Project, "proj")
	testutil.Contains(t, captured, `"name":"fork-alpha"`)
	testutil.Contains(t, captured, `"project":"proj"`)
}

func TestStore_ForkTask_RawFetchFallback(t *testing.T) {
	f := newFakeAPI(t)
	f.mux.HandleFunc("/api/tasks/src1/fork", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"f1","name":"fork-alpha","status":"pending"}`))
	})
	f.mux.HandleFunc("/api/tasks/f1/raw", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})

	got, err := f.store().ForkTask(context.Background(), "src1", "", "", "proj")
	testutil.NoError(t, err)
	testutil.Equal(t, got.ID, "f1")
	testutil.Equal(t, got.Name, "fork-alpha")
	testutil.Equal(t, got.Status, model.StatusPending)
	testutil.Equal(t, got.Project, "proj")
}

func TestStore_ForkTask_ErrorPropagates(t *testing.T) {
	f := newFakeAPI(t)
	f.mux.HandleFunc("/api/tasks/src1/fork", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "task not found", http.StatusNotFound)
	})
	_, err := f.store().ForkTask(context.Background(), "src1", "n", "p", "proj")
	if err == nil {
		t.Fatal("expected error from ForkTask")
	}
}

func TestStore_RunSchedule(t *testing.T) {
	f := newFakeAPI(t)
	var method, path string
	f.mux.HandleFunc("/api/schedules/s1/run", func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"task_id":"t42"}`))
	})

	taskID, err := f.store().RunSchedule(context.Background(), "s1")
	testutil.NoError(t, err)
	testutil.Equal(t, method, "POST")
	testutil.Equal(t, path, "/api/schedules/s1/run")
	testutil.Equal(t, taskID, "t42")
}

func TestStore_RunSchedule_ErrorPropagates(t *testing.T) {
	f := newFakeAPI(t)
	f.mux.HandleFunc("/api/schedules/s1/run", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "scheduler not running", http.StatusServiceUnavailable)
	})
	taskID, err := f.store().RunSchedule(context.Background(), "s1")
	if err == nil {
		t.Fatal("expected error from RunSchedule")
	}
	testutil.Equal(t, taskID, "")
}

func readBody(r *http.Request) string {
	buf := make([]byte, 4096)
	n, _ := r.Body.Read(buf)
	return string(buf[:n])
}
