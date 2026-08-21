package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// getHera runs GET /api/hera through the mux and decodes the response.
func getHera(t *testing.T, srv *Server) heraJSON {
	t.Helper()
	w := httptest.NewRecorder()
	srv.routes().ServeHTTP(w, authedReq("GET", "/api/hera", ""))
	testutil.Equal(t, w.Code, http.StatusOK)
	var resp heraJSON
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp
}

func TestHandleHera_Empty(t *testing.T) {
	srv, _ := testServer(t)
	resp := getHera(t, srv)
	testutil.Equal(t, len(resp.Orchestrators), 0)
	testutil.Equal(t, len(resp.Freelance), 0)
}

func TestHandleHera_OrchestratorWithBoundCoordinator(t *testing.T) {
	srv, d := testServer(t)

	task := &model.Task{Name: "build the thing", Project: "p", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))
	testutil.True(t, task.ID != "")

	orch, err := d.CreateHeraOrchestrator("ship-it", "")
	testutil.NoError(t, err)

	role, _, err := d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID,
		Name:           "coord",
		Kind:           db.HeraKindCoordinator,
		ArgusProject:   "p",
	}, task.ID, "/tmp/wt")
	testutil.NoError(t, err)
	testutil.NoError(t, d.UpsertHeraRoleStatus(role.ID, db.HeraStatusWorking))
	// Flag the bound task ready_to_close so the field round-trips.
	testutil.NoError(t, d.SetMeta(task.ID, db.HeraMetaNamespace, db.HeraMetaKeyReadyToClose, "true"))

	// An unbound worker role: exercises the worker kind round-trip and the
	// buildHeraRoleJSON unbound branch (Live=false, empty task fields).
	_, err = d.CreateHeraRole(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID,
		Name:           "worker-1",
		Kind:           db.HeraKindWorker,
		ArgusProject:   "p",
	})
	testutil.NoError(t, err)

	resp := getHera(t, srv)
	testutil.Equal(t, len(resp.Orchestrators), 1)
	oj := resp.Orchestrators[0]
	testutil.Equal(t, oj.Name, "ship-it")
	testutil.Equal(t, oj.Pinned, false)
	testutil.Equal(t, oj.Archived, false)
	testutil.Equal(t, len(oj.Roles), 2)

	var coord, worker *heraRoleJSON
	for i := range oj.Roles {
		switch oj.Roles[i].Kind {
		case "coordinator":
			coord = &oj.Roles[i]
		case "worker":
			worker = &oj.Roles[i]
		}
	}
	testutil.NotNil(t, coord)
	testutil.NotNil(t, worker)

	testutil.Equal(t, coord.Status, "working")
	testutil.Equal(t, coord.Live, true)
	testutil.Equal(t, coord.TaskID, task.ID)
	testutil.Equal(t, coord.TaskName, "build the thing")
	testutil.Equal(t, coord.TaskStatus, model.StatusInProgress.String())
	testutil.Equal(t, coord.ReadyToClose, true)
	testutil.Equal(t, coord.Archived, false)

	// Worker is unbound: kind round-trips, no live binding, empty task fields.
	testutil.Equal(t, worker.Name, "worker-1")
	testutil.Equal(t, worker.Live, false)
	testutil.Equal(t, worker.TaskID, "")
	testutil.Equal(t, worker.TaskName, "")
	testutil.Equal(t, worker.ReadyToClose, false)
}

// TestHandleHera_KanbanStatus pins add-hera-kanban-status: the orchestrator
// envelope's kanban_status field defaults to "active" and reflects an
// explicit value, regardless of nesting (the endpoint does not resolve
// canonical parents — see the rest-api delta spec).
func TestHandleHera_KanbanStatus(t *testing.T) {
	srv, d := testServer(t)

	_, err := d.CreateHeraOrchestrator("kb-default", "")
	testutil.NoError(t, err)
	blocked, err := d.CreateHeraOrchestrator("kb-blocked", "")
	testutil.NoError(t, err)
	testutil.NoError(t, d.SetHeraOrchestratorKanbanStatus(blocked.ID, db.HeraKanbanBlocked))

	resp := getHera(t, srv)
	testutil.Equal(t, len(resp.Orchestrators), 2)

	var gotDefault, gotBlocked *heraOrchJSON
	for i := range resp.Orchestrators {
		switch resp.Orchestrators[i].Name {
		case "kb-default":
			gotDefault = &resp.Orchestrators[i]
		case "kb-blocked":
			gotBlocked = &resp.Orchestrators[i]
		}
	}
	testutil.NotNil(t, gotDefault)
	testutil.NotNil(t, gotBlocked)
	testutil.Equal(t, gotDefault.KanbanStatus, "active")
	testutil.Equal(t, gotBlocked.KanbanStatus, "blocked")
}

func TestHandleHera_FreelanceHoistedAndPinArchiveFlags(t *testing.T) {
	srv, d := testServer(t)

	// Pinned orchestrator with a freelance role (active) — the freelance role
	// must be hoisted to the top-level Freelance list, not nested.
	pinned, err := d.CreateHeraOrchestrator("pinned-orch", "")
	testutil.NoError(t, err)
	testutil.NoError(t, d.PinHeraOrchestrator(pinned.ID))
	_, err = d.CreateHeraRole(db.CreateHeraRoleInput{
		OrchestratorID: pinned.ID,
		Name:           "free-1",
		Kind:           db.HeraKindFreelance,
		ArgusProject:   "p",
	})
	testutil.NoError(t, err)

	// Archived orchestrator — surfaces with Archived=true.
	arch, err := d.CreateHeraOrchestrator("old-orch", "")
	testutil.NoError(t, err)
	testutil.NoError(t, d.ArchiveHeraOrchestrator(arch.ID))

	resp := getHera(t, srv)

	var pinnedView, archView *heraOrchJSON
	for i := range resp.Orchestrators {
		switch resp.Orchestrators[i].Name {
		case "pinned-orch":
			pinnedView = &resp.Orchestrators[i]
		case "old-orch":
			archView = &resp.Orchestrators[i]
		}
	}
	testutil.NotNil(t, pinnedView)
	testutil.NotNil(t, archView)
	testutil.Equal(t, pinnedView.Pinned, true)
	testutil.Equal(t, len(pinnedView.Roles), 0) // freelance hoisted out
	testutil.Equal(t, archView.Archived, true)

	testutil.Equal(t, len(resp.Freelance), 1)
	testutil.Equal(t, resp.Freelance[0].Name, "free-1")
	testutil.Equal(t, resp.Freelance[0].Kind, "freelance")
	testutil.Equal(t, resp.Freelance[0].Live, false)
}

// TestHandleHera_FreelanceHoistSuppression pins the two cases where a
// freelance-kind role must STAY nested in its orchestrator's roles rather than
// being hoisted to the top-level Freelance list, mirroring BuildModel's
// condition (hoist only when role active AND orch active).
func TestHandleHera_FreelanceHoistSuppression(t *testing.T) {
	srv, d := testServer(t)

	// (1) Active orchestrator with an ARCHIVED freelance role → stays nested.
	act, err := d.CreateHeraOrchestrator("active-orch", "")
	testutil.NoError(t, err)
	archRole, err := d.CreateHeraRole(db.CreateHeraRoleInput{
		OrchestratorID: act.ID,
		Name:           "free-archived",
		Kind:           db.HeraKindFreelance,
		ArgusProject:   "p",
	})
	testutil.NoError(t, err)
	testutil.NoError(t, d.ArchiveHeraRole(archRole.ID))

	// (2) Archived orchestrator with an ACTIVE freelance role → stays nested.
	arch, err := d.CreateHeraOrchestrator("archived-orch", "")
	testutil.NoError(t, err)
	_, err = d.CreateHeraRole(db.CreateHeraRoleInput{
		OrchestratorID: arch.ID,
		Name:           "free-in-archived",
		Kind:           db.HeraKindFreelance,
		ArgusProject:   "p",
	})
	testutil.NoError(t, err)
	testutil.NoError(t, d.ArchiveHeraOrchestrator(arch.ID))

	resp := getHera(t, srv)

	// Neither freelance role was hoisted.
	testutil.Equal(t, len(resp.Freelance), 0)

	var actView, archView *heraOrchJSON
	for i := range resp.Orchestrators {
		switch resp.Orchestrators[i].Name {
		case "active-orch":
			actView = &resp.Orchestrators[i]
		case "archived-orch":
			archView = &resp.Orchestrators[i]
		}
	}
	testutil.NotNil(t, actView)
	testutil.NotNil(t, archView)

	testutil.Equal(t, len(actView.Roles), 1)
	testutil.Equal(t, actView.Roles[0].Name, "free-archived")
	testutil.Equal(t, actView.Roles[0].Archived, true)

	testutil.Equal(t, archView.Archived, true)
	testutil.Equal(t, len(archView.Roles), 1)
	testutil.Equal(t, archView.Roles[0].Name, "free-in-archived")
}

// dropHeraTable removes a table so a specific store read inside handleHera
// fails while earlier reads still succeed — letting each 500 branch be covered
// independently rather than only the first (closed-DB) one.
func dropHeraTable(t *testing.T, d *db.DB, table string) {
	t.Helper()
	testutil.NoError(t, d.WithTx(func(tx *sql.Tx) error {
		_, err := tx.Exec("DROP TABLE " + table)
		return err
	}))
}

// TestHandleHera_DBError covers every 500 branch in handleHera: the first read
// (ListHeraOrchestrators) via a closed DB, the bindings read, and the
// per-orchestrator roles read inside the loop.
func TestHandleHera_DBError(t *testing.T) {
	t.Run("orchestrators read fails (closed DB)", func(t *testing.T) {
		srv, d := testServer(t)
		testutil.NoError(t, d.Close())
		w := httptest.NewRecorder()
		srv.routes().ServeHTTP(w, authedReq("GET", "/api/hera", ""))
		testutil.Equal(t, w.Code, http.StatusInternalServerError)
	})

	t.Run("bindings read fails", func(t *testing.T) {
		srv, d := testServer(t)
		// Orchestrators table intact (read succeeds), bindings table gone.
		dropHeraTable(t, d, "hera_bindings")
		w := httptest.NewRecorder()
		srv.routes().ServeHTTP(w, authedReq("GET", "/api/hera", ""))
		testutil.Equal(t, w.Code, http.StatusInternalServerError)
	})

	t.Run("per-orchestrator roles read fails", func(t *testing.T) {
		srv, d := testServer(t)
		// An orchestrator must exist so the loop body (ListHeraRoles) runs;
		// bindings stay intact so the read reaches the roles call.
		_, err := d.CreateHeraOrchestrator("orch", "")
		testutil.NoError(t, err)
		dropHeraTable(t, d, "hera_roles")
		w := httptest.NewRecorder()
		srv.routes().ServeHTTP(w, authedReq("GET", "/api/hera", ""))
		testutil.Equal(t, w.Code, http.StatusInternalServerError)
	})
}

// TestHandleHera_CostFieldsPopulated pins add-coordinator-cost-estimate's
// GET /api/hera contract: per-role token/cost fields and the orchestrator's
// subtree_cost_usd are sourced directly from persisted values, with no
// rate-table lookup or repricing performed by this endpoint.
func TestHandleHera_CostFieldsPopulated(t *testing.T) {
	srv, d := testServer(t)
	task := &model.Task{Name: "cost-role"}
	testutil.NoError(t, d.Add(task))
	orch, err := d.CreateHeraOrchestrator("costs", "")
	testutil.NoError(t, err)
	role, binding, err := d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "worker-1", Kind: db.HeraKindWorker, ArgusProject: "p",
	}, task.ID, "/tmp/wt")
	testutil.NoError(t, err)
	testutil.NoError(t, d.UpdateHeraBindingCostTotals(binding.ID, db.HeraBindingCostTotals{
		TokensInput: 10, TokensCacheWrite1h: 20, TokensCacheWrite5m: 30, TokensCacheRead: 40, TokensOutput: 50,
		CostUSDAccrued: 3.5,
	}))
	_ = role

	resp := getHera(t, srv)
	testutil.Equal(t, len(resp.Orchestrators), 1)
	oj := resp.Orchestrators[0]
	testutil.Equal(t, len(oj.Roles), 1)
	rj := oj.Roles[0]
	testutil.Equal(t, rj.TokensInput, int64(10))
	testutil.Equal(t, rj.TokensCacheWrite1h, int64(20))
	testutil.Equal(t, rj.TokensCacheWrite5m, int64(30))
	testutil.Equal(t, rj.TokensCacheRead, int64(40))
	testutil.Equal(t, rj.TokensOutput, int64(50))
	testutil.Equal(t, rj.CostUSD, 3.5)
	testutil.Equal(t, oj.SubtreeCostUSD, 3.5)
}

// TestHandleHera_UnmeasuredRoleOmitsCostFields pins Decision 6: an unmeasured
// role's cost/token fields are omitted, not a fabricated $0.00.
func TestHandleHera_UnmeasuredRoleOmitsCostFields(t *testing.T) {
	srv, d := testServer(t)
	_, err := d.CreateHeraRole(db.CreateHeraRoleInput{
		OrchestratorID: mustOrch(t, d, "orch"), Name: "worker-1", Kind: db.HeraKindWorker, ArgusProject: "p",
	})
	testutil.NoError(t, err)

	resp := getHera(t, srv)
	oj := resp.Orchestrators[0]
	rj := oj.Roles[0]
	testutil.Equal(t, rj.CostUSD, 0.0)
	testutil.Equal(t, oj.SubtreeCostUSD, 0.0)

	// Confirm the JSON literally omits the field rather than emitting 0.
	body := getHeraRaw(t, srv)
	if strings.Contains(body, `"cost_usd"`) {
		t.Errorf("expected cost_usd to be omitted for an unmeasured role, got body=%s", body)
	}
}

// TestHandleHera_SubtreeCostIncludesNukedRole pins the deliberate divergence
// from every other rollup on this endpoint: a nuked role's recorded cost
// still counts toward its orchestrator's subtree_cost_usd.
func TestHandleHera_SubtreeCostIncludesNukedRole(t *testing.T) {
	srv, d := testServer(t)
	orchID := mustOrch(t, d, "orch")
	nukedRole, err := d.CreateHeraRole(db.CreateHeraRoleInput{
		OrchestratorID: orchID, Name: "since-nuked", Kind: db.HeraKindWorker, ArgusProject: "p",
	})
	testutil.NoError(t, err)
	task := &model.Task{Name: "nuked-task"}
	testutil.NoError(t, d.Add(task))
	binding, err := d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: nukedRole.ID, ArgusTaskID: task.ID, WorktreePath: "/tmp/wt"})
	testutil.NoError(t, err)
	testutil.NoError(t, d.UpdateHeraBindingCostTotals(binding.ID, db.HeraBindingCostTotals{CostUSDAccrued: 7.0}))
	testutil.NoError(t, d.NukeHeraRole(nukedRole.ID))

	resp := getHera(t, srv)
	testutil.Equal(t, len(resp.Orchestrators), 1)
	oj := resp.Orchestrators[0]
	testutil.Equal(t, len(oj.Roles), 0) // the nuked role itself never renders
	testutil.Equal(t, oj.SubtreeCostUSD, 7.0)
}

func mustOrch(t *testing.T, d *db.DB, name string) int64 {
	t.Helper()
	o, err := d.CreateHeraOrchestrator(name, "")
	testutil.NoError(t, err)
	return o.ID
}

// getHeraRaw runs GET /api/hera and returns the raw response body, for
// asserting on literal field presence/absence (omitempty behavior).
func getHeraRaw(t *testing.T, srv *Server) string {
	t.Helper()
	w := httptest.NewRecorder()
	srv.routes().ServeHTTP(w, authedReq("GET", "/api/hera", ""))
	testutil.Equal(t, w.Code, http.StatusOK)
	return w.Body.String()
}

// TestHandleHera_TopLevelOrchestratorHasNoBridgeParent pins the
// add-mac-hera-rail-toggle rest-api delta: an orchestrator with no worker role
// bridging into it from another orchestrator carries null bridge_parent_orch_id
// and bridge_parent_role_id.
func TestHandleHera_TopLevelOrchestratorHasNoBridgeParent(t *testing.T) {
	srv, d := testServer(t)
	_, err := d.CreateHeraOrchestrator("solo", "")
	testutil.NoError(t, err)

	resp := getHera(t, srv)
	testutil.Equal(t, len(resp.Orchestrators), 1)
	oj := resp.Orchestrators[0]
	testutil.Nil(t, oj.BridgeParentOrchID)
	testutil.Nil(t, oj.BridgeParentRoleID)
}

// TestHandleHera_NestedOrchestratorSurfacesBridgeParent pins the worker-bridge
// nesting shape: a worker role in the PARENT orchestrator and the CHILD
// orchestrator's coordinator role share the same bound task, so the child
// nests under the parent's worker role. bridge_parent_orch_id/role_id must
// identify that parent orchestrator and role.
func TestHandleHera_NestedOrchestratorSurfacesBridgeParent(t *testing.T) {
	srv, d := testServer(t)

	parent, err := d.CreateHeraOrchestrator("parent", "")
	testutil.NoError(t, err)
	child, err := d.CreateHeraOrchestrator("child", "")
	testutil.NoError(t, err)

	bridgeWorker, err := d.CreateHeraRole(db.CreateHeraRoleInput{
		OrchestratorID: parent.ID, Name: "bridge-worker", Kind: db.HeraKindWorker, ArgusProject: "p",
	})
	testutil.NoError(t, err)
	childCoord, err := d.CreateHeraRole(db.CreateHeraRoleInput{
		OrchestratorID: child.ID, Name: "coord", Kind: db.HeraKindCoordinator, ArgusProject: "p",
	})
	testutil.NoError(t, err)

	const sharedTask = "bridge-task"
	testutil.NoError(t, d.Add(&model.Task{ID: sharedTask, Name: sharedTask, Status: model.StatusInProgress}))
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: bridgeWorker.ID, ArgusTaskID: sharedTask, WorktreePath: "/wt/a"})
	testutil.NoError(t, err)
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: childCoord.ID, ArgusTaskID: sharedTask, WorktreePath: "/wt/b"})
	testutil.NoError(t, err)

	resp := getHera(t, srv)
	var parentJSON, childJSON *heraOrchJSON
	for i := range resp.Orchestrators {
		switch resp.Orchestrators[i].Name {
		case "parent":
			parentJSON = &resp.Orchestrators[i]
		case "child":
			childJSON = &resp.Orchestrators[i]
		}
	}
	testutil.NotNil(t, parentJSON)
	testutil.NotNil(t, childJSON)

	testutil.Nil(t, parentJSON.BridgeParentOrchID)
	testutil.NotNil(t, childJSON.BridgeParentOrchID)
	testutil.NotNil(t, childJSON.BridgeParentRoleID)
	testutil.Equal(t, *childJSON.BridgeParentOrchID, parent.ID)
	testutil.Equal(t, *childJSON.BridgeParentRoleID, bridgeWorker.ID)
}

// TestHandleHera_RoleNeedsInputMirrorsRunnerSignal pins that a role's
// needs_input field mirrors the same daemon-authoritative idle-detection
// signal (Server.runner.NeedsInputIDs) that backs GET /api/tasks and the SSE
// events stream — set directly on the runner here rather than driving a real
// PTY session.
func TestHandleHera_RoleNeedsInputMirrorsRunnerSignal(t *testing.T) {
	srv, d := testServer(t)
	orch, err := d.CreateHeraOrchestrator("orch", "")
	testutil.NoError(t, err)
	role, _, err := d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "worker-1", Kind: db.HeraKindWorker, ArgusProject: "p",
	}, "blocked-task", "/tmp/wt")
	testutil.NoError(t, err)
	other, _, err := d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "worker-2", Kind: db.HeraKindWorker, ArgusProject: "p",
	}, "fine-task", "/tmp/wt2")
	testutil.NoError(t, err)
	_ = role
	_ = other

	srv.runner.SetNeedsInputIDs([]string{"blocked-task"})

	resp := getHera(t, srv)
	testutil.Equal(t, len(resp.Orchestrators), 1)
	var blocked, fine *heraRoleJSON
	for i := range resp.Orchestrators[0].Roles {
		switch resp.Orchestrators[0].Roles[i].Name {
		case "worker-1":
			blocked = &resp.Orchestrators[0].Roles[i]
		case "worker-2":
			fine = &resp.Orchestrators[0].Roles[i]
		}
	}
	testutil.NotNil(t, blocked)
	testutil.NotNil(t, fine)
	testutil.Equal(t, blocked.NeedsInput, true)
	testutil.Equal(t, fine.NeedsInput, false)
}

// TestHandleHera_SubtreeNeedsInputRollsUpAcrossBridge pins the orchestrator
// rollup: a leaf worker needing input in a NESTED sub-orchestrator (reached
// via a worker→coordinator bridge) makes both the child's own
// subtree_needs_input AND the parent's roll up to true.
func TestHandleHera_SubtreeNeedsInputRollsUpAcrossBridge(t *testing.T) {
	srv, d := testServer(t)

	parent, err := d.CreateHeraOrchestrator("parent", "")
	testutil.NoError(t, err)
	child, err := d.CreateHeraOrchestrator("child", "")
	testutil.NoError(t, err)

	bridgeWorker, err := d.CreateHeraRole(db.CreateHeraRoleInput{
		OrchestratorID: parent.ID, Name: "bridge-worker", Kind: db.HeraKindWorker, ArgusProject: "p",
	})
	testutil.NoError(t, err)
	childCoord, err := d.CreateHeraRole(db.CreateHeraRoleInput{
		OrchestratorID: child.ID, Name: "coord", Kind: db.HeraKindCoordinator, ArgusProject: "p",
	})
	testutil.NoError(t, err)
	childWorker, err := d.CreateHeraRole(db.CreateHeraRoleInput{
		OrchestratorID: child.ID, Name: "leaf", Kind: db.HeraKindWorker, ArgusProject: "p",
	})
	testutil.NoError(t, err)

	const bridgeTask = "bridge-task"
	const leafTask = "leaf-task"
	testutil.NoError(t, d.Add(&model.Task{ID: bridgeTask, Name: bridgeTask, Status: model.StatusInProgress}))
	testutil.NoError(t, d.Add(&model.Task{ID: leafTask, Name: leafTask, Status: model.StatusInProgress}))
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: bridgeWorker.ID, ArgusTaskID: bridgeTask, WorktreePath: "/wt/a"})
	testutil.NoError(t, err)
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: childCoord.ID, ArgusTaskID: bridgeTask, WorktreePath: "/wt/b"})
	testutil.NoError(t, err)
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: childWorker.ID, ArgusTaskID: leafTask, WorktreePath: "/wt/c"})
	testutil.NoError(t, err)

	srv.runner.SetNeedsInputIDs([]string{leafTask})

	resp := getHera(t, srv)
	var parentJSON, childJSON *heraOrchJSON
	for i := range resp.Orchestrators {
		switch resp.Orchestrators[i].Name {
		case "parent":
			parentJSON = &resp.Orchestrators[i]
		case "child":
			childJSON = &resp.Orchestrators[i]
		}
	}
	testutil.NotNil(t, parentJSON)
	testutil.NotNil(t, childJSON)
	testutil.Equal(t, childJSON.SubtreeNeedsInput, true)
	testutil.Equal(t, parentJSON.SubtreeNeedsInput, true)
}

// TestHandleHera_RequiresAuth pins that /api/hera sits behind the global auth
// middleware (it is not in the unauthenticated skip-paths list). Tests
// elsewhere drive srv.routes() directly (no auth), so wrap the mux in
// authMiddleware here — the same pattern sseTestServer uses.
func TestHandleHera_RequiresAuth(t *testing.T) {
	srv, _ := testServer(t)
	handler := authMiddleware(srv.token, srv.db, srv.push, srv.routes(), "/")

	// No Authorization header / token query param → 401.
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/hera", nil))
	testutil.Equal(t, w.Code, http.StatusUnauthorized)

	// With the bearer token → 200.
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, authedReq("GET", "/api/hera", ""))
	testutil.Equal(t, w.Code, http.StatusOK)
}
