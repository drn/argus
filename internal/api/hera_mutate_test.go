package api

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func heraPost(srv *Server, path, body, auth string) *httptest.ResponseRecorder {
	req := authedReq("POST", path, body)
	if auth != "" {
		req.Header.Set("X-Argus-Auth", auth)
	}
	w := httptest.NewRecorder()
	srv.routes().ServeHTTP(w, req)
	srv.heraBG.Wait()
	return w
}

func idp(prefix string, id int64, suffix string) string {
	return "/api/hera/" + prefix + "/" + strconv.FormatInt(id, 10) + "/" + suffix
}

func seedOrch(t *testing.T, d *db.DB, name string, kinds ...db.HeraRoleKind) (*db.HeraOrchestrator, []*db.HeraRole, []*model.Task) {
	t.Helper()
	orch, err := d.CreateHeraOrchestrator(name, "")
	testutil.NoError(t, err)
	var roles []*db.HeraRole
	var tasks []*model.Task
	for i, k := range kinds {
		task := &model.Task{Name: name + "-t" + strconv.Itoa(i), Project: "p", Status: model.StatusInReview}
		testutil.NoError(t, d.Add(task))
		role, _, err := d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
			OrchestratorID: orch.ID, Name: name + "-r" + strconv.Itoa(i), Kind: k, ArgusProject: "p",
		}, task.ID, "/tmp/wt-"+name+strconv.Itoa(i))
		testutil.NoError(t, err)
		roles = append(roles, role)
		tasks = append(tasks, task)
	}
	return orch, roles, tasks
}

func TestHeraNukeOrch_MasterGate(t *testing.T) {
	srv, d := testServer(t)
	orch, _, _ := seedOrch(t, d, "o1", db.HeraKindCoordinator)
	for _, auth := range []string{"", "device"} {
		w := heraPost(srv, idp("orchestrators", orch.ID, "nuke"), "", auth)
		testutil.Equal(t, w.Code, http.StatusForbidden)
	}
	testutil.Equal(t, len(getHera(t, srv).Orchestrators), 1)
}

func TestHeraNukeOrch_CascadesAndArchivesTasks(t *testing.T) {
	srv, d := testServer(t)
	orch, _, tasks := seedOrch(t, d, "o1", db.HeraKindCoordinator, db.HeraKindWorker)

	w := heraPost(srv, idp("orchestrators", orch.ID, "nuke"), "", "master")
	testutil.Equal(t, w.Code, http.StatusOK)
	testutil.Equal(t, len(getHera(t, srv).Orchestrators), 0)

	got, err := d.HeraOrchestrator(orch.ID)
	testutil.NoError(t, err)
	testutil.True(t, got.NukedAt != nil) // row retained, never hard-deleted
	// The bg reclaim sweep prunes reclaimed tasks (no worktree, no session).
	for _, task := range tasks {
		if t2, err := d.Get(task.ID); err == nil && t2 != nil {
			testutil.True(t, t2.Archived)
		}
	}
}

func TestHeraNukeOrch_PreservesTaskBoundElsewhere(t *testing.T) {
	srv, d := testServer(t)
	orch, _, tasks := seedOrch(t, d, "o1", db.HeraKindWorker)
	other, err := d.CreateHeraOrchestrator("o2", "")
	testutil.NoError(t, err)
	_, _, err = d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: other.ID, Name: "shared", Kind: db.HeraKindWorker, ArgusProject: "p",
	}, tasks[0].ID, "/tmp/wt-shared")
	testutil.NoError(t, err)

	w := heraPost(srv, idp("orchestrators", orch.ID, "nuke"), "", "master")
	testutil.Equal(t, w.Code, http.StatusOK)

	got, err := d.Get(tasks[0].ID)
	testutil.NoError(t, err)
	testutil.True(t, got != nil)
	testutil.Equal(t, got.Archived, false)
	testutil.Equal(t, got.Status, model.StatusInReview)
	testutil.Equal(t, len(getHera(t, srv).Orchestrators), 1) // o2 survives
}

func TestHeraNukeOrch_NotFound(t *testing.T) {
	srv, _ := testServer(t)
	testutil.Equal(t, heraPost(srv, idp("orchestrators", 999, "nuke"), "", "master").Code, http.StatusNotFound)
	testutil.Equal(t, heraPost(srv, idp("roles", 999, "nuke"), "", "master").Code, http.StatusNotFound)
	testutil.Equal(t, heraPost(srv, "/api/hera/orchestrators/abc/nuke", "", "master").Code, http.StatusBadRequest)
}

func TestHeraNukePreview(t *testing.T) {
	srv, d := testServer(t)
	orch, _, _ := seedOrch(t, d, "o1", db.HeraKindCoordinator, db.HeraKindWorker)
	req := authedReq("GET", idp("orchestrators", orch.ID, "nuke-preview"), "")
	w := httptest.NewRecorder()
	srv.routes().ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)
	testutil.Contains(t, w.Body.String(), `"orchestrators":1`)
	testutil.Contains(t, w.Body.String(), `"agents":1`)
	testutil.Contains(t, w.Body.String(), `"worktrees":2`)
	// Preview mutates nothing.
	testutil.Equal(t, len(getHera(t, srv).Orchestrators), 1)

	w = httptest.NewRecorder()
	srv.routes().ServeHTTP(w, authedReq("GET", idp("orchestrators", 999, "nuke-preview"), ""))
	testutil.Equal(t, w.Code, http.StatusNotFound)
}

func TestHeraNukeRole(t *testing.T) {
	srv, d := testServer(t)
	orch, roles, _ := seedOrch(t, d, "o1", db.HeraKindCoordinator, db.HeraKindWorker)
	w := heraPost(srv, idp("roles", roles[1].ID, "nuke"), "", "master")
	testutil.Equal(t, w.Code, http.StatusOK)
	resp := getHera(t, srv)
	testutil.Equal(t, len(resp.Orchestrators), 1)
	testutil.Equal(t, resp.Orchestrators[0].ID, orch.ID)
	testutil.Equal(t, len(resp.Orchestrators[0].Roles), 1)
	r, err := d.HeraRole(roles[1].ID)
	testutil.NoError(t, err)
	testutil.True(t, r.NukedAt != nil)
}

func TestHeraArchiveUnarchive(t *testing.T) {
	srv, d := testServer(t)
	orch, roles, _ := seedOrch(t, d, "o1", db.HeraKindCoordinator, db.HeraKindWorker)

	testutil.Equal(t, heraPost(srv, idp("roles", roles[1].ID, "archive"), "", "device").Code, http.StatusOK)
	r, _ := d.HeraRole(roles[1].ID)
	testutil.True(t, r.ArchivedAt != nil)
	testutil.Equal(t, heraPost(srv, idp("roles", roles[1].ID, "unarchive"), "", "device").Code, http.StatusOK)
	r, _ = d.HeraRole(roles[1].ID)
	testutil.True(t, r.ArchivedAt == nil)

	testutil.Equal(t, heraPost(srv, idp("orchestrators", orch.ID, "archive"), "", "device").Code, http.StatusOK)
	testutil.True(t, getHera(t, srv).Orchestrators[0].Archived)
	testutil.Equal(t, heraPost(srv, idp("orchestrators", orch.ID, "unarchive"), "", "device").Code, http.StatusOK)
	testutil.Equal(t, getHera(t, srv).Orchestrators[0].Archived, false)

	testutil.Equal(t, heraPost(srv, idp("roles", 999, "archive"), "", "device").Code, http.StatusNotFound)
}

func TestHeraPinRename(t *testing.T) {
	srv, d := testServer(t)
	orch, roles, _ := seedOrch(t, d, "o1", db.HeraKindWorker)
	_, _, _ = seedOrch(t, d, "o2", db.HeraKindWorker)

	testutil.Equal(t, heraPost(srv, idp("orchestrators", orch.ID, "pin"), "", "device").Code, http.StatusOK)
	got, _ := d.HeraOrchestrator(orch.ID)
	testutil.True(t, got.PinnedAt != nil)
	testutil.Equal(t, heraPost(srv, idp("orchestrators", orch.ID, "unpin"), "", "device").Code, http.StatusOK)
	testutil.Equal(t, heraPost(srv, idp("roles", roles[0].ID, "pin"), "", "device").Code, http.StatusOK)
	testutil.Equal(t, heraPost(srv, idp("roles", roles[0].ID, "unpin"), "", "device").Code, http.StatusOK)

	testutil.Equal(t, heraPost(srv, idp("orchestrators", orch.ID, "rename"), `{"name":"renamed"}`, "device").Code, http.StatusOK)
	got, _ = d.HeraOrchestrator(orch.ID)
	testutil.Equal(t, got.Name, "renamed")
	testutil.Equal(t, heraPost(srv, idp("orchestrators", orch.ID, "rename"), `{"name":"o2"}`, "device").Code, http.StatusConflict)
	testutil.Equal(t, heraPost(srv, idp("orchestrators", orch.ID, "rename"), `{"name":" "}`, "device").Code, http.StatusBadRequest)
	testutil.Equal(t, heraPost(srv, idp("roles", roles[0].ID, "rename"), `{"name":"w-new"}`, "device").Code, http.StatusOK)
	testutil.Equal(t, heraPost(srv, idp("roles", 999, "rename"), `{"name":"x"}`, "device").Code, http.StatusNotFound)
}

func TestHeraRoleStatus(t *testing.T) {
	srv, d := testServer(t)
	_, roles, tasks := seedOrch(t, d, "o1", db.HeraKindWorker)
	// A worker task that is in_progress rolls to in_review on done.
	testutil.NoError(t, d.SetStatus(tasks[0].ID, model.StatusInProgress))

	testutil.Equal(t, heraPost(srv, idp("roles", roles[0].ID, "status"), `{"status":"bogus"}`, "device").Code, http.StatusBadRequest)
	testutil.Equal(t, heraPost(srv, idp("roles", 999, "status"), `{"status":"idle"}`, "device").Code, http.StatusNotFound)
	testutil.Equal(t, heraPost(srv, idp("roles", roles[0].ID, "status"), `{"status":"done"}`, "device").Code, http.StatusOK)

	st, err := d.HeraRoleStatusFor(roles[0].ID)
	testutil.NoError(t, err)
	testutil.Equal(t, st.Status, db.HeraStatusDone)
	got, _ := d.Get(tasks[0].ID)
	testutil.Equal(t, got.Status, model.StatusInReview)

	testutil.Equal(t, heraPost(srv, idp("roles", roles[0].ID, "status"), `{"status":"working"}`, "device").Code, http.StatusOK)
	st, _ = d.HeraRoleStatusFor(roles[0].ID)
	testutil.Equal(t, st.Status, db.HeraStatusWorking)
}

func TestHeraKanban(t *testing.T) {
	srv, d := testServer(t)
	orch, _, _ := seedOrch(t, d, "o1", db.HeraKindCoordinator)
	testutil.Equal(t, heraPost(srv, idp("orchestrators", orch.ID, "kanban"), `{"status":"backlog"}`, "device").Code, http.StatusOK)
	testutil.Equal(t, getHera(t, srv).Orchestrators[0].KanbanStatus, "backlog")
	testutil.Equal(t, heraPost(srv, idp("orchestrators", orch.ID, "kanban"), `{"status":"nope"}`, "device").Code, http.StatusBadRequest)
	testutil.Equal(t, heraPost(srv, idp("orchestrators", 999, "kanban"), `{"status":"done"}`, "device").Code, http.StatusNotFound)
}
