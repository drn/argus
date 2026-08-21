package hera

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/db"
	heramodel "github.com/drn/argus/internal/hera/model"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// TestBuildModel_SustainedActiveSuppressesContentNeedsInput is the headline for
// narrow-needs-input-sustained-active: a role whose bound task is SustainedActive
// never shows "(?)", even though the content-scan flag (NeedsInput) is set.
func TestBuildModel_SustainedActiveSuppressesContentNeedsInput(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")

	flagged := map[string]bool{"t-wkr": true}

	// Without SustainedActive, the flag surfaces as before (no regression).
	m, err := heramodel.BuildModel(d, flagged, nil, nil, nil)
	testutil.NoError(t, err)
	wkr := roleByName(t, &m, orch, "wkr")
	testutil.Equal(t, wkr.NeedsInput, true)
	testutil.Equal(t, wkr.ShowsNeedsInput(), true)
	testutil.Equal(t, coordSubtreeNI(t, &m, orch), true)

	// With SustainedActive on the same task, "(?)" is suppressed even though the
	// content-scan flag is still set.
	sustained := map[string]bool{"t-wkr": true}
	m2, err := heramodel.BuildModel(d, flagged, nil, nil, sustained)
	testutil.NoError(t, err)
	wkr2 := roleByName(t, &m2, orch, "wkr")
	testutil.Equal(t, wkr2.NeedsInput, true) // raw signal still recorded
	testutil.Equal(t, wkr2.SustainedActive, true)
	testutil.Equal(t, wkr2.ShowsNeedsInput(), false)
	testutil.Equal(t, coordSubtreeNI(t, &m2, orch), false)
}

// TestBuildModel_SustainedActiveSuppressesBlockedRoleStatus proves the OTHER
// OR'd source of needsInputOwn() — a self-reported `blocked` hera role status —
// is suppressed the same way.
func TestBuildModel_SustainedActiveSuppressesBlockedRoleStatus(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	wkr := seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")
	testutil.NoError(t, d.UpsertHeraRoleStatus(wkr.ID, db.HeraStatusBlocked))

	// Without SustainedActive, a self-reported blocked status surfaces "(?)".
	m, err := heramodel.BuildModel(d, nil, nil, nil, nil)
	testutil.NoError(t, err)
	rv := roleByName(t, &m, orch, "wkr")
	testutil.Equal(t, rv.HasStatus, true)
	testutil.Equal(t, rv.Status, db.HeraStatusBlocked)
	testutil.Equal(t, rv.ShowsNeedsInput(), true)

	// SustainedActive on the same task suppresses it.
	sustained := map[string]bool{"t-wkr": true}
	m2, err := heramodel.BuildModel(d, nil, nil, nil, sustained)
	testutil.NoError(t, err)
	rv2 := roleByName(t, &m2, orch, "wkr")
	testutil.Equal(t, rv2.Status, db.HeraStatusBlocked) // raw ladder value unchanged
	testutil.Equal(t, rv2.SustainedActive, true)
	testutil.Equal(t, rv2.ShowsNeedsInput(), false)
}

// TestBuildModel_SustainedActiveSuppressesAcrossDualBoundHats is the ground-truth
// repro: a task holds TWO live hera bindings (a parent-orchestrator worker-kind
// role and a child-orchestrator coordinator-kind role, per
// agent.MaterializeHeraSubCoordinator's dual-bound sub-coordinator shape). One
// hat carries a stale self-reported `blocked` status left over from an earlier
// phase; the OTHER hat is the one whose session is demonstrably, currently
// active. Because SustainedActive is computed per TASK (shared taskID), not per
// role, BOTH roles read the identical value — the stale blocked hat is
// suppressed WITHOUT any code needing to look up the other binding.
func TestBuildModel_SustainedActiveSuppressesAcrossDualBoundHats(t *testing.T) {
	d := memDB(t)
	parentOrch := seedOrch(t, d, "ai-swot")
	childOrch := seedOrch(t, d, "contrib-classifier")

	const sharedTask = "t-shared"
	testutil.NoError(t, d.Add(&model.Task{ID: sharedTask, Name: sharedTask, Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}))

	workerHat, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: parentOrch, Name: "contribution-classifier", Kind: db.HeraKindWorker, ArgusProject: "p"})
	testutil.NoError(t, err)
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: workerHat.ID, OrchestratorID: parentOrch, ArgusTaskID: sharedTask, WorktreePath: "/wt/" + sharedTask})
	testutil.NoError(t, err)
	testutil.NoError(t, d.UpsertHeraRoleStatus(workerHat.ID, db.HeraStatusBlocked)) // stale, left over

	coordHat, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: childOrch, Name: "coord", Kind: db.HeraKindCoordinator, ArgusProject: "p"})
	testutil.NoError(t, err)
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: coordHat.ID, OrchestratorID: childOrch, ArgusTaskID: sharedTask, WorktreePath: "/wt/" + sharedTask})
	testutil.NoError(t, err)
	testutil.NoError(t, d.UpsertHeraRoleStatus(coordHat.ID, db.HeraStatusWorking)) // genuinely active hat

	// Task rolled to in_review (mirrors the ground-truth daemon-bounce finding);
	// irrelevant to needsInputOwn either way, included for realism.
	testutil.NoError(t, d.SetStatus(sharedTask, model.StatusInReview))

	// Without SustainedActive: the stale blocked worker hat surfaces "(?)".
	m, err := heramodel.BuildModel(d, nil, nil, nil, nil)
	testutil.NoError(t, err)
	workerRV := roleByName(t, &m, parentOrch, "contribution-classifier")
	testutil.Equal(t, workerRV.ShowsNeedsInput(), true)

	// With the shared task SustainedActive: BOTH hats suppress "(?)" — the
	// worker hat's stale blocked status included — with no per-hat logic.
	sustained := map[string]bool{sharedTask: true}
	m2, err := heramodel.BuildModel(d, nil, nil, nil, sustained)
	testutil.NoError(t, err)
	workerRV2 := roleByName(t, &m2, parentOrch, "contribution-classifier")
	coordRV2 := roleByName(t, &m2, childOrch, "coord")
	testutil.Equal(t, workerRV2.SustainedActive, true)
	testutil.Equal(t, coordRV2.SustainedActive, true)
	testutil.Equal(t, workerRV2.ShowsNeedsInput(), false)
	testutil.Equal(t, coordRV2.ShowsNeedsInput(), false)
}

// TestBuildModel_SustainedActiveDoesNotMaskUnrelatedIdleBlocked confirms no
// regression: a role that is genuinely idle/blocked with no demonstrated
// sustained activity on ITS OWN task still shows "(?)" exactly as before — the
// suppression is per-task, not global.
func TestBuildModel_SustainedActiveDoesNotMaskUnrelatedIdleBlocked(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	blockedWkr := seedBoundRole(t, d, orch, "blocked-wkr", db.HeraKindWorker, "t-blocked")
	testutil.NoError(t, d.UpsertHeraRoleStatus(blockedWkr.ID, db.HeraStatusBlocked))
	seedBoundRole(t, d, orch, "other-wkr", db.HeraKindWorker, "t-other")

	// A DIFFERENT task ("t-other") is sustained-active; "t-blocked" is not.
	sustained := map[string]bool{"t-other": true}
	m, err := heramodel.BuildModel(d, nil, nil, nil, sustained)
	testutil.NoError(t, err)
	testutil.Equal(t, roleByName(t, &m, orch, "blocked-wkr").ShowsNeedsInput(), true)
	testutil.Equal(t, roleByName(t, &m, orch, "other-wkr").ShowsNeedsInput(), false)
}
