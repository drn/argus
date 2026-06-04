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
		{ID: "t2", Name: "beta", Status: model.StatusComplete, Project: "proj2", DependsOn: []string{"t1"}},
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
	testutil.DeepEqual(t, got[1].DependsOn, []string{"t1"})
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

	got, err := f.store().CreateTask(context.Background(), "", "do a thing", "proj", "claude")
	testutil.NoError(t, err)
	testutil.Equal(t, got.ID, "t9")
	testutil.Equal(t, got.Status, model.StatusInProgress)
	testutil.Equal(t, got.Project, "proj")
	// Request carried the create fields.
	testutil.Contains(t, captured, `"prompt":"do a thing"`)
	testutil.Contains(t, captured, `"project":"proj"`)
	testutil.Contains(t, captured, `"backend":"claude"`)
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

	got, err := f.store().CreateTask(context.Background(), "n", "p", "proj", "")
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
	_, err := f.store().CreateTask(context.Background(), "n", "p", "proj", "")
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
		_, _ = w.Write([]byte(`{"pruned":3,"worktrees":2,"orphans":1}`))
	})

	pruned, worktrees, orphans, err := f.store().PruneCompleted(context.Background())
	testutil.NoError(t, err)
	testutil.Equal(t, method, "POST")
	testutil.Equal(t, path, "/api/maintenance/prune-completed")
	testutil.Equal(t, pruned, 3)
	testutil.Equal(t, worktrees, 2)
	testutil.Equal(t, orphans, 1)
}

func TestStore_PruneCompleted_ErrorPropagates(t *testing.T) {
	f := newFakeAPI(t)
	f.mux.HandleFunc("/api/maintenance/prune-completed", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	pruned, worktrees, orphans, err := f.store().PruneCompleted(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	testutil.Equal(t, pruned, 0)
	testutil.Equal(t, worktrees, 0)
	testutil.Equal(t, orphans, 0)
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
