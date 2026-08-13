package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/mergesafety"
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

// TestHandleCleanupCandidatesList_IncludesOrchestratorName covers
// 5a-cleanup-tree-view: a candidate that once held a Hera role reports the
// orchestrator it belonged to (surviving the binding ending), while a plain
// non-Hera candidate reports none.
func TestHandleCleanupCandidatesList_IncludesOrchestratorName(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()

	heraTask := &model.Task{Name: "1a-build", Status: model.StatusInReview, Archived: true, Worktree: "/tmp/wt/1a-build"}
	testutil.NoError(t, d.Add(heraTask))
	orch, err := d.CreateHeraOrchestrator("fix-widget", "")
	testutil.NoError(t, err)
	_, binding, err := d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID,
		Name:           "1a-build",
		Kind:           db.HeraKindWorker,
		ArgusProject:   "p",
	}, heraTask.ID, heraTask.Worktree)
	testutil.NoError(t, err)
	testutil.NoError(t, d.EndHeraBinding(binding.ID, "test-teardown"))

	plainTask := &model.Task{Name: "plain-stuck", Status: model.StatusInReview, Archived: true}
	testutil.NoError(t, d.Add(plainTask))

	req := authedReq("GET", "/api/maintenance/cleanup-candidates", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	var resp struct {
		Candidates []cleanupCandidateJSON `json:"candidates"`
	}
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	testutil.Equal(t, len(resp.Candidates), 2)

	byID := map[string]cleanupCandidateJSON{}
	for _, c := range resp.Candidates {
		byID[c.TaskID] = c
	}
	testutil.Equal(t, byID[heraTask.ID].Orchestrator, "fix-widget")
	testutil.Equal(t, byID[plainTask.ID].Orchestrator, "")
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

// --- Tier C: coordinator-inferred safety (add-coordinator-inferred-safety) ---

// seedOrchestratorWithCoordinator creates a fresh orchestrator named orchName
// with a coordinator role bound to coordTask, and returns the orchestrator so
// callers can bind further roles (workers, or — for the one-hop-cap test — a
// second coordinator role elsewhere) to it.
func seedOrchestratorWithCoordinator(t *testing.T, d *db.DB, orchName string, coordTask *model.Task) *db.HeraOrchestrator {
	t.Helper()
	orch, err := d.CreateHeraOrchestrator(orchName, "")
	testutil.NoError(t, err)
	_, _, err = d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID,
		Name:           orchName + "-coord",
		Kind:           db.HeraKindCoordinator,
		ArgusProject:   "p",
	}, coordTask.ID, coordTask.Worktree)
	testutil.NoError(t, err)
	return orch
}

// seedOrchestratedWorker creates a stuck-task-predicate-matching task
// (archived, in_review, no live binding) whose most recent Hera role was a
// worker under orch.
func seedOrchestratedWorker(t *testing.T, d *db.DB, orch *db.HeraOrchestrator, name string) *model.Task {
	t.Helper()
	task := &model.Task{Name: name, Status: model.StatusInReview, Archived: true, Worktree: "/tmp/wt/" + name}
	testutil.NoError(t, d.Add(task))
	_, binding, err := d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID,
		Name:           name,
		Kind:           db.HeraKindWorker,
		ArgusProject:   "p",
	}, task.ID, task.Worktree)
	testutil.NoError(t, err)
	testutil.NoError(t, d.EndHeraBinding(binding.ID, "done"))
	return task
}

// metaMap collects a task's cleanup-namespace task_meta entries into a plain
// map for convenient assertion.
func metaMap(t *testing.T, d *db.DB, taskID string) map[string]string {
	t.Helper()
	entries, err := d.ListMeta(taskID, cleanupMetaNamespace)
	testutil.NoError(t, err)
	got := map[string]string{}
	for _, e := range entries {
		got[e.Key] = e.Value
	}
	return got
}

// TestRunCleanupCompute_CoordinatorInferredSafety_RescuesNotSafeWorker proves
// the core Tier C behavior: a worker task that classifies not-safe on its own
// (no resolvable project/repo, matching the "folded into the coordinator's
// branch via git merge, no standalone PR" scenario) is rescued when its
// coordinator's own task classifies confirmed-safe.
func TestRunCleanupCompute_CoordinatorInferredSafety_RescuesNotSafeWorker(t *testing.T) {
	srv, d := testServer(t)

	coordTask := &model.Task{Name: "coord-1a", Status: model.StatusInProgress, Worktree: "/tmp/wt/coord-1a"}
	testutil.NoError(t, d.Add(coordTask))
	orch := seedOrchestratorWithCoordinator(t, d, "fix-widget", coordTask)
	worker := seedOrchestratedWorker(t, d, orch, "1a-build")

	srv.classifyCoordinatorFn = func(ctx context.Context, task *model.Task) (mergesafety.Verdict, error) {
		testutil.Equal(t, task.ID, coordTask.ID)
		return mergesafety.Verdict{Safe: true, Tier: mergesafety.TierMergedPR, Reason: `merged PR https://x confirmed merged into "master"`}, nil
	}

	srv.runCleanupCompute(context.Background())

	got := metaMap(t, d, worker.ID)
	testutil.Equal(t, got[cleanupMetaSafe], "true")
	testutil.Equal(t, got[cleanupMetaTier], mergesafety.TierCoordinatorInferred)
	testutil.Contains(t, got[cleanupMetaReason], coordTask.Name)
	testutil.Contains(t, got[cleanupMetaReason], mergesafety.TierMergedPR)
}

// TestRunCleanupCompute_CoordinatorInferredSafety_NotSafeCoordinatorLeavesWorkerUnchanged
// proves the fail-closed side: a not-safe coordinator verdict never rescues
// its workers — the worker's own Tier A/B verdict is left exactly as-is.
func TestRunCleanupCompute_CoordinatorInferredSafety_NotSafeCoordinatorLeavesWorkerUnchanged(t *testing.T) {
	srv, d := testServer(t)

	coordTask := &model.Task{Name: "coord-2", Status: model.StatusInProgress, Worktree: "/tmp/wt/coord-2"}
	testutil.NoError(t, d.Add(coordTask))
	orch := seedOrchestratorWithCoordinator(t, d, "not-safe-orch", coordTask)
	worker := seedOrchestratedWorker(t, d, orch, "2a-build")

	srv.classifyCoordinatorFn = func(ctx context.Context, task *model.Task) (mergesafety.Verdict, error) {
		return mergesafety.Verdict{Safe: false, Reason: "no matching merged pull request found"}, nil
	}

	srv.runCleanupCompute(context.Background())

	got := metaMap(t, d, worker.ID)
	testutil.Equal(t, got[cleanupMetaSafe], "false")
	testutil.Equal(t, got[cleanupMetaReason], "no repo resolvable for a merged-PR lookup")
}

// TestRunCleanupCompute_CoordinatorInferredSafety_UnresolvableCoordinatorLeavesWorkerUnchanged
// proves the other fail-closed path: an orchestrator with no resolvable
// coordinator role/binding at all (e.g. pruned before this feature existed)
// never invokes coordinator classification and leaves the worker unchanged.
func TestRunCleanupCompute_CoordinatorInferredSafety_UnresolvableCoordinatorLeavesWorkerUnchanged(t *testing.T) {
	srv, d := testServer(t)

	orch, err := d.CreateHeraOrchestrator("coordless-orch", "")
	testutil.NoError(t, err)
	worker := seedOrchestratedWorker(t, d, orch, "3a-build")

	called := false
	srv.classifyCoordinatorFn = func(ctx context.Context, task *model.Task) (mergesafety.Verdict, error) {
		called = true
		return mergesafety.Verdict{Safe: true, Tier: mergesafety.TierMergedPR}, nil
	}

	srv.runCleanupCompute(context.Background())

	testutil.False(t, called)
	got := metaMap(t, d, worker.ID)
	testutil.Equal(t, got[cleanupMetaSafe], "false")
	testutil.Equal(t, got[cleanupMetaReason], "no repo resolvable for a merged-PR lookup")
}

// TestRunCleanupCompute_CoordinatorInferredSafety_ClassifiesCoordinatorExactlyOnce
// proves the per-orchestrator dedup: two not-safe workers under the same
// orchestrator must not each trigger their own coordinator classification.
func TestRunCleanupCompute_CoordinatorInferredSafety_ClassifiesCoordinatorExactlyOnce(t *testing.T) {
	srv, d := testServer(t)

	coordTask := &model.Task{Name: "coord-4", Status: model.StatusInProgress, Worktree: "/tmp/wt/coord-4"}
	testutil.NoError(t, d.Add(coordTask))
	orch := seedOrchestratorWithCoordinator(t, d, "shared-orch", coordTask)
	workerA := seedOrchestratedWorker(t, d, orch, "4a-build")
	workerB := seedOrchestratedWorker(t, d, orch, "4b-build")

	var callCount int
	srv.classifyCoordinatorFn = func(ctx context.Context, task *model.Task) (mergesafety.Verdict, error) {
		callCount++
		return mergesafety.Verdict{Safe: true, Tier: mergesafety.TierLocalAncestor, Reason: "test-seeded"}, nil
	}

	srv.runCleanupCompute(context.Background())

	testutil.Equal(t, callCount, 1)
	for _, w := range []*model.Task{workerA, workerB} {
		got := metaMap(t, d, w.ID)
		testutil.Equal(t, got[cleanupMetaSafe], "true")
		testutil.Equal(t, got[cleanupMetaTier], mergesafety.TierCoordinatorInferred)
	}
}

// TestRunCleanupCompute_CoordinatorInferredSafety_CapsAtOneHop is the test
// that actually proves the one-hop Non-Goal: the immediate coordinator is
// itself a worker under a "grandparent" orchestrator whose own coordinator
// would classify safe if it were ever consulted (mirroring the hera
// sub-coordinator nesting shape — a task simultaneously a worker in one
// orchestrator and the coordinator of another). The not-safe immediate
// coordinator must NOT be rescued by chasing that grandparent, and the
// grandparent's coordinator must never even be classified.
func TestRunCleanupCompute_CoordinatorInferredSafety_CapsAtOneHop(t *testing.T) {
	srv, d := testServer(t)

	grandparentCoordTask := &model.Task{Name: "grandparent-coord", Status: model.StatusInProgress, Worktree: "/tmp/wt/gp-coord"}
	testutil.NoError(t, d.Add(grandparentCoordTask))
	parentOrch := seedOrchestratorWithCoordinator(t, d, "parent-orch", grandparentCoordTask)

	immediateCoordTask := &model.Task{Name: "immediate-coord", Status: model.StatusInProgress, Worktree: "/tmp/wt/imm-coord"}
	testutil.NoError(t, d.Add(immediateCoordTask))
	// immediateCoordTask is a WORKER under parent-orch (the bridging shape)...
	_, immBinding, err := d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: parentOrch.ID, Name: "immediate-worker", Kind: db.HeraKindWorker, ArgusProject: "p",
	}, immediateCoordTask.ID, immediateCoordTask.Worktree)
	testutil.NoError(t, err)
	testutil.NoError(t, d.EndHeraBinding(immBinding.ID, "done"))
	// ...AND separately the COORDINATOR of its own child-orch.
	childOrch := seedOrchestratorWithCoordinator(t, d, "child-orch", immediateCoordTask)

	workerA := seedOrchestratedWorker(t, d, childOrch, "grandchild-worker")

	var calledFor []string
	srv.classifyCoordinatorFn = func(ctx context.Context, task *model.Task) (mergesafety.Verdict, error) {
		calledFor = append(calledFor, task.ID)
		if task.ID == immediateCoordTask.ID {
			return mergesafety.Verdict{Safe: false, Reason: "not safe"}, nil
		}
		// Never legitimately reached — the grandparent must not be classified.
		return mergesafety.Verdict{Safe: true, Tier: mergesafety.TierMergedPR}, nil
	}

	srv.runCleanupCompute(context.Background())

	testutil.Equal(t, len(calledFor), 1)
	testutil.Equal(t, calledFor[0], immediateCoordTask.ID)

	got := metaMap(t, d, workerA.ID)
	testutil.Equal(t, got[cleanupMetaSafe], "false")
	testutil.Equal(t, got[cleanupMetaReason], "no repo resolvable for a merged-PR lookup")
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
