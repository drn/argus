package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// --- cleanupComputeState: the in-flight guard, tested in isolation so the
// exclusivity contract doesn't depend on classification timing. ---

func TestCleanupComputeState_ExclusiveWhileRunning(t *testing.T) {
	var st cleanupComputeState
	testutil.True(t, st.tryStart())
	testutil.True(t, st.isComputing())
	testutil.False(t, st.tryStart()) // second start while running is rejected
	st.finish()
	testutil.False(t, st.isComputing())
	testutil.True(t, st.tryStart())
}

// --- POST /api/maintenance/cleanup-candidates/compute ---

func TestHandleCleanupCandidatesCompute_StartsAndIsIdempotentWhileRunning(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	started := make(chan struct{}, 2)
	release := make(chan struct{})
	srv.cleanupComputeFn = func(ctx context.Context) {
		started <- struct{}{}
		<-release
	}

	req1 := authedReq("POST", "/api/maintenance/cleanup-candidates/compute", "")
	w1 := httptest.NewRecorder()
	mux.ServeHTTP(w1, req1)
	testutil.Equal(t, w1.Code, http.StatusOK)

	var resp1 map[string]any
	testutil.NoError(t, json.Unmarshal(w1.Body.Bytes(), &resp1))
	testutil.Equal(t, resp1["computing"], true)

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("compute pass did not start")
	}

	// A second call while the first pass is still blocked on release must be
	// a no-op: no second invocation of cleanupComputeFn.
	req2 := authedReq("POST", "/api/maintenance/cleanup-candidates/compute", "")
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	testutil.Equal(t, w2.Code, http.StatusOK)

	var resp2 map[string]any
	testutil.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp2))
	testutil.Equal(t, resp2["computing"], true)

	select {
	case <-started:
		t.Fatal("a second compute pass started while the first was in flight")
	case <-time.After(200 * time.Millisecond):
		// expected: no second start signal
	}

	close(release)
}

// --- GET /api/maintenance/cleanup-candidates ---

func TestHandleCleanupCandidatesList_ReturnsCachedResultsAndComputing(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()

	stuck := &model.Task{Name: "stuck", Status: model.StatusInReview, Archived: true, Project: "ghost"}
	testutil.NoError(t, d.Add(stuck))
	testutil.NoError(t, d.Add(&model.Task{Name: "active", Status: model.StatusInProgress}))

	req := authedReq("GET", "/api/maintenance/cleanup-candidates", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	var resp struct {
		Candidates []cleanupCandidateJSON `json:"candidates"`
		Computing  bool                   `json:"computing"`
	}
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	testutil.Equal(t, len(resp.Candidates), 1)
	testutil.Equal(t, resp.Candidates[0].TaskID, stuck.ID)
	testutil.False(t, resp.Candidates[0].Safe)
	testutil.True(t, resp.Candidates[0].Pending)
	testutil.Equal(t, resp.Candidates[0].Reason, "not yet classified")
	testutil.False(t, resp.Computing)

	// Cache a safe verdict directly (bypassing the classifier's network
	// boundary entirely) and confirm the list reflects it without any
	// compute call.
	testutil.NoError(t, d.SetMetaBatch(stuck.ID, cleanupMetaNamespace, map[string]string{
		cleanupMetaSafe:   "true",
		cleanupMetaTier:   "local-ancestor",
		cleanupMetaReason: `branch "x" is an ancestor of "master"`,
	}))

	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, authedReq("GET", "/api/maintenance/cleanup-candidates", ""))
	testutil.Equal(t, w2.Code, http.StatusOK)

	var resp2 struct {
		Candidates []cleanupCandidateJSON `json:"candidates"`
	}
	testutil.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp2))
	testutil.Equal(t, len(resp2.Candidates), 1)
	testutil.True(t, resp2.Candidates[0].Safe)
	testutil.False(t, resp2.Candidates[0].Pending)
	testutil.Equal(t, resp2.Candidates[0].Tier, "local-ancestor")
}

// --- runCleanupCompute: repo resolution + fail-closed classification,
// exercised end to end with zero real git/gh processes spawned. ---

// TestRunCleanupCompute_UnresolvableProjectClassifiesNotSafe proves the
// "task whose project row no longer exists" scenario (rest-api spec delta,
// tasks.md 3.10): cfg.Projects["ghost"] is the config zero value (empty
// Path), so cleanupCandidateFor leaves RepoDir/RepoSlug empty and
// mergesafety.ClassifyBatch's own fail-closed path takes over — no repo
// resolution or PR lookup is ever attempted.
func TestRunCleanupCompute_UnresolvableProjectClassifiesNotSafe(t *testing.T) {
	srv, d := testServer(t)

	stuck := &model.Task{Name: "orphan-project-task", Status: model.StatusInReview, Archived: true, Project: "ghost"}
	testutil.NoError(t, d.Add(stuck))

	srv.runCleanupCompute(context.Background())

	entries, err := d.ListMeta(stuck.ID, cleanupMetaNamespace)
	testutil.NoError(t, err)
	got := map[string]string{}
	for _, e := range entries {
		got[e.Key] = e.Value
	}
	testutil.Equal(t, got[cleanupMetaSafe], "false")
	testutil.Contains(t, got[cleanupMetaReason], "no repo resolvable")
}

// TestRunCleanupCompute_NeverReclassifiesTerminalSafeVerdict proves the
// "safe verdicts are cached as terminal" scenario: a task already cached
// safe=true is left untouched by a later compute pass even though its
// (bogus, nonexistent) project path would, if freshly evaluated, produce a
// not-safe "no repo resolvable" verdict instead — the only way the cached
// value could survive is if compute skipped reclassifying it entirely.
func TestRunCleanupCompute_NeverReclassifiesTerminalSafeVerdict(t *testing.T) {
	srv, d := testServer(t)

	stuck := &model.Task{Name: "already-safe", Status: model.StatusInReview, Archived: true, Project: "ghost"}
	testutil.NoError(t, d.Add(stuck))
	testutil.NoError(t, d.SetMetaBatch(stuck.ID, cleanupMetaNamespace, map[string]string{
		cleanupMetaSafe:   "true",
		cleanupMetaTier:   "merged-pr",
		cleanupMetaReason: "previously confirmed merged",
	}))

	srv.runCleanupCompute(context.Background())

	entries, err := d.ListMeta(stuck.ID, cleanupMetaNamespace)
	testutil.NoError(t, err)
	got := map[string]string{}
	for _, e := range entries {
		got[e.Key] = e.Value
	}
	testutil.Equal(t, got[cleanupMetaSafe], "true")
	testutil.Equal(t, got[cleanupMetaReason], "previously confirmed merged")
}

// TestRunCleanupCompute_CachesEachTaskIndividually proves the
// fix-hera-reclaim-status wiring: runCleanupCompute drives
// mergesafety.ClassifyBatchFunc (the incremental per-candidate form) rather
// than the old all-at-once ClassifyBatch, writing EACH task's own cache entry
// as its own verdict arrives rather than a single shared write after the
// whole batch finishes. Multiple tasks in one compute pass each end up with
// their own distinct, correctly-keyed cache row — not cross-contaminated by
// another task's outcome or a shared/batched write.
func TestRunCleanupCompute_CachesEachTaskIndividually(t *testing.T) {
	srv, d := testServer(t)

	taskA := &model.Task{Name: "orphan-a", Status: model.StatusInReview, Archived: true, Project: "ghost", Branch: "argus/a"}
	taskB := &model.Task{Name: "orphan-b", Status: model.StatusInReview, Archived: true, Project: "ghost", Branch: "argus/b"}
	testutil.NoError(t, d.Add(taskA))
	testutil.NoError(t, d.Add(taskB))

	srv.runCleanupCompute(context.Background())

	for _, task := range []*model.Task{taskA, taskB} {
		entries, err := d.ListMeta(task.ID, cleanupMetaNamespace)
		testutil.NoError(t, err)
		got := map[string]string{}
		for _, e := range entries {
			got[e.Key] = e.Value
		}
		testutil.Equal(t, got[cleanupMetaSafe], "false")
		testutil.Contains(t, got[cleanupMetaReason], "no repo resolvable")
	}
}

// --- POST /api/maintenance/cleanup-candidates/clean ---

func TestHandleCleanupCandidatesClean_RejectsNonMaster(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	req := deviceReq("POST", "/api/maintenance/cleanup-candidates/clean", `{"scope":"all"}`)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusForbidden)
}

func TestHandleCleanupCandidatesClean_RejectsInvalidScope(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	req := masterReq("POST", "/api/maintenance/cleanup-candidates/clean", `{"scope":"bogus"}`)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusBadRequest)
}

// seedStuckTask adds an archived+in_review task with no live Hera binding
// (i.e. one matching the stuck-task predicate) and, when verdict is
// non-empty, caches a classification for it.
func seedStuckTask(t *testing.T, d *db.DB, name string, safe bool, cached bool) *model.Task {
	t.Helper()
	task := &model.Task{Name: name, Status: model.StatusInReview, Archived: true}
	testutil.NoError(t, d.Add(task))
	if cached {
		testutil.NoError(t, d.SetMetaBatch(task.ID, cleanupMetaNamespace, map[string]string{
			cleanupMetaSafe:   boolStr(safe),
			cleanupMetaTier:   "local-ancestor",
			cleanupMetaReason: "test-seeded",
		}))
	}
	return task
}

func TestHandleCleanupCandidatesClean_SafeScopeOnlyDeletesConfirmedSafe(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // isolate db.DataDir() from real ~/.argus

	srv, d := testServer(t)
	mux := srv.routes()

	safeTask := seedStuckTask(t, d, "safe-one", true, true)
	notSafeTask := seedStuckTask(t, d, "not-safe-one", false, true)

	req := masterReq("POST", "/api/maintenance/cleanup-candidates/clean", `{"scope":"safe"}`)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	var resp map[string]any
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	testutil.Equal(t, resp["cleaned"].(float64), float64(1))

	_, err := d.Get(safeTask.ID)
	testutil.Error(t, err) // deleted
	still, err := d.Get(notSafeTask.ID)
	testutil.NoError(t, err) // untouched
	testutil.Equal(t, still.Status, model.StatusInReview)
}

func TestHandleCleanupCandidatesClean_AllScopeDeletesEveryCandidate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	srv, d := testServer(t)
	mux := srv.routes()

	safeTask := seedStuckTask(t, d, "safe-two", true, true)
	notSafeTask := seedStuckTask(t, d, "not-safe-two", false, true)

	req := masterReq("POST", "/api/maintenance/cleanup-candidates/clean", `{"scope":"all"}`)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	var resp map[string]any
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	testutil.Equal(t, resp["cleaned"].(float64), float64(2))

	_, err := d.Get(safeTask.ID)
	testutil.Error(t, err)
	_, err = d.Get(notSafeTask.ID)
	testutil.Error(t, err)
}

// TestHandleCleanupCandidatesClean_SkipsTaskNoLongerMatchingPredicate proves
// the re-verification requirement: a task cached safe (as if reviewed in a
// prior GET) that has since stopped matching the stuck-task predicate (its
// status moved on) is skipped, not deleted, and the rest of the batch is
// unaffected.
func TestHandleCleanupCandidatesClean_SkipsTaskNoLongerMatchingPredicate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	srv, d := testServer(t)
	mux := srv.routes()

	staleTask := seedStuckTask(t, d, "went-stale", true, true)
	testutil.NoError(t, d.SetStatus(staleTask.ID, model.StatusComplete)) // no longer in_review
	stillStuck := seedStuckTask(t, d, "still-stuck", true, true)

	req := masterReq("POST", "/api/maintenance/cleanup-candidates/clean", `{"scope":"all"}`)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	var resp map[string]any
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	testutil.Equal(t, resp["cleaned"].(float64), float64(1))

	stale, err := d.Get(staleTask.ID)
	testutil.NoError(t, err) // survived — no longer a candidate
	testutil.Equal(t, stale.Status, model.StatusComplete)
	_, err = d.Get(stillStuck.ID)
	testutil.Error(t, err) // deleted
}

// TestHandleCleanupCandidatesClean_ActsOnCachedSnapshotNotFreshClassification
// proves clean never re-runs classification: the task's project points at a
// nonexistent path, which — if freshly (re-)classified — would necessarily
// fail closed to "no repo resolvable" (not-safe). Because clean trusts the
// cached safe=true verdict instead, scope=safe still deletes it.
func TestHandleCleanupCandidatesClean_ActsOnCachedSnapshotNotFreshClassification(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	srv, d := testServer(t)
	mux := srv.routes()

	task := &model.Task{Name: "cached-safe-bogus-repo", Status: model.StatusInReview, Archived: true, Project: "ghost"}
	testutil.NoError(t, d.Add(task))
	testutil.NoError(t, d.SetMetaBatch(task.ID, cleanupMetaNamespace, map[string]string{
		cleanupMetaSafe:   "true",
		cleanupMetaTier:   "merged-pr",
		cleanupMetaReason: "cached from an earlier compute pass",
	}))

	req := masterReq("POST", "/api/maintenance/cleanup-candidates/clean", `{"scope":"safe"}`)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	var resp map[string]any
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	testutil.Equal(t, resp["cleaned"].(float64), float64(1))

	_, err := d.Get(task.ID)
	testutil.Error(t, err)
}

// TestHandleCleanupCandidatesClean_NoMatchingCandidatesDoesNotFallBackToDefaultSweep
// guards the landmine in agent.PrunePrepare: an empty TaskIDs slice falls
// back to the default all-status=complete sweep. When nothing in the
// requested scope qualifies, clean must short-circuit before ever calling
// PrunePrepare, or an unrelated completed task elsewhere in the system would
// be silently deleted by a request that named no candidates at all.
func TestHandleCleanupCandidatesClean_NoMatchingCandidatesDoesNotFallBackToDefaultSweep(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	srv, d := testServer(t)
	mux := srv.routes()

	unrelated := &model.Task{Name: "unrelated-complete", Status: model.StatusComplete}
	testutil.NoError(t, d.Add(unrelated))
	// One stuck task exists, but it has no cached verdict at all, so it
	// cannot match scope=safe.
	seedStuckTask(t, d, "uncached", false, false)

	req := masterReq("POST", "/api/maintenance/cleanup-candidates/clean", `{"scope":"safe"}`)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	var resp map[string]any
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	testutil.Equal(t, resp["cleaned"].(float64), float64(0))

	still, err := d.Get(unrelated.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, still.Status, model.StatusComplete)
}
