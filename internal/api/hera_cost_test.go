package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func putHeraTokens(t *testing.T, srv *Server, taskID, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	srv.routes().ServeHTTP(w, authedReq("PUT", "/api/tasks/"+taskID+"/hera/tokens", body))
	return w
}

func mkBoundWorker(t *testing.T, d *db.DB, taskModel string) (task *model.Task, roleID int64) {
	t.Helper()
	task = &model.Task{Name: "cost-worker", Model: taskModel}
	testutil.NoError(t, d.Add(task))

	orch, err := d.CreateHeraOrchestrator("costs", "")
	testutil.NoError(t, err)
	role, _, err := d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID,
		Name:           "worker-1",
		Kind:           db.HeraKindWorker,
		ArgusProject:   "p",
	}, task.ID, "/tmp/wt")
	testutil.NoError(t, err)
	return task, role.ID
}

// libraryRatesEnv points ~/.argus (db.DataDir's HOME-derived root) at a fresh
// temp dir so InstallDefault's automatic install has somewhere safe to write
// during the test, per testing.md's t.Setenv("HOME", ...) rule for anything
// resolving through db.DataDir().
func libraryRatesEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

func TestHandleHeraTokensPut_PricesDeltaAgainstSeedRate(t *testing.T) {
	libraryRatesEnv(t)
	srv, d := testServer(t)
	task, roleID := mkBoundWorker(t, d, "sonnet")

	body := `{"tokens_input":1000000,"tokens_cache_write_1h":0,"tokens_cache_write_5m":0,"tokens_cache_read":0,"tokens_output":0}`
	w := putHeraTokens(t, srv, task.ID, body)
	testutil.Equal(t, w.Code, http.StatusOK)

	var resp map[string]float64
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	testutil.Equal(t, resp["cost_usd_accrued"], 3.0) // sonnet input rate: $3.00/Mtok in the seed

	sum, err := d.SumHeraRoleCostAccrued(roleID)
	testutil.NoError(t, err)
	testutil.Equal(t, sum, 3.0)
}

func TestHandleHeraTokensPut_DuplicateInvocationAddsNothing(t *testing.T) {
	libraryRatesEnv(t)
	srv, d := testServer(t)
	task, roleID := mkBoundWorker(t, d, "sonnet")

	body := `{"tokens_input":1000000,"tokens_cache_write_1h":0,"tokens_cache_write_5m":0,"tokens_cache_read":0,"tokens_output":0}`
	testutil.Equal(t, putHeraTokens(t, srv, task.ID, body).Code, http.StatusOK)
	testutil.Equal(t, putHeraTokens(t, srv, task.ID, body).Code, http.StatusOK) // same totals again

	sum, err := d.SumHeraRoleCostAccrued(roleID)
	testutil.NoError(t, err)
	testutil.Equal(t, sum, 3.0) // unchanged by the duplicate — zero delta
}

func TestHandleHeraTokensPut_RawTotalsAdvanceEvenWithNoRateEntry(t *testing.T) {
	libraryRatesEnv(t)
	srv, d := testServer(t)
	task, roleID := mkBoundWorker(t, d, "some-uncurated-custom-model")

	body := `{"tokens_input":1000000,"tokens_cache_write_1h":0,"tokens_cache_write_5m":0,"tokens_cache_read":0,"tokens_output":0}`
	w := putHeraTokens(t, srv, task.ID, body)
	testutil.Equal(t, w.Code, http.StatusOK)

	sum, err := d.SumHeraRoleCostAccrued(roleID)
	testutil.NoError(t, err)
	testutil.Equal(t, sum, 0.0) // never priced, not zero-priced

	binding, err := d.HeraLiveBindingByTask(task.ID)
	testutil.NoError(t, err)
	totals, err := d.GetHeraBindingCostTotals(binding.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, totals.TokensInput, int64(1_000_000)) // raw totals still advance
}

// TestHandleHeraTokensPut_RateChangeDoesNotRepriceAlreadyAccrued pins the
// core accrual-time-stamping correctness requirement (design.md Decision 2):
// a rate-table change affects only future deltas, never an already-accrued
// dollar figure.
func TestHandleHeraTokensPut_RateChangeDoesNotRepriceAlreadyAccrued(t *testing.T) {
	libraryRatesEnv(t)
	srv, d := testServer(t)
	task, roleID := mkBoundWorker(t, d, "sonnet")

	firstBody := `{"tokens_input":1000000,"tokens_cache_write_1h":0,"tokens_cache_write_5m":0,"tokens_cache_read":0,"tokens_output":0}`
	testutil.Equal(t, putHeraTokens(t, srv, task.ID, firstBody).Code, http.StatusOK)

	sumBefore, err := d.SumHeraRoleCostAccrued(roleID)
	testutil.NoError(t, err)
	testutil.Equal(t, sumBefore, 3.0)

	// Hand-edit the library rate table AFTER the first delta has already been
	// priced and accrued.
	libraryPath := filepath.Join(db.DataDir(), "rates.toml")
	testutil.NoError(t, os.WriteFile(libraryPath, []byte("[models.sonnet]\ninput = 999.0\n"), 0o644))

	// The already-accrued figure from the first delta must be untouched by
	// the rate change.
	sumStillBefore, err := d.SumHeraRoleCostAccrued(roleID)
	testutil.NoError(t, err)
	testutil.Equal(t, sumStillBefore, 3.0)

	// A NEW delta is priced at the NEW rate, not the old one.
	secondBody := `{"tokens_input":2000000,"tokens_cache_write_1h":0,"tokens_cache_write_5m":0,"tokens_cache_read":0,"tokens_output":0}`
	testutil.Equal(t, putHeraTokens(t, srv, task.ID, secondBody).Code, http.StatusOK)

	sumAfter, err := d.SumHeraRoleCostAccrued(roleID)
	testutil.NoError(t, err)
	testutil.Equal(t, sumAfter, 3.0+999.0) // 3.0 (untouched) + 1M new-delta tokens * $999/Mtok
}

func TestHandleHeraTokensPut_NoLiveBinding_NotFound(t *testing.T) {
	libraryRatesEnv(t)
	srv, d := testServer(t)
	task := &model.Task{Name: "unbound"}
	testutil.NoError(t, d.Add(task))

	w := putHeraTokens(t, srv, task.ID, `{}`)
	testutil.Equal(t, w.Code, http.StatusNotFound)
}

func TestHandleHeraTokensPut_UnknownTask_NotFound(t *testing.T) {
	libraryRatesEnv(t)
	srv, _ := testServer(t)
	w := putHeraTokens(t, srv, "does-not-exist", `{}`)
	testutil.Equal(t, w.Code, http.StatusNotFound)
}

func TestHandleHeraTokensPut_AmbiguousMultiBinding_Conflict(t *testing.T) {
	libraryRatesEnv(t)
	srv, d := testServer(t)
	task := &model.Task{Name: "double-bound"}
	testutil.NoError(t, d.Add(task))

	orchA, err := d.CreateHeraOrchestrator("a", "")
	testutil.NoError(t, err)
	orchB, err := d.CreateHeraOrchestrator("b", "")
	testutil.NoError(t, err)
	_, _, err = d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{OrchestratorID: orchA.ID, Name: "worker-1", Kind: db.HeraKindWorker, ArgusProject: "p"}, task.ID, "/tmp/a")
	testutil.NoError(t, err)
	_, _, err = d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{OrchestratorID: orchB.ID, Name: "coord", Kind: db.HeraKindCoordinator, ArgusProject: "p"}, task.ID, "/tmp/b")
	testutil.NoError(t, err)

	w := putHeraTokens(t, srv, task.ID, `{"tokens_input":100}`)
	testutil.Equal(t, w.Code, http.StatusConflict)
}

func TestHandleHeraTokensPut_RequiresAuth(t *testing.T) {
	libraryRatesEnv(t)
	srv, d := testServer(t)
	task, _ := mkBoundWorker(t, d, "sonnet")
	handler := authMiddleware(srv.token, srv.db, srv.push, srv.routes(), "/")

	w := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/tasks/"+task.ID+"/hera/tokens", strings.NewReader(`{}`))
	handler.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusUnauthorized)
}
