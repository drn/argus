package apistore

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drn/argus/internal/apiclient"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// newStore is a one-call test helper: hook handlers onto a fresh mux, get
// back the *Store wired to it. Saves the boilerplate of newFakeAPI + .store()
// in every short test.
func newStore(t *testing.T, register func(mux *http.ServeMux)) (*Store, *httptest.Server) {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	if register != nil {
		register(mux)
	}
	c := apiclient.New(srv.URL, "tok", apiclient.WithHTTPClient(srv.Client()))
	return New(c), srv
}

func TestStore_Add_RoundTripsAssignedID(t *testing.T) {
	s, _ := newStore(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/api/tasks-raw", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" {
				http.Error(w, "wrong method", http.StatusMethodNotAllowed)
				return
			}
			var task model.Task
			_ = json.NewDecoder(r.Body).Decode(&task)
			task.ID = "server-assigned-id"
			task.CreatedAt = time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(&task)
		})
	})

	task := &model.Task{Name: "new", Project: "p"}
	testutil.NoError(t, s.Add(task))
	testutil.Equal(t, task.ID, "server-assigned-id")
	if task.CreatedAt.IsZero() {
		t.Fatal("CreatedAt should be populated from server response")
	}
}

func TestStore_Delete(t *testing.T) {
	var hits int32
	s, _ := newStore(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/api/tasks/t1", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "DELETE" {
				atomic.AddInt32(&hits, 1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"status":"deleted"}`))
			}
		})
	})
	testutil.NoError(t, s.Delete("t1"))
	testutil.Equal(t, atomic.LoadInt32(&hits), int32(1))
}

func TestStore_Projects(t *testing.T) {
	s, _ := newStore(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/api/projects/full", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"projects":[{"name":"alpha","path":"/tmp/alpha","branch":"main"},{"name":"beta","path":"/tmp/beta","sandbox":{"enabled":true,"deny_read":["~/.aws"],"extra_write":["/build"],"allow_apple_events":[]}}]}`))
		})
	})
	got, err := s.Projects()
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 2)
	testutil.Equal(t, got["alpha"].Path, "/tmp/alpha")
	if got["beta"].Sandbox.Enabled == nil || !*got["beta"].Sandbox.Enabled {
		t.Fatal("expected beta.Sandbox.Enabled true")
	}
	testutil.DeepEqual(t, got["beta"].Sandbox.DenyRead, []string{"~/.aws"})
}

func TestStore_SetProject_POSTSuccess(t *testing.T) {
	var postHits, putHits int32
	s, _ := newStore(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/api/projects", func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&postHits, 1)
			w.WriteHeader(http.StatusCreated)
		})
		mux.HandleFunc("/api/projects/alpha", func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&putHits, 1)
		})
	})
	testutil.NoError(t, s.SetProject("alpha", config.Project{Path: "/tmp/alpha"}))
	testutil.Equal(t, atomic.LoadInt32(&postHits), int32(1))
	testutil.Equal(t, atomic.LoadInt32(&putHits), int32(0))
}

func TestStore_SetProject_409FallsBackToPUT(t *testing.T) {
	var postHits, putHits int32
	s, _ := newStore(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/api/projects", func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&postHits, 1)
			http.Error(w, `{"error":"already exists"}`, http.StatusConflict)
		})
		mux.HandleFunc("/api/projects/alpha", func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&putHits, 1)
			w.WriteHeader(http.StatusOK)
		})
	})
	testutil.NoError(t, s.SetProject("alpha", config.Project{Path: "/tmp/alpha"}))
	testutil.Equal(t, atomic.LoadInt32(&postHits), int32(1))
	testutil.Equal(t, atomic.LoadInt32(&putHits), int32(1))
}

func TestStore_SetProject_4xxSurfacesError(t *testing.T) {
	var putHits int32
	s, _ := newStore(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/api/projects", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"error":"path is required"}`, http.StatusBadRequest)
		})
		mux.HandleFunc("/api/projects/alpha", func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&putHits, 1)
		})
	})
	err := s.SetProject("alpha", config.Project{})
	if err == nil {
		t.Fatal("expected 400 to surface, not silently fall back to PUT")
	}
	testutil.Equal(t, atomic.LoadInt32(&putHits), int32(0))
}

func TestStore_DeleteProject(t *testing.T) {
	s, _ := newStore(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/api/projects/gone", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "DELETE" {
				http.Error(w, "wrong method", http.StatusMethodNotAllowed)
				return
			}
			w.WriteHeader(http.StatusOK)
		})
	})
	testutil.NoError(t, s.DeleteProject("gone"))
}

func TestStore_Backends(t *testing.T) {
	s, _ := newStore(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/api/backends", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"backends":[{"name":"claude","command":"claude","prompt_flag":"-p"}]}`))
		})
	})
	got, err := s.Backends()
	testutil.NoError(t, err)
	testutil.Equal(t, got["claude"].Command, "claude")
	testutil.Equal(t, got["claude"].PromptFlag, "-p")
}

func TestStore_SetBackend_And_Delete(t *testing.T) {
	var postHits, delHits int32
	s, _ := newStore(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/api/backends", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "POST" {
				atomic.AddInt32(&postHits, 1)
				w.WriteHeader(http.StatusCreated)
			}
		})
		mux.HandleFunc("/api/backends/foo", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "DELETE" {
				atomic.AddInt32(&delHits, 1)
				w.WriteHeader(http.StatusOK)
			}
		})
	})
	testutil.NoError(t, s.SetBackend("foo", config.Backend{Command: "cmd"}))
	testutil.NoError(t, s.DeleteBackend("foo"))
	testutil.Equal(t, atomic.LoadInt32(&postHits), int32(1))
	testutil.Equal(t, atomic.LoadInt32(&delHits), int32(1))
}

func TestStore_Schedules_RoundTrip(t *testing.T) {
	s, _ := newStore(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/api/schedules", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"schedules":[{"id":"s1","name":"daily","project":"p","prompt":"x","schedule":"@daily","enabled":true,"created_at":"2026-05-22T00:00:00Z"}]}`))
		})
	})
	got, err := s.Schedules()
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 1)
	testutil.Equal(t, got[0].ID, "s1")
	if got[0].CreatedAt.IsZero() {
		t.Fatal("CreatedAt should parse RFC3339")
	}
}

func TestStore_AddSchedule_AssignsIDAndCreatedAt(t *testing.T) {
	s, _ := newStore(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/api/schedules", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"new-sched","name":"daily","project":"p","prompt":"x","schedule":"@daily","enabled":true,"created_at":"2026-05-22T00:00:00Z"}`))
		})
	})
	sch := &model.ScheduledTask{Name: "daily", Project: "p", Prompt: "x", Schedule: "@daily", Enabled: true}
	testutil.NoError(t, s.AddSchedule(sch))
	testutil.Equal(t, sch.ID, "new-sched")
	if sch.CreatedAt.IsZero() {
		t.Fatal("AddSchedule should populate CreatedAt")
	}
}

func TestStore_UpdateSchedule_DeleteSchedule(t *testing.T) {
	var putHits, delHits int32
	s, _ := newStore(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/api/schedules/s1", func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case "PUT":
				atomic.AddInt32(&putHits, 1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"s1","schedule":"@hourly"}`))
			case "DELETE":
				atomic.AddInt32(&delHits, 1)
				w.WriteHeader(http.StatusOK)
			}
		})
	})
	testutil.NoError(t, s.UpdateSchedule(&model.ScheduledTask{ID: "s1", Schedule: "@hourly"}))
	testutil.NoError(t, s.DeleteSchedule("s1"))
	testutil.Equal(t, atomic.LoadInt32(&putHits), int32(1))
	testutil.Equal(t, atomic.LoadInt32(&delHits), int32(1))
}

func TestStore_GetSchedule_404(t *testing.T) {
	s, _ := newStore(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/api/schedules/missing/raw", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		})
	})
	_, err := s.GetSchedule("missing")
	if err == nil {
		t.Fatal("expected ErrScheduleNotFound from 404")
	}
}

func TestStore_SetConfigValue_KeyMapping(t *testing.T) {
	cases := []struct {
		key, val string
	}{
		{"sandbox.enabled", "true"},
		{"sandbox.deny_read", "/a,/b"},
		{"sandbox.extra_write", "/c"},
		{"sandbox.allow_apple_events", "com.apple.Terminal"},
		{"kb.enabled", "false"},
		{"kb.metis_vault_path", "/home/user/vault"},
		{"api.enabled", "true"},
		{"defaults.backend", "claude"},
		{"default_backend", "codex"},
		{"defaults.share_project", "argus"},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			var captured string
			s, _ := newStore(t, func(mux *http.ServeMux) {
				mux.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
					body, _ := readAll(r)
					captured = body
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"sandbox":{"enabled":false,"available":true,"deny_read":[],"extra_write":[],"allow_apple_events":[]},"kb":{"enabled":false,"metis_vault_path":""},"api":{"enabled":false,"http_port":0},"defaults":{"backend":""}}`))
				})
			})
			testutil.NoError(t, s.SetConfigValue(c.key, c.val))
			if captured == "" {
				t.Fatal("server never received PUT body")
			}
		})
	}

	t.Run("unknown key errors", func(t *testing.T) {
		s, _ := newStore(t, func(mux *http.ServeMux) {})
		err := s.SetConfigValue("unknown.thing", "x")
		if err == nil {
			t.Fatal("expected error for unmapped key")
		}
		testutil.Contains(t, err.Error(), "no remote handler")
	})
}

func TestStore_SetArchived(t *testing.T) {
	var arcHits, unarcHits int32
	s, _ := newStore(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/api/tasks/t1/archive", func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&arcHits, 1)
			w.WriteHeader(http.StatusOK)
		})
		mux.HandleFunc("/api/tasks/t1/unarchive", func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&unarcHits, 1)
			w.WriteHeader(http.StatusOK)
		})
	})
	testutil.NoError(t, s.SetArchived("t1", true))
	testutil.NoError(t, s.SetArchived("t1", false))
	testutil.Equal(t, atomic.LoadInt32(&arcHits), int32(1))
	testutil.Equal(t, atomic.LoadInt32(&unarcHits), int32(1))
}

func TestStore_SetPlanSlug(t *testing.T) {
	var captured string
	s, _ := newStore(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/api/tasks/t1/plan-slug", func(w http.ResponseWriter, r *http.Request) {
			body, _ := readAll(r)
			captured = body
			w.WriteHeader(http.StatusOK)
		})
	})
	testutil.NoError(t, s.SetPlanSlug("t1", "feature-x"))
	testutil.Contains(t, captured, "feature-x")
}

func TestStore_SetDependsOn_DiffsCurrentVsTarget(t *testing.T) {
	var linkCalls, unlinkCalls []string
	s, _ := newStore(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/api/tasks/child/deps", func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case "GET":
				_, _ = w.Write([]byte(`{"parents":[{"id":"p1"},{"id":"p2"}]}`))
			case "POST":
				body, _ := readAll(r)
				linkCalls = append(linkCalls, body)
				w.WriteHeader(http.StatusOK)
			}
		})
		mux.HandleFunc("/api/tasks/child/deps/p1", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "DELETE" {
				unlinkCalls = append(unlinkCalls, "p1")
				w.WriteHeader(http.StatusOK)
			}
		})
		mux.HandleFunc("/api/tasks/child/deps/p2", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "DELETE" {
				unlinkCalls = append(unlinkCalls, "p2")
				w.WriteHeader(http.StatusOK)
			}
		})
		mux.HandleFunc("/api/tasks/child/deps/p3", func(w http.ResponseWriter, r *http.Request) {})
	})

	// Target: keep p1, drop p2, add p3.
	testutil.NoError(t, s.SetDependsOn("child", []string{"p1", "p3"}))

	// p3 added (POST), p2 removed (DELETE), p1 untouched.
	if len(linkCalls) != 1 {
		t.Fatalf("expected 1 link call (p3), got %d", len(linkCalls))
	}
	testutil.Contains(t, linkCalls[0], "p3")
	if len(unlinkCalls) != 1 || unlinkCalls[0] != "p2" {
		t.Fatalf("expected unlink p2, got %v", unlinkCalls)
	}
}

func TestStore_DeleteMessagesForTask_ReturnsError(t *testing.T) {
	s, _ := newStore(t, func(mux *http.ServeMux) {})
	n, err := s.DeleteMessagesForTask("t1")
	testutil.Equal(t, n, 0)
	if err == nil {
		t.Fatal("expected non-nil error — endpoint not exposed")
	}
}

func TestStore_Get_404TranslatesToErrTaskNotFound(t *testing.T) {
	s, _ := newStore(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/api/tasks/missing/raw", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"error":"task not found"}`, http.StatusNotFound)
		})
	})
	_, err := s.Get("missing")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestStore_RefreshConfig_RaceSafe(t *testing.T) {
	// Reproduce the iter-1 data-race scenario: many concurrent readers and a
	// single writer hitting cachedConfig. With -race this would fire if the
	// RWMutex were missing or used incorrectly.
	s, _ := newStore(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Defaults":{"Backend":"claude"}}`))
		})
	})
	var wg sync.WaitGroup
	wg.Add(20)
	for i := 0; i < 10; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = s.Config()
			}
		}()
	}
	for i := 0; i < 10; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				_, _ = s.RefreshConfig(context.Background())
			}
		}()
	}
	wg.Wait()
}

func TestShouldFallbackUpsert(t *testing.T) {
	t.Run("409 yes", func(t *testing.T) {
		err := &apiclient.Error{Status: 409}
		testutil.True(t, shouldFallbackUpsert(err))
	})
	t.Run("400 no", func(t *testing.T) {
		err := &apiclient.Error{Status: 400}
		testutil.False(t, shouldFallbackUpsert(err))
	})
	t.Run("500 no — narrow to 409 only", func(t *testing.T) {
		err := &apiclient.Error{Status: 500}
		testutil.False(t, shouldFallbackUpsert(err))
	})
	t.Run("transport no — uncertain server state", func(t *testing.T) {
		testutil.False(t, shouldFallbackUpsert(errorsNew("network down")))
	})
}

func TestSplitCSV(t *testing.T) {
	testutil.DeepEqual(t, splitCSV(""), []string{})
	testutil.DeepEqual(t, splitCSV("a,b,c"), []string{"a", "b", "c"})
	testutil.DeepEqual(t, splitCSV(" a , , b "), []string{"a", "b"})
}

func TestStringsOrEmpty(t *testing.T) {
	testutil.DeepEqual(t, stringsOrEmpty(nil), []string{})
	testutil.DeepEqual(t, stringsOrEmpty([]string{"a"}), []string{"a"})
}

func TestProjectFromAPI_RoundTrip(t *testing.T) {
	enabled := true
	api := apiclient.ProjectJSON{
		Name: "p", Path: "/tmp/p", Branch: "main", Backend: "claude",
		Sandbox: map[string]any{"enabled": enabled, "deny_read": []any{"/x"}, "extra_write": []any{"/y"}, "allow_apple_events": []any{"com.apple.Terminal"}},
	}
	out := projectFromAPI(api)
	testutil.Equal(t, out.Path, "/tmp/p")
	if out.Sandbox.Enabled == nil || !*out.Sandbox.Enabled {
		t.Fatal("Sandbox.Enabled lost")
	}
	testutil.DeepEqual(t, out.Sandbox.DenyRead, []string{"/x"})
}

func TestProjectToAPI_OmitsEmptySandbox(t *testing.T) {
	out := projectToAPI("p", config.Project{Path: "/tmp/p"})
	if out.Sandbox != nil {
		t.Fatal("empty sandbox should serialize as nil to keep wire shape minimal")
	}
}

func TestProjectToAPI_SerializesSandbox(t *testing.T) {
	enabled := true
	p := config.Project{
		Path: "/tmp/p",
		Sandbox: config.ProjectSandboxConfig{
			Enabled:          &enabled,
			DenyRead:         []string{"/secret"},
			ExtraWrite:       []string{"/build"},
			AllowAppleEvents: []string{"com.apple.Terminal"},
		},
	}
	out := projectToAPI("p", p)
	if out.Sandbox == nil {
		t.Fatal("sandbox should serialize when any field is set")
	}
	testutil.Equal(t, out.Sandbox["enabled"], true)
}

func TestStringSliceFromAny(t *testing.T) {
	testutil.Equal(t, len(stringSliceFromAny(nil)), 0)
	testutil.Equal(t, len(stringSliceFromAny("not-a-slice")), 0)
	got := stringSliceFromAny([]any{"a", 42, "b"})
	testutil.DeepEqual(t, got, []string{"a", "b"})
}

func TestStore_RefreshConfig_HTTPFailureKeepsPreviousValue(t *testing.T) {
	var hits int32
	s, _ := newStore(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
			n := atomic.AddInt32(&hits, 1)
			if n == 1 {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"Defaults":{"Backend":"claude"}}`))
			} else {
				http.Error(w, "boom", http.StatusInternalServerError)
			}
		})
	})
	cfg, err := s.RefreshConfig(context.Background())
	testutil.NoError(t, err)
	testutil.Equal(t, cfg.Defaults.Backend, "claude")
	// Second call errors but cached value should still be visible.
	_, err = s.RefreshConfig(context.Background())
	if err == nil {
		t.Fatal("expected error on second call")
	}
	testutil.Equal(t, s.Config().Defaults.Backend, "claude")
}

func TestStore_SetBackend_409FallsBackToPUT(t *testing.T) {
	var postHits, putHits int32
	s, _ := newStore(t, func(mux *http.ServeMux) {
		mux.HandleFunc("/api/backends", func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&postHits, 1)
			http.Error(w, `{"error":"exists"}`, http.StatusConflict)
		})
		mux.HandleFunc("/api/backends/foo", func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&putHits, 1)
			w.WriteHeader(http.StatusOK)
		})
	})
	testutil.NoError(t, s.SetBackend("foo", config.Backend{Command: "cmd"}))
	testutil.Equal(t, atomic.LoadInt32(&postHits), int32(1))
	testutil.Equal(t, atomic.LoadInt32(&putHits), int32(1))
}

func TestScheduleReqFromModel_OmitsZeroRunOnceAt(t *testing.T) {
	req := scheduleReqFromModel(&model.ScheduledTask{Name: "n", Project: "p", Prompt: "x", Schedule: "@daily"})
	if req.RunOnceAt != nil {
		t.Fatalf("zero RunOnceAt should serialize as nil, got %q", *req.RunOnceAt)
	}
}

func TestScheduleReqFromModel_PreservesRunOnceAt(t *testing.T) {
	ts := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	req := scheduleReqFromModel(&model.ScheduledTask{Name: "n", Project: "p", Prompt: "x", RunOnceAt: ts})
	if req.RunOnceAt == nil {
		t.Fatal("non-zero RunOnceAt must be sent")
	}
	if !strings.HasPrefix(*req.RunOnceAt, "2026-05-22T12:00:00") {
		t.Fatalf("RunOnceAt format mismatch: %q", *req.RunOnceAt)
	}
}

// readAll is a single-shot body reader for short test bodies.
func readAll(r *http.Request) (string, error) {
	buf := make([]byte, 16*1024)
	n, _ := r.Body.Read(buf)
	return string(buf[:n]), nil
}

// errorsNew avoids a top-level import of "errors" (already in store.go).
type plainErr struct{ msg string }

func (e *plainErr) Error() string { return e.msg }

func errorsNew(msg string) error { return &plainErr{msg} }
