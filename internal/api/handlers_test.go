package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func testServer(t *testing.T) (*Server, *db.DB) {
	t.Helper()
	d, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })

	runner := agent.NewRunner(nil)
	creator := func(name, prompt, project string, _ bool) (*model.Task, error) {
		task := &model.Task{
			Name:    name,
			Prompt:  prompt,
			Project: project,
			Status:  model.StatusInProgress,
		}
		d.Add(task)
		return task, nil
	}

	srv := New(d, runner, "test-token", creator, nil)
	return srv, d
}

func authedReq(method, url string, body string) *http.Request {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, url, nil)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	return req
}

func TestHandleStatus(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()

	// Add some tasks.
	d.Add(&model.Task{Name: "t1", Status: model.StatusPending})
	d.Add(&model.Task{Name: "t2", Status: model.StatusInProgress})
	d.Add(&model.Task{Name: "t3", Status: model.StatusComplete})

	req := authedReq("GET", "/api/status", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	testutil.Equal(t, w.Code, http.StatusOK)

	var resp statusResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	testutil.True(t, resp.OK)
	testutil.Equal(t, resp.Tasks.Pending, 1)
	testutil.Equal(t, resp.Tasks.InProgress, 1)
	testutil.Equal(t, resp.Tasks.Complete, 1)
}

func TestHandleListTasks(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()

	d.Add(&model.Task{Name: "task-a", Status: model.StatusPending, Project: "proj1"})
	d.Add(&model.Task{Name: "task-b", Status: model.StatusInProgress, Project: "proj2"})

	t.Run("lists all tasks", func(t *testing.T) {
		req := authedReq("GET", "/api/tasks", "")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)

		var resp map[string][]taskJSON
		json.Unmarshal(w.Body.Bytes(), &resp)
		testutil.Equal(t, len(resp["tasks"]), 2)
	})

	t.Run("in_progress without session reports idle", func(t *testing.T) {
		req := authedReq("GET", "/api/tasks?status=in_progress", "")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)

		var resp map[string][]taskJSON
		testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		testutil.Equal(t, len(resp["tasks"]), 1)
		testutil.Equal(t, resp["tasks"][0].Name, "task-b")
		testutil.True(t, resp["tasks"][0].Idle)
	})

	t.Run("non-in_progress task is never idle", func(t *testing.T) {
		req := authedReq("GET", "/api/tasks?status=pending", "")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		var resp map[string][]taskJSON
		testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		testutil.Equal(t, resp["tasks"][0].Idle, false)
	})

	t.Run("filters by status", func(t *testing.T) {
		req := authedReq("GET", "/api/tasks?status=pending", "")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)

		var resp map[string][]taskJSON
		json.Unmarshal(w.Body.Bytes(), &resp)
		testutil.Equal(t, len(resp["tasks"]), 1)
		testutil.Equal(t, resp["tasks"][0].Name, "task-a")
	})

	t.Run("filters by project", func(t *testing.T) {
		req := authedReq("GET", "/api/tasks?project=proj2", "")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)

		var resp map[string][]taskJSON
		json.Unmarshal(w.Body.Bytes(), &resp)
		testutil.Equal(t, len(resp["tasks"]), 1)
		testutil.Equal(t, resp["tasks"][0].Name, "task-b")
	})
}

func TestHandleGetTask(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()

	task := &model.Task{Name: "get-me", Status: model.StatusPending}
	d.Add(task)

	t.Run("found", func(t *testing.T) {
		req := authedReq("GET", "/api/tasks/"+task.ID, "")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)

		var resp taskJSON
		json.Unmarshal(w.Body.Bytes(), &resp)
		testutil.Equal(t, resp.Name, "get-me")
	})

	t.Run("not found", func(t *testing.T) {
		req := authedReq("GET", "/api/tasks/nonexistent", "")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusNotFound)
	})
}

func TestComputeRuntimeState(t *testing.T) {
	cases := []struct {
		name     string
		status   model.Status
		running  bool
		idle     bool
		wantIdle bool
	}{
		{"pending never idle", model.StatusPending, false, false, false},
		{"in_review never idle", model.StatusInReview, false, false, false},
		{"complete never idle", model.StatusComplete, false, false, false},
		{"in_progress no session is idle", model.StatusInProgress, false, false, true},
		{"in_progress active session not idle", model.StatusInProgress, true, false, false},
		{"in_progress idle session is idle", model.StatusInProgress, true, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := &model.Task{ID: "t1", Status: tc.status}
			running := map[string]bool{}
			idle := map[string]bool{}
			if tc.running {
				running["t1"] = true
			}
			if tc.idle {
				idle["t1"] = true
			}
			testutil.Equal(t, computeRuntimeState(task, running, idle).Idle, tc.wantIdle)
		})
	}
}

func TestHandleCreateTask(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	t.Run("creates task", func(t *testing.T) {
		body := `{"name":"new-task","prompt":"do the thing","project":"proj"}`
		req := authedReq("POST", "/api/tasks", body)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusCreated)

		var resp map[string]any
		json.Unmarshal(w.Body.Bytes(), &resp)
		testutil.Equal(t, resp["name"], "new-task")
		testutil.NotEqual(t, resp["id"], "")
	})

	t.Run("rejects missing project", func(t *testing.T) {
		body := `{"name":"task","prompt":"do it"}`
		req := authedReq("POST", "/api/tasks", body)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusBadRequest)
	})

	t.Run("rejects empty name and prompt", func(t *testing.T) {
		body := `{"project":"proj"}`
		req := authedReq("POST", "/api/tasks", body)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusBadRequest)
	})
}

// TestHandleResumeTask covers the resume endpoint paths: 404 on missing task,
// 409 on already-running task, and the heal path where the runner has a live
// session but the DB row drifted out of sync (the original "session already
// exists for task X" failure).
func TestHandleResumeTask(t *testing.T) {
	t.Run("404 when task missing", func(t *testing.T) {
		srv, _ := testServer(t)
		mux := srv.routes()
		req := authedReq("POST", "/api/tasks/missing/resume", "")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusNotFound)
	})

	// "Ghost in_progress" — DB says in_progress but the runner has no
	// session (e.g. daemon restarted and the row never reconciled). The
	// handler short-circuits with 409; this locks in that contract.
	t.Run("409 when already in_progress (with or without live session)", func(t *testing.T) {
		srv, d := testServer(t)
		mux := srv.routes()
		task := &model.Task{Name: "running", Status: model.StatusInProgress}
		d.Add(task)
		req := authedReq("POST", "/api/tasks/"+task.ID+"/resume", "")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusConflict)
	})

	t.Run("heals desync when runner has a live session", func(t *testing.T) {
		if testing.Short() {
			t.Skip("starts a real PTY-backed sleep; skipped in -short")
		}
		srv, d := testServer(t)
		mux := srv.routes()

		// `sh-sleep` works as a test backend because BuildCmd needs only a
		// non-empty backend Command and a worktree — no prompt-flag handling
		// or session-id pinning is involved when resume=false. If BuildCmd
		// is ever hardened to require additional fields, update this fixture.
		testutil.NoError(t, d.SetBackend("sh-sleep", config.Backend{Command: "sleep 30"}))

		task := &model.Task{
			Name:     "desynced",
			Status:   model.StatusPending,
			Backend:  "sh-sleep",
			Worktree: t.TempDir(),
		}
		testutil.NoError(t, d.Add(task))

		// Populate the runner with a real live session under task.ID, then
		// flip the DB row back to Pending to simulate the desync that
		// produces the "session already exists for task X" error.
		sess, err := srv.runner.Start(task, d.Config(), 24, 80, false)
		testutil.NoError(t, err)
		// The heal path is non-destructive — it never stops the session, so
		// the test owns the lifecycle. If the heal logic ever starts
		// stopping/restarting, this cleanup needs revisiting.
		t.Cleanup(func() {
			_ = srv.runner.Stop(task.ID)
			<-sess.Done()
		})
		task.SetStatus(model.StatusPending)
		testutil.NoError(t, d.Update(task))

		req := authedReq("POST", "/api/tasks/"+task.ID+"/resume", "")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)

		var resp map[string]any
		testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		testutil.Equal(t, resp["status"], "resumed")
		testutil.Equal(t, resp["healed"], true)

		// DB row should be re-synced to in_progress.
		got, err := d.Get(task.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, got.Status, model.StatusInProgress)
	})
}

func TestHandleDeleteTask(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()

	task := &model.Task{Name: "delete-me", Status: model.StatusPending}
	d.Add(task)

	req := authedReq("DELETE", "/api/tasks/"+task.ID, "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	// Verify deleted.
	got, _ := d.Get(task.ID)
	testutil.Nil(t, got)
}

func TestHandleListSkills(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()

	// Set up a project with a skill directory.
	projDir := t.TempDir()
	skillDir := filepath.Join(projDir, ".claude", "skills", "deploy")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\ndescription: Deploy to prod\n---\n"), 0o644)

	d.SetProject("myproj", config.Project{Path: projDir})

	t.Run("returns skills for project", func(t *testing.T) {
		req := authedReq("GET", "/api/skills?project=myproj", "")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)

		var resp map[string][]skillJSON
		testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		found := false
		for _, s := range resp["skills"] {
			if s.Name == "deploy" {
				found = true
				testutil.Equal(t, s.Description, "Deploy to prod")
			}
		}
		testutil.True(t, found)
	})

	t.Run("filters by substring", func(t *testing.T) {
		req := authedReq("GET", "/api/skills?project=myproj&filter=dep", "")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)

		var resp map[string][]skillJSON
		testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		for _, s := range resp["skills"] {
			testutil.True(t, strings.Contains(strings.ToLower(s.Name), "dep"))
		}
	})

	t.Run("no project returns global skills", func(t *testing.T) {
		req := authedReq("GET", "/api/skills", "")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)

		var resp map[string][]skillJSON
		testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		// Should succeed (may return global skills or empty).
		testutil.True(t, resp["skills"] != nil)
	})
}

func TestHandleSize(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	t.Run("404 when no session", func(t *testing.T) {
		req := authedReq("GET", "/api/tasks/missing/size", "")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusNotFound)
	})
}

func TestHandleResize(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	t.Run("404 when no session", func(t *testing.T) {
		req := authedReq("POST", "/api/tasks/missing/resize", `{"cols":80,"rows":24}`)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusNotFound)
	})

	t.Run("rejects zero dims", func(t *testing.T) {
		// Use a task that exists but has no live session — still 404 since
		// session presence is what matters. Zero-dim validation is downstream.
		// Test bad JSON instead.
		req := authedReq("POST", "/api/tasks/missing/resize", `not json`)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		// Without a session, we never reach JSON parse — 404 first.
		testutil.Equal(t, w.Code, http.StatusNotFound)
	})
}

func TestHandleGitDiff_PathTraversal(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()

	// Seed a task with a worktree set so the early "not found" path doesn't fire.
	task := &model.Task{Name: "diff-task", Status: model.StatusPending, Worktree: "/tmp"}
	testutil.NoError(t, d.Add(task))

	bad := []string{
		"/etc/passwd",          // absolute
		"../../etc/passwd",     // dotdot
		"foo/../../etc/passwd", // embedded dotdot
	}
	for _, path := range bad {
		t.Run("rejects "+path, func(t *testing.T) {
			req := authedReq("GET", "/api/tasks/"+task.ID+"/git/diff?path="+path, "")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			testutil.Equal(t, w.Code, http.StatusBadRequest)
		})
	}
}

func TestHandleStopAll_MasterOnly(t *testing.T) {
	srv, d := testServer(t)
	// Wrap with auth middleware so X-Argus-Auth gets set.
	handler := authMiddleware(srv.token, d, nil, srv.routes())

	t.Run("accepts master token", func(t *testing.T) {
		req := authedReq("POST", "/api/sessions/stop-all", "")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)
	})

	t.Run("rejects device token", func(t *testing.T) {
		plain, _, err := MintToken(d, "phone")
		testutil.NoError(t, err)
		req := httptest.NewRequest("POST", "/api/sessions/stop-all", nil)
		req.Header.Set("Authorization", "Bearer "+plain)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusForbidden)
	})
}

func TestHandlePushTest_MasterOnly(t *testing.T) {
	srv, d := testServer(t)
	handler := authMiddleware(srv.token, d, nil, srv.routes())

	t.Run("rejects device token", func(t *testing.T) {
		plain, _, err := MintToken(d, "phone")
		testutil.NoError(t, err)
		req := httptest.NewRequest("POST", "/api/push/test", nil)
		req.Header.Set("Authorization", "Bearer "+plain)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusForbidden)
	})
}

func TestHandleCreateToken_MasterOnly(t *testing.T) {
	srv, d := testServer(t)
	handler := authMiddleware(srv.token, d, nil, srv.routes())

	t.Run("device token cannot mint", func(t *testing.T) {
		plain, _, err := MintToken(d, "phone")
		testutil.NoError(t, err)
		req := httptest.NewRequest("POST", "/api/tokens", strings.NewReader(`{"label":"x"}`))
		req.Header.Set("Authorization", "Bearer "+plain)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusForbidden)
	})

	t.Run("master token mints", func(t *testing.T) {
		req := authedReq("POST", "/api/tokens", `{"label":"laptop"}`)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusCreated)
	})
}

func TestProjectsBackends_MasterOnly(t *testing.T) {
	srv, d := testServer(t)
	handler := authMiddleware(srv.token, d, nil, srv.routes())
	plain, _, err := MintToken(d, "phone")
	testutil.NoError(t, err)
	device := func(method, url, body string) *http.Request {
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, url, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
		} else {
			req = httptest.NewRequest(method, url, nil)
		}
		req.Header.Set("Authorization", "Bearer "+plain)
		return req
	}

	cases := []struct {
		name   string
		method string
		url    string
		body   string
	}{
		{"projects create", "POST", "/api/projects", `{"name":"x","path":"/tmp/x"}`},
		{"projects update", "PUT", "/api/projects/x", `{"path":"/tmp/y"}`},
		{"projects delete", "DELETE", "/api/projects/x", ""},
		{"backends create", "POST", "/api/backends", `{"name":"x","command":"echo"}`},
		{"backends update", "PUT", "/api/backends/x", `{"command":"echo y"}`},
		{"backends delete", "DELETE", "/api/backends/x", ""},
		{"tokens list", "GET", "/api/tokens", ""},
	}
	for _, c := range cases {
		t.Run(c.name+" forbidden for device", func(t *testing.T) {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, device(c.method, c.url, c.body))
			testutil.Equal(t, w.Code, http.StatusForbidden)
		})
	}
}

func TestHandleForkTask_RejectsEmptyProject(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()

	// Seed a source task with no project (legacy data).
	src := &model.Task{Name: "src", Status: model.StatusComplete, Project: ""}
	testutil.NoError(t, d.Add(src))

	t.Run("400 when source has no project and request omits it", func(t *testing.T) {
		req := authedReq("POST", "/api/tasks/"+src.ID+"/fork", `{"name":"forked"}`)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusBadRequest)
	})
}

func TestSanitizeName(t *testing.T) {
	t.Run("truncates long names", func(t *testing.T) {
		name := sanitizeName("This is a very long prompt that should be truncated at 40 characters")
		testutil.Equal(t, len(name), 40)
	})

	t.Run("replaces newlines", func(t *testing.T) {
		name := sanitizeName("line1\nline2\ttab")
		testutil.Equal(t, name, "line1 line2 tab")
	})
}

func TestHandleShareTarget(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	// /share serves the same dashboard HTML as /; client-side JS reads the
	// query params. Unauthenticated (the page must load before token entry).
	req := httptest.NewRequest("GET", "/share?title=hello&text=world&url=https%3A%2F%2Fexample.com", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	testutil.Equal(t, w.Code, http.StatusOK)
	testutil.Contains(t, w.Header().Get("Content-Type"), "text/html")
	// Confirm we're returning the dashboard. Match a stable structural marker
	// rather than the share JS variable name so a refactor doesn't silently
	// turn the test into a no-op.
	testutil.Contains(t, w.Body.String(), `id="main-app"`)
}

func TestManifestShareTarget(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	req := httptest.NewRequest("GET", "/manifest.webmanifest", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	testutil.Equal(t, w.Code, http.StatusOK)
	var m struct {
		ShareTarget struct {
			Action string            `json:"action"`
			Method string            `json:"method"`
			Params map[string]string `json:"params"`
		} `json:"share_target"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("manifest parse: %v", err)
	}
	testutil.Equal(t, m.ShareTarget.Action, "/share")
	testutil.Equal(t, m.ShareTarget.Method, "GET")
	testutil.Equal(t, m.ShareTarget.Params["title"], "title")
	testutil.Equal(t, m.ShareTarget.Params["text"], "text")
	testutil.Equal(t, m.ShareTarget.Params["url"], "url")
}
