package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/orch"
	"github.com/drn/argus/internal/testutil"
)

// seedLinkable inserts two trivial tasks the linking tests can wire together.
// Status is pending and project is empty — neither matters for the endpoints
// exercised below.
func seedLinkable(t *testing.T, srv *Server) (parent, child *model.Task) {
	t.Helper()
	parent = &model.Task{Name: "parent"}
	testutil.NoError(t, srv.db.Add(parent))
	child = &model.Task{Name: "child"}
	testutil.NoError(t, srv.db.Add(child))
	return parent, child
}

func TestAPI_LinkTask_Happy(t *testing.T) {
	srv, _ := testServer(t)
	parent, child := seedLinkable(t, srv)

	mux := srv.routes()
	body := `{"parent_id":"` + parent.ID + `"}`
	req := authedReq("POST", "/api/tasks/"+child.ID+"/deps", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	testutil.Equal(t, w.Code, http.StatusOK)
	got, _ := srv.db.Get(child.ID)
	testutil.DeepEqual(t, got.DependsOn, []string{parent.ID})
}

func TestAPI_LinkTask_Cycle(t *testing.T) {
	srv, _ := testServer(t)
	a := &model.Task{Name: "A"}
	testutil.NoError(t, srv.db.Add(a))
	b := &model.Task{Name: "B", DependsOn: []string{a.ID}}
	testutil.NoError(t, srv.db.Add(b))

	// Linking A → B closes the cycle A depends on B depends on A.
	mux := srv.routes()
	body := `{"parent_id":"` + b.ID + `"}`
	req := authedReq("POST", "/api/tasks/"+a.ID+"/deps", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	testutil.Equal(t, w.Code, http.StatusConflict)
	var resp struct {
		Error string   `json:"error"`
		Cycle []string `json:"cycle"`
	}
	testutil.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	testutil.NotEqual(t, resp.Error, "")
	if len(resp.Cycle) == 0 {
		t.Fatal("expected non-empty cycle in response")
	}
	// State unchanged: A should not have gained B in its DependsOn.
	gotA, _ := srv.db.Get(a.ID)
	testutil.Equal(t, len(gotA.DependsOn), 0)
}

func TestAPI_LinkTask_BadJSON(t *testing.T) {
	srv, _ := testServer(t)
	_, child := seedLinkable(t, srv)
	mux := srv.routes()
	req := authedReq("POST", "/api/tasks/"+child.ID+"/deps", "not-json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusBadRequest)
}

func TestAPI_LinkTask_MissingParent(t *testing.T) {
	srv, _ := testServer(t)
	_, child := seedLinkable(t, srv)
	mux := srv.routes()
	req := authedReq("POST", "/api/tasks/"+child.ID+"/deps", `{"parent_id":"ghost"}`)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusNotFound)
}

// TestAPI_HaltDownstream_RequiresMaster gates halt-downstream to master-only.
// Per-task linking endpoints (link/unlink/deps/plan-slug) intentionally
// accept device tokens — they match the archive/rename tier — so they have
// no requireMaster test.
func TestAPI_HaltDownstream_RequiresMaster(t *testing.T) {
	srv, _ := testServer(t)
	a := &model.Task{Name: "A", Status: model.StatusInProgress}
	testutil.NoError(t, srv.db.Add(a))

	// Simulate a device-token request: the auth middleware would tag it
	// X-Argus-Auth=device; here we go through the raw mux and set the
	// tag manually.
	mux := srv.routes()
	req := httptest.NewRequest("POST", "/api/tasks/"+a.ID+"/halt-downstream", nil)
	req.Header.Set("X-Argus-Auth", "device")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusForbidden)
}

func TestAPI_UnlinkTask(t *testing.T) {
	srv, _ := testServer(t)
	parent := &model.Task{Name: "P"}
	testutil.NoError(t, srv.db.Add(parent))
	child := &model.Task{Name: "C", DependsOn: []string{parent.ID}}
	testutil.NoError(t, srv.db.Add(child))

	mux := srv.routes()
	req := authedReq("DELETE", "/api/tasks/"+child.ID+"/deps/"+parent.ID, "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	testutil.Equal(t, w.Code, http.StatusOK)
	got, _ := srv.db.Get(child.ID)
	testutil.Equal(t, len(got.DependsOn), 0)
}

func TestAPI_GetDeps(t *testing.T) {
	srv, _ := testServer(t)
	a := &model.Task{Name: "A"}
	testutil.NoError(t, srv.db.Add(a))
	b := &model.Task{Name: "B", DependsOn: []string{a.ID}}
	testutil.NoError(t, srv.db.Add(b))
	c := &model.Task{Name: "C", DependsOn: []string{a.ID}}
	testutil.NoError(t, srv.db.Add(c))

	mux := srv.routes()
	req := authedReq("GET", "/api/tasks/"+a.ID+"/deps", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	testutil.Equal(t, w.Code, http.StatusOK)
	var view orch.DepsView
	testutil.NoError(t, json.NewDecoder(w.Body).Decode(&view))
	testutil.Equal(t, len(view.Upstream), 0)
	testutil.Equal(t, len(view.Downstream), 2)
}

func TestAPI_DAG_Filters(t *testing.T) {
	srv, _ := testServer(t)
	a := &model.Task{Name: "A", Project: "p1", PlanSlug: "s1"}
	testutil.NoError(t, srv.db.Add(a))
	b := &model.Task{Name: "B", Project: "p1", PlanSlug: "s1", DependsOn: []string{a.ID}}
	testutil.NoError(t, srv.db.Add(b))
	c := &model.Task{Name: "C", Project: "p2", PlanSlug: "s2"}
	testutil.NoError(t, srv.db.Add(c))
	d := &model.Task{Name: "D", Project: "p1", PlanSlug: "s1", Archived: true}
	testutil.NoError(t, srv.db.Add(d))

	mux := srv.routes()

	// No filter — every non-archived task surfaces.
	req := authedReq("GET", "/api/dag", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)
	var resp struct {
		Nodes []orch.DAGNode `json:"nodes"`
	}
	testutil.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	testutil.Equal(t, len(resp.Nodes), 3) // A, B, C; D is archived

	// Project filter narrows the set.
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, authedReq("GET", "/api/dag?project=p2", ""))
	var resp2 struct {
		Nodes []orch.DAGNode `json:"nodes"`
	}
	testutil.NoError(t, json.NewDecoder(w2.Body).Decode(&resp2))
	testutil.Equal(t, len(resp2.Nodes), 1)
	testutil.Equal(t, resp2.Nodes[0].ID, c.ID)

	// archived=1 brings back the greyed-out row.
	w3 := httptest.NewRecorder()
	mux.ServeHTTP(w3, authedReq("GET", "/api/dag?project=p1&archived=1", ""))
	var resp3 struct {
		Nodes []orch.DAGNode `json:"nodes"`
	}
	testutil.NoError(t, json.NewDecoder(w3.Body).Decode(&resp3))
	testutil.Equal(t, len(resp3.Nodes), 3) // A, B, D
}

func TestAPI_HaltDownstream(t *testing.T) {
	srv, _ := testServer(t)
	a := &model.Task{Name: "A", Status: model.StatusInProgress}
	testutil.NoError(t, srv.db.Add(a))
	b := &model.Task{Name: "B", DependsOn: []string{a.ID}, Status: model.StatusPending}
	testutil.NoError(t, srv.db.Add(b))
	c := &model.Task{Name: "C", DependsOn: []string{b.ID}, Status: model.StatusPending}
	testutil.NoError(t, srv.db.Add(c))

	mux := srv.routes()
	req := authedReq("POST", "/api/tasks/"+a.ID+"/halt-downstream", "")
	// authedReq goes through the raw mux without auth middleware; stamp the
	// master tag directly so requireMaster passes.
	req.Header.Set("X-Argus-Auth", "master")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	var report orch.HaltReport
	testutil.NoError(t, json.NewDecoder(w.Body).Decode(&report))
	testutil.Equal(t, len(report.Archived), 2)

	gotB, _ := srv.db.Get(b.ID)
	testutil.True(t, gotB.Archived)
	gotC, _ := srv.db.Get(c.ID)
	testutil.True(t, gotC.Archived)
	// Seed must remain unchanged.
	gotA, _ := srv.db.Get(a.ID)
	testutil.False(t, gotA.Archived)
}

func TestAPI_SetPlanSlug(t *testing.T) {
	srv, _ := testServer(t)
	task := &model.Task{Name: "T"}
	testutil.NoError(t, srv.db.Add(task))

	mux := srv.routes()
	req := authedReq("POST", "/api/tasks/"+task.ID+"/plan-slug", `{"plan_slug":"my-stack"}`)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	got, _ := srv.db.Get(task.ID)
	testutil.Equal(t, got.PlanSlug, "my-stack")
}

func TestAPI_SetPlanSlug_BadJSON(t *testing.T) {
	srv, _ := testServer(t)
	task := &model.Task{Name: "T"}
	testutil.NoError(t, srv.db.Add(task))

	mux := srv.routes()
	req := authedReq("POST", "/api/tasks/"+task.ID+"/plan-slug", "{bad")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusBadRequest)
}
