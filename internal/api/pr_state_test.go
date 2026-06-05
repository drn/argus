package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// TestHandleListTasks_PRState verifies that pr_state on the list DTO is sourced
// purely from the cached task_meta "pr" namespace and that the handler never
// shells out to gh. There is no prFetcher in the api package and no daemon
// poller running under test, so the only way pr_state can appear is from the
// seeded meta — which is exactly the cache-only contract.
func TestHandleListTasks_PRState(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()

	awaiting := &model.Task{Name: "awaiting", Status: model.StatusInReview, Project: "p", Branch: "argus/awaiting"}
	changes := &model.Task{Name: "changes", Status: model.StatusComplete, Project: "p", Branch: "argus/changes"}
	approved := &model.Task{Name: "approved", Status: model.StatusComplete, Project: "p", Branch: "argus/approved"}
	noneTask := &model.Task{Name: "none", Status: model.StatusComplete, Project: "p", Branch: "argus/none"}
	bareTask := &model.Task{Name: "bare", Status: model.StatusPending, Project: "p"}
	for _, tk := range []*model.Task{awaiting, changes, approved, noneTask, bareTask} {
		testutil.NoError(t, d.Add(tk))
	}

	// Seed cached PR review states (the daemon poller's job in production).
	testutil.NoError(t, d.SetMetaBatch(awaiting.ID, "pr", map[string]string{"state": model.PRAwaitingReview.String(), "url": "https://gh/1"}))
	testutil.NoError(t, d.SetMetaBatch(changes.ID, "pr", map[string]string{"state": model.PRChangesRequested.String(), "url": "https://gh/2"}))
	testutil.NoError(t, d.SetMetaBatch(approved.ID, "pr", map[string]string{"state": model.PRApproved.String(), "url": "https://gh/3"}))
	// "none" must collapse to an omitted field.
	testutil.NoError(t, d.SetMetaBatch(noneTask.ID, "pr", map[string]string{"state": model.PRNone.String()}))

	req := authedReq("GET", "/api/tasks", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	var resp struct {
		Tasks []taskJSON `json:"tasks"`
	}
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	byID := make(map[string]taskJSON, len(resp.Tasks))
	for _, tk := range resp.Tasks {
		byID[tk.ID] = tk
	}

	testutil.Equal(t, byID[awaiting.ID].PRState, "awaiting-review")
	testutil.Equal(t, byID[changes.ID].PRState, "changes-requested")
	testutil.Equal(t, byID[approved.ID].PRState, "approved")
	// "none" and "no meta at all" both render as empty (omitted) via omitempty.
	testutil.Equal(t, byID[noneTask.ID].PRState, "")
	testutil.Equal(t, byID[bareTask.ID].PRState, "")

	// Assert the wire JSON actually omits pr_state for none/bare tasks.
	testutil.Equal(t, jsonHasKey(t, w.Body.Bytes(), noneTask.ID, "pr_state"), false)
	testutil.Equal(t, jsonHasKey(t, w.Body.Bytes(), awaiting.ID, "pr_state"), true)
}

// TestHandleGetTask_PRState covers the single-task path.
func TestHandleGetTask_PRState(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()

	task := &model.Task{Name: "t", Status: model.StatusComplete, Project: "p", Branch: "argus/t"}
	testutil.NoError(t, d.Add(task))
	testutil.NoError(t, d.SetMetaBatch(task.ID, "pr", map[string]string{"state": model.PRApproved.String(), "url": "https://gh/9"}))

	req := authedReq("GET", "/api/tasks/"+task.ID, "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	var got taskJSON
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	testutil.Equal(t, got.PRState, "approved")

	t.Run("no meta omits pr_state", func(t *testing.T) {
		bare := &model.Task{Name: "bare", Status: model.StatusComplete, Project: "p"}
		testutil.NoError(t, d.Add(bare))
		req := authedReq("GET", "/api/tasks/"+bare.ID, "")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)
		var got taskJSON
		testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
		testutil.Equal(t, got.PRState, "")
	})
}

// jsonHasKey reports whether the task object with the given id in a
// {"tasks":[...]} body carries the named key (used to prove omitempty drops
// pr_state rather than emitting "").
func jsonHasKey(t *testing.T, body []byte, id, key string) bool {
	t.Helper()
	var resp struct {
		Tasks []map[string]json.RawMessage `json:"tasks"`
	}
	testutil.NoError(t, json.Unmarshal(body, &resp))
	for _, m := range resp.Tasks {
		var gotID string
		if raw, ok := m["id"]; ok {
			_ = json.Unmarshal(raw, &gotID)
		}
		if gotID == id {
			_, has := m[key]
			return has
		}
	}
	t.Fatalf("task %s not found in body", id)
	return false
}
