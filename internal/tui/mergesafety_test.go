package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/mergesafety"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/hera"
	"github.com/gdamore/tcell/v2"
)

// --- Stage 5 (fix-hera-nuke-cleanup): cascade-nuke Tier D discovery --------

// seedStackedBranchStack seeds a 3-task base_branch stack (task1 <- task2 <-
// task3), each on project "p" (Path = t.TempDir(), Branch = "master" so
// gitutil.ResolveDefaultBranch short-circuits without a real git repo — see
// its own doc comment: a non-empty `configured` branch skips every git
// shellout). task1 is the earliest link (no BaseBranch of its own — branched
// off "master" conceptually), task2 branched off task1's branch, task3
// (the stack's tip) branched off task2's branch.
func seedStackedBranchStack(t *testing.T, d *db.DB) {
	t.Helper()
	testutil.NoError(t, d.SetProject("p", config.Project{Path: t.TempDir(), Branch: "master"}))
	now := time.Now()
	tasks := []*model.Task{
		{ID: "task1", Name: "task1", Project: "p", Branch: "b1", Status: model.StatusInProgress, CreatedAt: now},
		{ID: "task2", Name: "task2", Project: "p", Branch: "b2", BaseBranch: "b1", Status: model.StatusInProgress, CreatedAt: now},
		{ID: "task3", Name: "task3", Project: "p", Branch: "b3", BaseBranch: "b2", Status: model.StatusInProgress, CreatedAt: now},
	}
	for _, task := range tasks {
		testutil.NoError(t, d.Add(task))
	}
}

// TestClassifyStackedBranchesForCascade_RescuesEarlierChainLink covers the
// core Tier D discovery scenario (fix-hera-nuke-cleanup Stage 5): task1 is
// the not-safe candidate the CURRENT cascade already reclaims unconditionally
// (inCascade=true — its own branch is deleted regardless of this pass, and
// its task_meta is wiped the instant it's archived, so it can never be a
// meaningfully-excludable "extra" candidate). task2, one link further up the
// SAME base_branch stack, is NOT part of this cascade and is genuinely
// rescued: its stack's tip (task3, stubbed via classifyStackInferredFn to
// avoid a real git repo/network call) confirms safe, so task2 surfaces as an
// extra candidate. The tip itself (task3) is never included.
func TestClassifyStackedBranchesForCascade_RescuesEarlierChainLink(t *testing.T) {
	d := testDB(t)
	seedStackedBranchStack(t, d)
	app := New(d, agent.NewRunner(nil), false)
	app.classifyStackInferredFn = func(ctx context.Context, p mergesafety.StackParams, tipVerdicts map[string]mergesafety.Verdict) (mergesafety.Verdict, error) {
		return mergesafety.Verdict{Safe: true, Tier: mergesafety.TierStackInferred, Reason: "stubbed"}, nil
	}

	extra := app.classifyStackedBranchesForCascade(context.Background(), []string{"task1"}, map[string]bool{"task1": true}, make(map[string]mergesafety.Verdict))

	got := map[string]string{}
	for _, c := range extra {
		got[c.TaskID] = c.Branch
	}
	testutil.Equal(t, len(extra), 1)
	testutil.Equal(t, got["task2"], "b2")
	if _, x := got["task1"]; x {
		t.Fatal("a task already reclaimed unconditionally by this cascade must never be offered as an extra, excludable candidate")
	}
	if _, tipIncluded := got["task3"]; tipIncluded {
		t.Fatal("the stack's own tip must never be reported as an extra branch")
	}
}

// TestClassifyStackedBranchesForCascade_ExcludedBranchNeverReoffered covers
// the spec scenario "An excluded stacked branch is never re-offered": once
// the operator's decision is persisted via db.ExcludeCleanupBranch, a later
// call over the SAME candidate set must never surface that branch again.
// Uses a 4-task stack (root <- task1 <- task2 <- task3) so BOTH task1 and
// task2 are genuinely rescuable extra candidates (neither is in this
// cascade) — excluding task1 must not affect task2.
func TestClassifyStackedBranchesForCascade_ExcludedBranchNeverReoffered(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.SetProject("p", config.Project{Path: t.TempDir(), Branch: "master"}))
	now := time.Now()
	tasks := []*model.Task{
		{ID: "root", Name: "root", Project: "p", Branch: "b0", Status: model.StatusInProgress, CreatedAt: now},
		{ID: "task1", Name: "task1", Project: "p", Branch: "b1", BaseBranch: "b0", Status: model.StatusInProgress, CreatedAt: now},
		{ID: "task2", Name: "task2", Project: "p", Branch: "b2", BaseBranch: "b1", Status: model.StatusInProgress, CreatedAt: now},
		{ID: "task3", Name: "task3", Project: "p", Branch: "b3", BaseBranch: "b2", Status: model.StatusInProgress, CreatedAt: now},
	}
	for _, task := range tasks {
		testutil.NoError(t, d.Add(task))
	}
	app := New(d, agent.NewRunner(nil), false)
	app.classifyStackInferredFn = func(ctx context.Context, p mergesafety.StackParams, tipVerdicts map[string]mergesafety.Verdict) (mergesafety.Verdict, error) {
		return mergesafety.Verdict{Safe: true, Tier: mergesafety.TierStackInferred, Reason: "stubbed"}, nil
	}

	testutil.NoError(t, d.ExcludeCleanupBranch("task1", "b1"))

	extra := app.classifyStackedBranchesForCascade(context.Background(), []string{"root"}, map[string]bool{"root": true}, make(map[string]mergesafety.Verdict))

	got := map[string]bool{}
	for _, c := range extra {
		got[c.TaskID] = true
	}
	testutil.Equal(t, got["task1"], false) // excluded — never re-offered
	testutil.Equal(t, got["task2"], true)  // unaffected
}

// TestClassifyStackedBranchesForCascade_NoDescendantIsNoop covers the common
// case (every existing cascade-nuke test hits this): a not-safe candidate
// with no base_branch descendant at all does zero extra work and returns no
// candidates — classifyStackInferredFn must never even be called.
func TestClassifyStackedBranchesForCascade_NoDescendantIsNoop(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.SetProject("p", config.Project{Path: t.TempDir(), Branch: "master"}))
	testutil.NoError(t, d.Add(&model.Task{ID: "solo", Name: "solo", Project: "p", Branch: "b-solo", Status: model.StatusInProgress, CreatedAt: time.Now()}))
	app := New(d, agent.NewRunner(nil), false)
	app.classifyStackInferredFn = func(ctx context.Context, p mergesafety.StackParams, tipVerdicts map[string]mergesafety.Verdict) (mergesafety.Verdict, error) {
		t.Fatal("should not be called when the candidate has no base_branch descendant")
		return mergesafety.Verdict{}, nil
	}

	extra := app.classifyStackedBranchesForCascade(context.Background(), []string{"solo"}, nil, make(map[string]mergesafety.Verdict))
	testutil.Equal(t, len(extra), 0)
}

// TestClassifyTasksConcurrently_CountsSafe covers the bounded-concurrency
// classification helper cascade nuke / clear-archive use for their "X of Y
// reclaimed tasks confirmed merged" count.
func TestClassifyTasksConcurrently_CountsSafe(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.classifyNukeCandidateFn = func(taskID string) mergesafety.Verdict {
		return mergesafety.Verdict{Safe: taskID == "safe-1" || taskID == "safe-2"}
	}

	got := app.classifyTasksConcurrently([]string{"safe-1", "not-safe-1", "safe-2", "not-safe-2", "not-safe-3"})
	testutil.Equal(t, got, 2)
}

// TestClassifyTasksConcurrently_EmptyIsZero covers the fast path callers take
// (heraCascadeNukeFrom/heraClearArchive) when there's nothing to classify.
func TestClassifyTasksConcurrently_EmptyIsZero(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.classifyNukeCandidateFn = func(string) mergesafety.Verdict {
		t.Fatal("should not be called for an empty ID list")
		return mergesafety.Verdict{}
	}
	testutil.Equal(t, app.classifyTasksConcurrently(nil), 0)
}

// TestHeraOpenSingleNukeReview_OpensPopupWithVerdict drives the happy path:
// classify completes with the selection unchanged, so the popup opens with
// the classified candidate.
func TestHeraOpenSingleNukeReview_OpensPopupWithVerdict(t *testing.T) {
	d := testDB(t)
	orch := seedHeraOrch(t, d, "o")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	seedHeraBoundRole(t, d, orch, "w1", db.HeraKindWorker, "tw1")
	app := New(d, agent.NewRunner(nil), false)
	app.classifyNukeCandidateFn = func(taskID string) mergesafety.Verdict {
		return mergesafety.Verdict{Safe: false, Reason: "no matching merged pull request found"}
	}

	sim, stop := wireApp(t, app)
	defer stop()
	heraTabCursorOnWorker(t, app, sim) // cursor lands on w1 (the sole worker)

	sim.InjectKey(tcell.KeyCtrlD, 0, 0)
	waitForMode(t, app, modeMergeSafetyPopup)

	readUI(t, app.tapp, func() {
		cands := app.mergeSafetyPopup.Candidates()
		testutil.Equal(t, len(cands), 1)
		testutil.Equal(t, cands[0].TaskID, "tw1")
		testutil.Equal(t, cands[0].Safe, false)
		testutil.Contains(t, cands[0].Reason, "no matching merged pull request")
	})
}

// TestHeraOpenSingleNukeReview_StalenessGuardDropsPopup covers the mission's
// explicit requirement: if the rail selection changes between dispatching the
// classify goroutine and it completing, the popup must be silently dropped
// rather than opening over a stale target (mirrors fetchGitStatus's own
// staleness check). classifyNukeCandidateFn blocks on a channel so the test
// can deterministically move the selection mid-flight.
func TestHeraOpenSingleNukeReview_StalenessGuardDropsPopup(t *testing.T) {
	d := testDB(t)
	orch := seedHeraOrch(t, d, "o")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	seedHeraBoundRole(t, d, orch, "w1", db.HeraKindWorker, "tw1")
	seedHeraBoundRole(t, d, orch, "w2", db.HeraKindWorker, "tw2")
	app := New(d, agent.NewRunner(nil), false)

	release := make(chan struct{})
	classifyStarted := make(chan struct{}, 1)
	app.classifyNukeCandidateFn = func(taskID string) mergesafety.Verdict {
		classifyStarted <- struct{}{}
		<-release
		return mergesafety.Verdict{Safe: true}
	}

	sim, stop := wireApp(t, app)
	defer stop()
	heraTabCursorOnWorker(t, app, sim) // cursor lands on w1

	sim.InjectKey(tcell.KeyCtrlD, 0, 0) // dispatches the (blocked) classify goroutine

	select {
	case <-classifyStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for classify to start")
	}

	// Move the cursor to w2 WHILE classify is still blocked on w1.
	sim.InjectKey(tcell.KeyRune, 'j', 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		sel := app.heraPage.SelectionContext()
		if sel.Role == nil || sel.Role.Name != "w2" {
			t.Fatalf("expected selection to have moved to w2, got %+v", sel)
		}
	})

	close(release) // let the stale classify complete

	// The popup must NOT open for the stale w1 classification — mode stays
	// modeTaskList (bounded wait to rule out it merely opening slowly).
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		var mode viewMode
		readUI(t, app.tapp, func() { mode = app.mode })
		if mode == modeMergeSafetyPopup {
			t.Fatal("merge-safety popup opened over a stale selection")
		}
		time.Sleep(5 * time.Millisecond)
	}
	readUI(t, app.tapp, func() { testutil.Equal(t, app.mode, modeTaskList) })
}

// --- global Cleanup (Part B REST wiring) ------------------------------------

// cleanupTestServer builds an httptest.Server implementing the three
// maintenance endpoints against an in-memory candidate list, so tests never
// touch a real daemon. computing reports true for the first `computingTicks`
// GET calls, then false. Every field is guarded by mu — httptest serves each
// request on its own goroutine, distinct from both the poll goroutine and the
// test/tview goroutines that read these fields back.
type cleanupTestServer struct {
	*httptest.Server
	mu            sync.Mutex
	candidates    []cleanupCandidateJSON
	computingLeft int
	computeCalls  int
	cleanCalls    []string // scopes passed to /clean
}

func newCleanupTestServer(t *testing.T, candidates []cleanupCandidateJSON, computingTicks int) *cleanupTestServer {
	t.Helper()
	s := &cleanupTestServer{candidates: candidates, computingLeft: computingTicks}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/maintenance/cleanup-candidates/compute", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.computeCalls++
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /api/maintenance/cleanup-candidates", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		computing := s.computingLeft > 0
		if s.computingLeft > 0 {
			s.computingLeft--
		}
		cands := s.candidates
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cleanupCandidatesResp{Candidates: cands, Computing: computing})
	})
	mux.HandleFunc("POST /api/maintenance/cleanup-candidates/clean", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Scope string `json:"scope"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.mu.Lock()
		s.cleanCalls = append(s.cleanCalls, body.Scope)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cleanupCleanResp{Cleaned: 1, Skipped: 0})
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func (s *cleanupTestServer) client() *localMaintenanceClient {
	return &localMaintenanceClient{baseURL: s.URL, token: "tok", hc: s.Client()}
}

func (s *cleanupTestServer) computeCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.computeCalls
}

func (s *cleanupTestServer) cleanScopes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.cleanCalls...)
}

// TestHeraOpenGlobalCleanup_ScansThenPopulates covers the "First open
// triggers classification with a visible wait state" scenario end to end
// against a fake HTTP server (never a real daemon).
func TestHeraOpenGlobalCleanup_ScansThenPopulates(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	srv := newCleanupTestServer(t, []cleanupCandidateJSON{
		{ID: "t1", Name: "alpha", Project: "p1", Safe: true},
		{ID: "t2", Name: "beta", Project: "p2", Safe: false, Reason: "ambiguous"},
	}, 2) // computing=true for 2 polls, then false
	app.maintenanceClientFactory = func() (*localMaintenanceClient, error) { return srv.client(), nil }
	cleanupPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { cleanupPollInterval = 700 * time.Millisecond })

	sim, stop := wireApp(t, app)
	defer stop()
	sim.InjectKey(tcell.KeyRune, '2', 0)
	syncUI(t, app.tapp)

	readUI(t, app.tapp, func() { app.heraOpenGlobalCleanup(hera.Selection{}) })
	waitForMode(t, app, modeMergeSafetyPopup)

	// Wait for the ENTIRE poll loop to finish (computing flips false and the
	// background goroutine returns) rather than just the first candidate
	// batch to land — the goroutine reads the package-level cleanupPollInterval
	// on every iteration, and this test's own cleanup mutates it back
	// afterward, so letting a stale iteration keep running past this point
	// would race with that reset.
	deadline := time.Now().Add(2 * time.Second)
	for {
		var scanning bool
		readUI(t, app.tapp, func() {
			if app.mergeSafetyPopup != nil {
				scanning = app.mergeSafetyPopup.Scanning()
			}
		})
		if !scanning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the cleanup classification pass to finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
	readUI(t, app.tapp, func() {
		cands := app.mergeSafetyPopup.Candidates()
		names := map[string]bool{}
		for _, c := range cands {
			names[c.TaskID] = true
		}
		testutil.Equal(t, names["t1"], true)
		testutil.Equal(t, names["t2"], true)
	})
	testutil.Equal(t, srv.computeCallCount(), 1)
}

// TestHeraOpenGlobalCleanup_ThreadsCoordinatorInferredTierIntoPopup covers
// add-coordinator-inferred-safety's wire-to-widget threading: a SAFE
// candidate carrying the coordinator-inferred tier over the wire round-trips
// through cleanupCandidatesToRows into the popup's own candidate shape.
func TestHeraOpenGlobalCleanup_ThreadsCoordinatorInferredTierIntoPopup(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	srv := newCleanupTestServer(t, []cleanupCandidateJSON{
		{ID: "t1", Name: "rescued", Project: "p1", Safe: true, Tier: mergesafety.TierCoordinatorInferred, Reason: "coordinator task coord-1a confirmed via merged-pr: ..."},
	}, 0) // computing=false immediately — a single settled batch is enough here
	app.maintenanceClientFactory = func() (*localMaintenanceClient, error) { return srv.client(), nil }
	cleanupPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { cleanupPollInterval = 700 * time.Millisecond })

	sim, stop := wireApp(t, app)
	defer stop()
	sim.InjectKey(tcell.KeyRune, '2', 0)
	syncUI(t, app.tapp)

	readUI(t, app.tapp, func() { app.heraOpenGlobalCleanup(hera.Selection{}) })
	waitForMode(t, app, modeMergeSafetyPopup)

	deadline := time.Now().Add(2 * time.Second)
	for {
		var scanning bool
		readUI(t, app.tapp, func() {
			if app.mergeSafetyPopup != nil {
				scanning = app.mergeSafetyPopup.Scanning()
			}
		})
		if !scanning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the cleanup classification pass to finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
	readUI(t, app.tapp, func() {
		cands := app.mergeSafetyPopup.Candidates()
		testutil.Equal(t, len(cands), 1)
		testutil.Equal(t, cands[0].Tier, mergesafety.TierCoordinatorInferred)
	})
}

// TestHeraDoGlobalClean_PostsChosenScope covers the "Clean safe/Clean all"
// scope reaching the master-gated clean endpoint, and the status-bar result.
func TestHeraDoGlobalClean_PostsChosenScope(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	srv := newCleanupTestServer(t, nil, 0)
	app.maintenanceClientFactory = func() (*localMaintenanceClient, error) { return srv.client(), nil }

	sim, stop := wireApp(t, app)
	defer stop()
	sim.InjectKey(tcell.KeyRune, '2', 0)
	syncUI(t, app.tapp)

	readUI(t, app.tapp, func() { app.heraDoGlobalClean(mergeSafetyScopeSafe, nil) })

	deadline := time.Now().Add(2 * time.Second)
	for {
		calls := srv.cleanScopes()
		if len(calls) == 1 {
			testutil.Equal(t, calls[0], "safe")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for the clean request to land, got %v", calls)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestNewLocalMaintenanceClient_MissingTokenErrors covers the "API not
// enabled" degradation path — no api-token file on disk yet.
func TestNewLocalMaintenanceClient_MissingTokenErrors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	_, err := app.newLocalMaintenanceClient()
	if err == nil {
		t.Fatal("expected an error when the API token file doesn't exist")
	}
	testutil.Contains(t, err.Error(), "enable the API")
}

// TestNewLocalMaintenanceClient_RemoteModeErrors covers the defensive guard:
// a --remote TUI (a.db is not *db.DB) must never attempt a local
// ~/.argus/api-token read or a 127.0.0.1 dial — that would target the wrong
// machine entirely.
func TestNewLocalMaintenanceClient_RemoteModeErrors(t *testing.T) {
	app := New(stubStore{}, agent.NewRunner(nil), false)
	_, err := app.newLocalMaintenanceClient()
	if err == nil {
		t.Fatal("expected an error in remote mode")
	}
	testutil.Contains(t, err.Error(), "local mode")
}
