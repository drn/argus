package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/hera"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// testHeraAPIServer wires SetHeraMutations with a real hera.Service (no
// notifier — delivery is soft-fail-skipped, which is fine for these tests)
// and no spawner by default; tests that exercise the spawn-worker endpoint
// override srv.heraSpawn directly.
func testHeraAPIServer(t *testing.T) (*Server, *db.DB) {
	t.Helper()
	srv, d := testServer(t)
	srv.SetHeraMutations(hera.New(d, nil), nil)
	return srv, d
}

// seedAPICoordinator creates an orchestrator with a live-bound coordinator
// role (task in test-project) — the precondition every mutation endpoint
// requires.
func seedAPICoordinator(t *testing.T, d *db.DB, orchName string) (*db.HeraOrchestrator, *db.HeraRole, *model.Task) {
	t.Helper()
	task := &model.Task{Name: orchName + "-coord-task", Project: "test-project", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))
	orch, err := d.CreateHeraOrchestrator(orchName, "")
	testutil.NoError(t, err)
	role, _, err := d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID,
		Name:           "coord",
		Kind:           db.HeraKindCoordinator,
		ArgusProject:   "test-project",
	}, task.ID, "/tmp/wt")
	testutil.NoError(t, err)
	return orch, role, task
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	testutil.NoError(t, err)
	return string(b)
}

func doHeraReq(srv *Server, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	srv.routes().ServeHTTP(w, authedReq(method, path, body))
	return w
}

func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	return out
}

// --- shared precondition ---

func TestHeraMutation_UnknownOrchestrator(t *testing.T) {
	srv, _ := testHeraAPIServer(t)
	w := doHeraReq(srv, "POST", "/api/hera/orchestrators/999/workers", `{"prompt":"x"}`)
	testutil.Equal(t, w.Code, http.StatusNotFound)
}

func TestHeraMutation_NonNumericOrchestrator(t *testing.T) {
	srv, _ := testHeraAPIServer(t)
	w := doHeraReq(srv, "POST", "/api/hera/orchestrators/abc/workers", `{"prompt":"x"}`)
	testutil.Equal(t, w.Code, http.StatusBadRequest)
}

func TestHeraMutation_NoLiveCoordinator(t *testing.T) {
	srv, d := testHeraAPIServer(t)
	orch, err := d.CreateHeraOrchestrator("headless", "")
	testutil.NoError(t, err)
	w := doHeraReq(srv, "POST", fmt.Sprintf("/api/hera/orchestrators/%d/workers", orch.ID), `{"prompt":"x"}`)
	testutil.Equal(t, w.Code, http.StatusConflict)
}

// --- spawn worker ---

func TestHeraSpawnWorker_Defaults(t *testing.T) {
	srv, d := testHeraAPIServer(t)
	orch, _, _ := seedAPICoordinator(t, d, "myorch")

	var captured hera.SpawnInput
	srv.heraSpawn = func(in hera.SpawnInput) (*hera.SpawnResult, error) {
		captured = in
		return &hera.SpawnResult{
			Task:    &model.Task{ID: "task-1", Name: in.BaseName, Status: model.StatusInProgress},
			Role:    &db.HeraRole{ID: 42, Name: in.BaseName, Kind: db.HeraKindWorker},
			Binding: &db.HeraBinding{ID: 7},
		}, nil
	}

	w := doHeraReq(srv, "POST", fmt.Sprintf("/api/hera/orchestrators/%d/workers", orch.ID),
		mustJSON(t, map[string]any{"prompt": "Implement the parser", "role_name": "parser-work"}))
	testutil.Equal(t, w.Code, http.StatusCreated)

	body := decodeBody(t, w)
	testutil.Equal(t, body["role_id"].(float64), float64(42))
	testutil.Equal(t, body["name"], "parser-work")
	testutil.Equal(t, body["kind"], "worker")
	testutil.Equal(t, body["project"], "test-project")
	testutil.Equal(t, body["argus_task_id"], "task-1")

	testutil.Equal(t, captured.Project, "test-project")
	testutil.Equal(t, captured.BaseName, "parser-work")
	testutil.Contains(t, captured.TaskPrompt, "coordinator \"coord\"")
	testutil.Contains(t, captured.TaskPrompt, "orchestrator \""+orch.Name+"\"")
	testutil.Equal(t, captured.RolePrompt, "Implement the parser")
}

func TestHeraSpawnWorker_MissingPrompt(t *testing.T) {
	srv, d := testHeraAPIServer(t)
	orch, _, _ := seedAPICoordinator(t, d, "myorch")
	srv.heraSpawn = func(hera.SpawnInput) (*hera.SpawnResult, error) { return &hera.SpawnResult{}, nil }

	w := doHeraReq(srv, "POST", fmt.Sprintf("/api/hera/orchestrators/%d/workers", orch.ID), `{}`)
	testutil.Equal(t, w.Code, http.StatusBadRequest)
}

func TestHeraSpawnWorker_UnknownBackendRejected(t *testing.T) {
	srv, d := testHeraAPIServer(t)
	orch, _, _ := seedAPICoordinator(t, d, "myorch")
	srv.heraSpawn = func(hera.SpawnInput) (*hera.SpawnResult, error) {
		return nil, fmt.Errorf("backend %q not found in config", "bogus")
	}

	w := doHeraReq(srv, "POST", fmt.Sprintf("/api/hera/orchestrators/%d/workers", orch.ID),
		mustJSON(t, map[string]any{"prompt": "x", "backend": "bogus"}))
	testutil.Equal(t, w.Code, http.StatusBadRequest)
}

func TestHeraSpawnWorker_NotConfigured(t *testing.T) {
	srv, d := testHeraAPIServer(t)
	orch, _, _ := seedAPICoordinator(t, d, "myorch")
	// srv.heraSpawn left nil.
	w := doHeraReq(srv, "POST", fmt.Sprintf("/api/hera/orchestrators/%d/workers", orch.ID), `{"prompt":"x"}`)
	testutil.Equal(t, w.Code, http.StatusServiceUnavailable)
}

// --- send message ---

func seedRecipient(t *testing.T, d *db.DB, orchID int64, name string) *db.HeraRole {
	t.Helper()
	role, err := d.CreateHeraRole(db.CreateHeraRoleInput{
		OrchestratorID: orchID, Name: name, Kind: db.HeraKindWorker, ArgusProject: "test-project",
	})
	testutil.NoError(t, err)
	return role
}

func TestHeraSendMessage_CoordinatorSendsToWorker(t *testing.T) {
	srv, d := testHeraAPIServer(t)
	orch, _, _ := seedAPICoordinator(t, d, "myorch")
	worker := seedRecipient(t, d, orch.ID, "w1")

	w := doHeraReq(srv, "POST", fmt.Sprintf("/api/hera/orchestrators/%d/messages", orch.ID),
		mustJSON(t, map[string]any{"to": worker.ID, "tldr": "status", "body": "please proceed"}))
	testutil.Equal(t, w.Code, http.StatusCreated)
	body := decodeBody(t, w)
	testutil.Equal(t, body["to_role_id"].(float64), float64(worker.ID))
}

func TestHeraSendMessage_MissingFields(t *testing.T) {
	srv, d := testHeraAPIServer(t)
	orch, _, _ := seedAPICoordinator(t, d, "myorch")
	w := doHeraReq(srv, "POST", fmt.Sprintf("/api/hera/orchestrators/%d/messages", orch.ID), `{}`)
	testutil.Equal(t, w.Code, http.StatusBadRequest)
}

func TestHeraSendMessage_TldrTooLong(t *testing.T) {
	srv, d := testHeraAPIServer(t)
	orch, _, _ := seedAPICoordinator(t, d, "myorch")
	worker := seedRecipient(t, d, orch.ID, "w1")
	longTldr := ""
	for i := 0; i < 130; i++ {
		longTldr += "x"
	}
	w := doHeraReq(srv, "POST", fmt.Sprintf("/api/hera/orchestrators/%d/messages", orch.ID),
		mustJSON(t, map[string]any{"to": worker.ID, "tldr": longTldr, "body": "b"}))
	testutil.Equal(t, w.Code, http.StatusBadRequest)
}

func TestHeraSendMessage_RecipientNotFound(t *testing.T) {
	srv, d := testHeraAPIServer(t)
	orch, _, _ := seedAPICoordinator(t, d, "myorch")
	w := doHeraReq(srv, "POST", fmt.Sprintf("/api/hera/orchestrators/%d/messages", orch.ID),
		mustJSON(t, map[string]any{"to": 999999, "tldr": "t", "body": "b"}))
	testutil.Equal(t, w.Code, http.StatusNotFound)
}

func TestHeraSendMessage_RecipientInDifferentOrchestrator(t *testing.T) {
	srv, d := testHeraAPIServer(t)
	orch, _, _ := seedAPICoordinator(t, d, "myorch")
	otherOrch, _, _ := seedAPICoordinator(t, d, "otherorch")
	otherWorker := seedRecipient(t, d, otherOrch.ID, "w-other")

	w := doHeraReq(srv, "POST", fmt.Sprintf("/api/hera/orchestrators/%d/messages", orch.ID),
		mustJSON(t, map[string]any{"to": otherWorker.ID, "tldr": "t", "body": "b"}))
	testutil.Equal(t, w.Code, http.StatusNotFound)
}

func TestHeraSendMessage_SelfSendRejected(t *testing.T) {
	srv, d := testHeraAPIServer(t)
	orch, coord, _ := seedAPICoordinator(t, d, "myorch")

	w := doHeraReq(srv, "POST", fmt.Sprintf("/api/hera/orchestrators/%d/messages", orch.ID),
		mustJSON(t, map[string]any{"to": coord.ID, "tldr": "t", "body": "b"}))
	testutil.Equal(t, w.Code, http.StatusConflict)
}

func TestHeraSendMessage_BodyTooLarge(t *testing.T) {
	srv, d := testHeraAPIServer(t)
	orch, _, _ := seedAPICoordinator(t, d, "myorch")
	worker := seedRecipient(t, d, orch.ID, "w1")

	big := make([]byte, model.MaxMessageBodyBytes+1)
	for i := range big {
		big[i] = 'a'
	}
	w := doHeraReq(srv, "POST", fmt.Sprintf("/api/hera/orchestrators/%d/messages", orch.ID),
		mustJSON(t, map[string]any{"to": worker.ID, "tldr": "t", "body": string(big)}))
	testutil.Equal(t, w.Code, http.StatusRequestEntityTooLarge)
}

func TestHeraSendMessage_NotConfigured(t *testing.T) {
	srv, d := testServer(t) // no SetHeraMutations at all
	orch, _, _ := seedAPICoordinator(t, d, "myorch")
	worker := seedRecipient(t, d, orch.ID, "w1")
	w := doHeraReq(srv, "POST", fmt.Sprintf("/api/hera/orchestrators/%d/messages", orch.ID),
		mustJSON(t, map[string]any{"to": worker.ID, "tldr": "t", "body": "b"}))
	testutil.Equal(t, w.Code, http.StatusServiceUnavailable)
}

// --- plan node create ---

func TestHeraPlanNodeCreate_Worker(t *testing.T) {
	srv, d := testHeraAPIServer(t)
	orch, _, _ := seedAPICoordinator(t, d, "myorch")

	w := doHeraReq(srv, "POST", fmt.Sprintf("/api/hera/orchestrators/%d/plan/nodes", orch.ID),
		mustJSON(t, map[string]any{"name": "1a-writer", "prompt": "write the doc"}))
	testutil.Equal(t, w.Code, http.StatusCreated)
	body := decodeBody(t, w)
	testutil.Equal(t, body["name"], "1a-writer")
	testutil.Equal(t, body["status"], "planned")
	testutil.Equal(t, body["project"], "test-project")
}

func TestHeraPlanNodeCreate_SubcoordRequiresGoal(t *testing.T) {
	srv, d := testHeraAPIServer(t)
	orch, _, _ := seedAPICoordinator(t, d, "myorch")

	w := doHeraReq(srv, "POST", fmt.Sprintf("/api/hera/orchestrators/%d/plan/nodes", orch.ID),
		mustJSON(t, map[string]any{"name": "2a-subteam", "kind": "subcoord"}))
	testutil.Equal(t, w.Code, http.StatusBadRequest)
}

func TestHeraPlanNodeCreate_MissingName(t *testing.T) {
	srv, d := testHeraAPIServer(t)
	orch, _, _ := seedAPICoordinator(t, d, "myorch")
	w := doHeraReq(srv, "POST", fmt.Sprintf("/api/hera/orchestrators/%d/plan/nodes", orch.ID),
		mustJSON(t, map[string]any{"prompt": "x"}))
	testutil.Equal(t, w.Code, http.StatusBadRequest)
}

// --- plan (whole graph) ---

func TestHeraPlanCreate_WholeGraph(t *testing.T) {
	srv, d := testHeraAPIServer(t)
	orch, _, _ := seedAPICoordinator(t, d, "myorch")

	w := doHeraReq(srv, "POST", fmt.Sprintf("/api/hera/orchestrators/%d/plan", orch.ID),
		mustJSON(t, map[string]any{
			"nodes": []map[string]any{
				{"name": "1a", "prompt": "stage one a"},
				{"name": "1b", "prompt": "stage one b"},
				{"name": "2a", "prompt": "stage two a"},
			},
			"edges": []map[string]any{
				{"blocked": "2a", "blocker": "1a"},
				{"blocked": "2a", "blocker": "1b"},
			},
		}))
	testutil.Equal(t, w.Code, http.StatusCreated)
	body := decodeBody(t, w)
	testutil.Equal(t, body["nodes_created"].(float64), float64(3))
	testutil.Equal(t, body["edges_created"].(float64), float64(2))

	n2a, err := d.HeraRoleByName(orch.ID, "2a")
	testutil.NoError(t, err)
	blockers, err := d.HeraBlockersOf(n2a.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(blockers), 2)
}

func TestHeraPlanCreate_UnresolvableEdgeName(t *testing.T) {
	srv, d := testHeraAPIServer(t)
	orch, _, _ := seedAPICoordinator(t, d, "myorch")

	w := doHeraReq(srv, "POST", fmt.Sprintf("/api/hera/orchestrators/%d/plan", orch.ID),
		mustJSON(t, map[string]any{
			"nodes": []map[string]any{{"name": "a", "prompt": "p"}},
			"edges": []map[string]any{{"blocked": "a", "blocker": "ghost"}},
		}))
	testutil.Equal(t, w.Code, http.StatusBadRequest)

	// Nothing persisted — all-or-nothing.
	_, err := d.HeraRoleByName(orch.ID, "a")
	testutil.ErrorIs(t, err, db.ErrHeraNotFound)
}

func TestHeraPlanCreate_MissingNodes(t *testing.T) {
	srv, d := testHeraAPIServer(t)
	orch, _, _ := seedAPICoordinator(t, d, "myorch")
	w := doHeraReq(srv, "POST", fmt.Sprintf("/api/hera/orchestrators/%d/plan", orch.ID), `{}`)
	testutil.Equal(t, w.Code, http.StatusBadRequest)
}

// --- plan node update ---

func planNodeAPI(t *testing.T, d *db.DB, orchID int64, name string) *db.HeraRole {
	t.Helper()
	role, err := d.CreateHeraPlannedRole(db.CreateHeraRoleInput{
		OrchestratorID: orchID, Name: name, ArgusProject: "test-project", Prompt: "original",
	})
	testutil.NoError(t, err)
	return role
}

func TestHeraPlanNodeUpdate_EditsPrompt(t *testing.T) {
	srv, d := testHeraAPIServer(t)
	orch, _, _ := seedAPICoordinator(t, d, "myorch")
	role := planNodeAPI(t, d, orch.ID, "1a-writer")

	w := doHeraReq(srv, "PATCH", fmt.Sprintf("/api/hera/orchestrators/%d/plan/nodes/%d", orch.ID, role.ID),
		mustJSON(t, map[string]any{"prompt": "revised"}))
	testutil.Equal(t, w.Code, http.StatusOK)

	updated, err := d.HeraRole(role.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, updated.Prompt, "revised")
}

func TestHeraPlanNodeUpdate_EmptyRejected(t *testing.T) {
	srv, d := testHeraAPIServer(t)
	orch, _, _ := seedAPICoordinator(t, d, "myorch")
	role := planNodeAPI(t, d, orch.ID, "1a-writer")

	w := doHeraReq(srv, "PATCH", fmt.Sprintf("/api/hera/orchestrators/%d/plan/nodes/%d", orch.ID, role.ID), `{}`)
	testutil.Equal(t, w.Code, http.StatusBadRequest)
}

func TestHeraPlanNodeUpdate_MaterializedRejected(t *testing.T) {
	srv, d := testHeraAPIServer(t)
	orch, _, _ := seedAPICoordinator(t, d, "myorch")
	workerTask := &model.Task{Name: "wt", Project: "p", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(workerTask))
	role, _, err := d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "live-worker", Kind: db.HeraKindWorker, ArgusProject: "p", Prompt: "orig",
	}, workerTask.ID, "/tmp/w")
	testutil.NoError(t, err)

	w := doHeraReq(srv, "PATCH", fmt.Sprintf("/api/hera/orchestrators/%d/plan/nodes/%d", orch.ID, role.ID),
		mustJSON(t, map[string]any{"prompt": "new"}))
	testutil.Equal(t, w.Code, http.StatusConflict)
}

func TestHeraPlanNodeUpdate_UnknownRoleID(t *testing.T) {
	srv, d := testHeraAPIServer(t)
	orch, _, _ := seedAPICoordinator(t, d, "myorch")
	w := doHeraReq(srv, "PATCH", fmt.Sprintf("/api/hera/orchestrators/%d/plan/nodes/999999", orch.ID),
		mustJSON(t, map[string]any{"prompt": "new"}))
	testutil.Equal(t, w.Code, http.StatusNotFound)
}

// --- plan node cancel ---

func TestHeraPlanNodeCancel_CancelsPlannedNode(t *testing.T) {
	srv, d := testHeraAPIServer(t)
	orch, _, _ := seedAPICoordinator(t, d, "myorch")
	role := planNodeAPI(t, d, orch.ID, "1a-writer")

	w := doHeraReq(srv, "POST", fmt.Sprintf("/api/hera/orchestrators/%d/plan/nodes/%d/cancel", orch.ID, role.ID), "")
	testutil.Equal(t, w.Code, http.StatusOK)

	planned, err := d.ListHeraPlannedNodes()
	testutil.NoError(t, err)
	for _, p := range planned {
		if p.ID == role.ID {
			t.Fatalf("expected role %d to be excluded from planned nodes after cancel", role.ID)
		}
	}
}

func TestHeraPlanNodeCancel_MaterializedRejected(t *testing.T) {
	srv, d := testHeraAPIServer(t)
	orch, _, _ := seedAPICoordinator(t, d, "myorch")
	workerTask := &model.Task{Name: "wt2", Project: "p", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(workerTask))
	role, _, err := d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "live-worker-2", Kind: db.HeraKindWorker, ArgusProject: "p", Prompt: "orig",
	}, workerTask.ID, "/tmp/w2")
	testutil.NoError(t, err)

	w := doHeraReq(srv, "POST", fmt.Sprintf("/api/hera/orchestrators/%d/plan/nodes/%d/cancel", orch.ID, role.ID), "")
	testutil.Equal(t, w.Code, http.StatusConflict)
}

// --- blocks ---

func TestHeraBlockCreate_AddsEdge(t *testing.T) {
	srv, d := testHeraAPIServer(t)
	orch, _, _ := seedAPICoordinator(t, d, "myorch")
	a := planNodeAPI(t, d, orch.ID, "a")
	b := planNodeAPI(t, d, orch.ID, "b")

	w := doHeraReq(srv, "POST", fmt.Sprintf("/api/hera/orchestrators/%d/plan/blocks", orch.ID),
		mustJSON(t, map[string]any{"blocked_role_id": b.ID, "blocker_role_id": a.ID}))
	testutil.Equal(t, w.Code, http.StatusCreated)

	blockers, err := d.HeraBlockersOf(b.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(blockers), 1)
}

func TestHeraBlockCreate_CycleRejected(t *testing.T) {
	srv, d := testHeraAPIServer(t)
	orch, _, _ := seedAPICoordinator(t, d, "myorch")
	a := planNodeAPI(t, d, orch.ID, "a")
	b := planNodeAPI(t, d, orch.ID, "b")
	testutil.NoError(t, d.AddHeraBlock(b.ID, a.ID)) // b waits on a

	w := doHeraReq(srv, "POST", fmt.Sprintf("/api/hera/orchestrators/%d/plan/blocks", orch.ID),
		mustJSON(t, map[string]any{"blocked_role_id": a.ID, "blocker_role_id": b.ID}))
	testutil.Equal(t, w.Code, http.StatusConflict)
}

func TestHeraBlockCreate_CrossOrchestratorRejected(t *testing.T) {
	srv, d := testHeraAPIServer(t)
	orch, _, _ := seedAPICoordinator(t, d, "myorch")
	otherOrch, _, _ := seedAPICoordinator(t, d, "otherorch")
	a := planNodeAPI(t, d, orch.ID, "a")
	b := planNodeAPI(t, d, otherOrch.ID, "b")

	w := doHeraReq(srv, "POST", fmt.Sprintf("/api/hera/orchestrators/%d/plan/blocks", orch.ID),
		mustJSON(t, map[string]any{"blocked_role_id": a.ID, "blocker_role_id": b.ID}))
	testutil.Equal(t, w.Code, http.StatusBadRequest)
}

func TestHeraBlockDelete_Idempotent(t *testing.T) {
	srv, d := testHeraAPIServer(t)
	orch, _, _ := seedAPICoordinator(t, d, "myorch")
	a := planNodeAPI(t, d, orch.ID, "a")
	b := planNodeAPI(t, d, orch.ID, "b")

	// No edge exists yet — delete must still succeed.
	url := fmt.Sprintf("/api/hera/orchestrators/%d/plan/blocks?blocked_role_id=%d&blocker_role_id=%d", orch.ID, b.ID, a.ID)
	w := doHeraReq(srv, "DELETE", url, "")
	testutil.Equal(t, w.Code, http.StatusOK)

	testutil.NoError(t, d.AddHeraBlock(b.ID, a.ID))
	w = doHeraReq(srv, "DELETE", url, "")
	testutil.Equal(t, w.Code, http.StatusOK)
	blockers, err := d.HeraBlockersOf(b.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(blockers), 0)
}

func TestHeraBlockDelete_MissingQueryParams(t *testing.T) {
	srv, d := testHeraAPIServer(t)
	orch, _, _ := seedAPICoordinator(t, d, "myorch")
	w := doHeraReq(srv, "DELETE", fmt.Sprintf("/api/hera/orchestrators/%d/plan/blocks", orch.ID), "")
	testutil.Equal(t, w.Code, http.StatusBadRequest)
}

// --- auth ---

func TestHeraMutation_RequiresAuth(t *testing.T) {
	srv, d := testHeraAPIServer(t)
	orch, _, _ := seedAPICoordinator(t, d, "myorch")
	handler := authMiddleware(srv.token, srv.db, srv.push, srv.routes(), "/")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("POST", fmt.Sprintf("/api/hera/orchestrators/%d/workers", orch.ID), nil))
	testutil.Equal(t, w.Code, http.StatusUnauthorized)
}
