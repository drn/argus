package hera

import (
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/theme"
)

// TestBUG028_PermissionBlockedWorkerRowShowsNeedsInput pins the worker-row path:
// a hera worker blocked on a permission prompt (in_progress + in the needs-input
// set) renders the needs-input "(?)" glyph in the rail. This path was already
// correct (shipped with BUG-023's rollup, #772); the test guards against
// regression. Mirrors the production render: BuildModel stamps RoleView.NeedsInput
// → statusIcon → role.ShowsNeedsInput().
func TestBUG028_PermissionBlockedWorkerRowShowsNeedsInput(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	// seedBoundRole binds the task as StatusInProgress — exactly a worker paused
	// at a permission prompt (PTY idle, task not yet finished).
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")

	m, err := BuildModel(d, map[string]bool{"t-wkr": true}, nil)
	testutil.NoError(t, err)

	wkr := roleByName(t, &m, orch, "wkr")
	testutil.Equal(t, wkr.NeedsInput, true)

	icon, style := statusIcon(wkr, false, 0)
	testutil.Equal(t, icon, theme.IconNeedsInput)
	testutil.Equal(t, style, theme.StyleNeedsInput)
}

// TestBUG028_CoordinatorlessOrchSurfacesSubtreeNeedsInput is the BUG-028
// headline: the rollup is stamped on the OrchView so a COLLAPSED orchestrator
// header surfaces a descendant's needs-input even when no coordinator role
// exists to carry the glyph (the coordinator was nuked, etc.). Without the
// OrchView stamp the header would render no needs-input cue at all — invisible in
// the default "tidy summary" collapsed view, unlike the always-flat task list.
func TestBUG028_CoordinatorlessOrchSurfacesSubtreeNeedsInput(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	// Worker only — no coordinator role (e.g. it was nuked).
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")

	m, err := BuildModel(d, map[string]bool{"t-wkr": true}, nil)
	testutil.NoError(t, err)

	ov := m.OrchByID(orch)
	testutil.Nil(t, ov.CoordRole())               // no coordinator glyph to carry it
	testutil.Equal(t, ov.SubtreeNeedsInput, true) // ...but the header rollup is stamped

	// BUG-023 parity at the header: the App's needs-input set is STICKY (a
	// finished worker idling at its final prompt lingers in it), but the
	// in_progress gate in buildRoleView drops the per-role signal once the worker
	// rolls to in_review — so the header rollup MUST clear too, never stay pinned.
	flagged := map[string]bool{"t-wkr": true}
	testutil.NoError(t, d.SetStatus("t-wkr", model.StatusInReview))
	m2, err := BuildModel(d, flagged, nil)
	testutil.NoError(t, err)
	testutil.Equal(t, m2.OrchByID(orch).SubtreeNeedsInput, false)
}

// TestBUG028_BlockedCoordinatorSurfacesEvenWhenTaskComplete is the headline
// BUG-028 case from the live bug-bash: a COORDINATOR whose bound task has rolled
// to complete/in_review (coordinators commonly finish their task status early)
// but whose session is alive and blocked on a user prompt MUST surface "(?)" on
// its (collapsed) header. The in_progress gate is worker-only — a non-worker role
// does not "finish" by task status while alive, so it surfaces regardless. The
// worker path stays in_progress-gated (BUG-023), proven separately.
func TestBUG028_BlockedCoordinatorSurfacesEvenWhenTaskComplete(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	coord := seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	_ = coord
	// Coordinator task rolled to complete while its session stays alive + blocked.
	testutil.NoError(t, d.SetStatus("t-coord", model.StatusComplete))

	m, err := BuildModel(d, map[string]bool{"t-coord": true}, nil)
	testutil.NoError(t, err)
	cr := m.OrchByID(orch).CoordRole()
	testutil.Equal(t, cr.TaskStatus, "complete")
	testutil.Equal(t, cr.NeedsInput, true)        // surfaces despite complete task
	testutil.Equal(t, cr.ShowsNeedsInput(), true) // header renders "(?)"

	icon, style := statusIcon(cr, false, 0)
	testutil.Equal(t, icon, theme.IconNeedsInput)
	testutil.Equal(t, style, theme.StyleNeedsInput)
}

// TestBUG028_FinishedWorkerStaysClearedEvenWhenTaskComplete guards BUG-023: the
// non-worker exemption above must NOT leak to workers. A worker whose task rolled
// to in_review/complete (finished) but lingers in the sticky needs-input set MUST
// NOT show "(?)" — the worker gate stays strictly in_progress.
func TestBUG028_FinishedWorkerStaysClearedEvenWhenTaskComplete(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")
	testutil.NoError(t, d.SetStatus("t-wkr", model.StatusComplete)) // worker finished

	m, err := BuildModel(d, map[string]bool{"t-wkr": true}, nil) // sticky marker lingers
	testutil.NoError(t, err)
	testutil.Equal(t, roleByName(t, &m, orch, "wkr").NeedsInput, false)
	testutil.Equal(t, m.OrchByID(orch).CoordRole().SubtreeNeedsInput, false)
}
