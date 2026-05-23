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

func readBody(r *http.Request) string {
	buf := make([]byte, 4096)
	n, _ := r.Body.Read(buf)
	return string(buf[:n])
}
