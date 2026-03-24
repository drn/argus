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
	creator := func(name, prompt, project, todoPath string) (*model.Task, error) {
		task := &model.Task{
			Name:    name,
			Prompt:  prompt,
			Project: project,
			Status:  model.StatusInProgress,
		}
		d.Add(task)
		return task, nil
	}

	srv := New(d, runner, "test-token", creator)
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
		json.Unmarshal(w.Body.Bytes(), &resp)
		found := false
		for _, s := range resp["skills"] {
			if s.Name == "deploy" {
				found = true
				testutil.Equal(t, s.Description, "Deploy to prod")
			}
		}
		testutil.True(t, found)
	})

	t.Run("filters by prefix", func(t *testing.T) {
		req := authedReq("GET", "/api/skills?project=myproj&prefix=dep", "")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)

		var resp map[string][]skillJSON
		json.Unmarshal(w.Body.Bytes(), &resp)
		for _, s := range resp["skills"] {
			testutil.True(t, strings.HasPrefix(s.Name, "dep"))
		}
	})

	t.Run("no project returns global skills", func(t *testing.T) {
		req := authedReq("GET", "/api/skills", "")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)

		var resp map[string][]skillJSON
		json.Unmarshal(w.Body.Bytes(), &resp)
		// Should succeed (may return global skills or empty).
		testutil.True(t, resp["skills"] != nil)
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
