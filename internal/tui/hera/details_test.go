package hera

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/gdamore/tcell/v2"
)

// drawDetails renders a DetailsView to a fresh sim screen and returns the row
// of text starting at the given (x,y), trimmed — a cheap way to assert content.
func drawnText(t *testing.T, draw func(tcell.Screen), x, y, w int) string {
	t.Helper()
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	t.Cleanup(sim.Fini)
	sim.SetSize(80, 30)
	draw(sim)
	sim.Show() // flush SetContent writes into the buffer GetContents reads
	cells, _, _ := sim.GetContents()
	runes := make([]rune, 0, w)
	for i := 0; i < w; i++ {
		c := cells[(y*80)+x+i]
		if len(c.Runes) > 0 {
			runes = append(runes, c.Runes[0])
		}
	}
	return string(runes)
}

func TestDetails_RostersWorkers(t *testing.T) {
	orch := &OrchView{
		ID:   1,
		Name: "my-orch",
		Roles: []RoleView{
			{RoleID: 1, OrchID: 1, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "t-c"},
			{RoleID: 2, OrchID: 1, Name: "alpha", Kind: db.HeraKindWorker, Live: true, TaskID: "t-a", ReadyToClose: true},
			{RoleID: 3, OrchID: 1, Name: "beta", Kind: db.HeraKindWorker, Live: true, TaskID: "t-b"},
		},
	}
	prMeta := map[string]map[string]string{"t-b": {"url": "https://x/pr/1", "state": "approved"}}
	d := NewDetailsView()
	d.SetOrch(orch, prMeta)

	// Title row is the orchestrator name.
	testutil.Contains(t, drawnText(t, func(s tcell.Screen) { d.Draw(s, 0, 0, 40, 20, false) }, 1, 1, 20), "my-orch")

	// Roster header shows the worker count (2, excluding the coordinator).
	found := false
	for y := 0; y < 20; y++ {
		if got := drawnText(t, func(s tcell.Screen) { d.Draw(s, 0, 0, 40, 20, false) }, 1, y, 20); got != "" {
			if testContains(got, "Agents (2)") {
				found = true
			}
		}
	}
	testutil.Equal(t, found, true)
}

func TestDetails_NilOrchAndMarks(t *testing.T) {
	d := NewDetailsView()
	// Nil orch → placeholder, no panic.
	testutil.Contains(t, drawnText(t, func(s tcell.Screen) { d.Draw(s, 0, 0, 40, 10, false) }, 1, 1, 26), "no coordinator")

	// hasPR (state-gated, not url-presence) + rosterStatusText compose "ready PR":
	// an actionable review state shows PR; a merged/closed state retains the url
	// in the cache but must not.
	d.prMeta = map[string]map[string]string{"t": {"url": "u", "state": "awaiting-review"}}
	rc := &RoleView{TaskID: "t", ReadyToClose: true}
	testutil.Equal(t, d.hasPR(rc), true)
	testutil.Equal(t, rosterStatusText(rc, d.hasPR(rc)), "ready PR")

	d.prMeta = map[string]map[string]string{"t": {"url": "u", "state": "merged-closed"}}
	testutil.Equal(t, d.hasPR(rc), false)
	testutil.Equal(t, rosterStatusText(rc, d.hasPR(rc)), "ready")

	noMark := &RoleView{TaskID: "none"}
	testutil.Equal(t, d.hasPR(noMark), false)
	testutil.Equal(t, rosterStatusText(noMark, d.hasPR(noMark)), "—")
}

// TestDetails_HasPR_PRStateTable locks the PR-state gating (ported from the
// pre-merge roleMark table): only actionable review states show the PR mark;
// merged/closed, draft, unknown, and empty/unparseable states never do, even
// with a url still cached.
func TestDetails_HasPR_PRStateTable(t *testing.T) {
	d := NewDetailsView()
	cases := []struct {
		name  string
		state string
		want  bool
	}{
		{"awaiting-review", "awaiting-review", true},
		{"changes-requested", "changes-requested", true},
		{"approved", "approved", true},
		{"merged-closed", "merged-closed", false},
		{"draft", "draft", false},
		{"unknown", "unknown", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d.prMeta = map[string]map[string]string{"t": {"url": "u", "state": tc.state}}
			testutil.Equal(t, d.hasPR(&RoleView{TaskID: "t"}), tc.want)
		})
	}
}

func TestDetails_TinyRectNoPanic(t *testing.T) {
	d := NewDetailsView()
	d.SetOrch(&OrchView{ID: 1, Name: "o"}, nil)
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(80, 30)
	d.Draw(sim, 0, 0, 1, 1, true) // below the 2x2 floor → early return
	d.Draw(sim, 0, 0, 6, 4, true) // focused border path
}

// rosterContainsWidth is rosterContains generalized to a caller-chosen pane
// width — the roster table needs more horizontal room than the default 40
// columns once the ARCHETYPE/MODEL columns are in play.
func rosterContainsWidth(t *testing.T, d *DetailsView, w, h int, sub string) bool {
	t.Helper()
	for y := range h {
		if testContains(drawnText(t, func(s tcell.Screen) { d.Draw(s, 0, 0, w, h, false) }, 1, y, w-4), sub) {
			return true
		}
	}
	return false
}

// rosterContains reports whether any row of a DetailsView drawn at the given
// height (width 40) contains sub (scans the full inner width).
func rosterContains(t *testing.T, d *DetailsView, h int, sub string) bool {
	t.Helper()
	return rosterContainsWidth(t, d, 40, h, sub)
}

func TestDetails_ContentHeight(t *testing.T) {
	coord := RoleView{RoleID: 1, OrchID: 1, Name: "coord", Kind: db.HeraKindCoordinator}
	wkr := func(id int64, name string) RoleView {
		return RoleView{RoleID: id, OrchID: 1, Name: name, Kind: db.HeraKindWorker}
	}
	tests := []struct {
		name string
		orch *OrchView
		want int
	}{
		// Row budget: border(2) + always(11) + coord(0/1) + agent(0) + worktree(0)
		// + reposRows(1 "(none)") + workerRows(1 "(none)" when empty, else 1 header
		// + n data rows). The test roles carry no ArgusProject and the coord is
		// unbound, so agent/worktree are omitted and repos is the "(none)" line.
		{"nil orch", nil, 3}, // border + placeholder line
		{"coord, no workers", &OrchView{ID: 1, Roles: []RoleView{coord}}, 16},                           // 2 + 11 + 1 + 1(repos none) + 1(workers none)
		{"coord + 2 workers", &OrchView{ID: 1, Roles: []RoleView{coord, wkr(2, "a"), wkr(3, "b")}}, 18}, // 2 + 11 + 1 + 1 + (1 header + 2)
		{"no coord role, 2 workers", &OrchView{ID: 1, Roles: []RoleView{wkr(2, "a"), wkr(3, "b")}}, 17}, // 2 + 11 + 0 + 1 + (1 header + 2)
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDetailsView()
			d.SetOrch(tc.orch, nil)
			testutil.Equal(t, d.ContentHeight(), tc.want)
		})
	}
}

// TestDetails_ContentHeightMatchesDraw pins the contract that ContentHeight is
// the EXACT minimum height at which Draw renders the full pane. The Summary
// placeholder is the LAST line Draw emits, so at h == ContentHeight it is
// visible; at h-1 it is truncated. This guards the formula against drifting from
// Draw's actual row budget. Both the coordinator-present and coordinator-absent
// branches are exercised, since they have different row budgets.
func TestDetails_ContentHeightMatchesDraw(t *testing.T) {
	tests := []struct {
		name  string
		roles []RoleView
	}{
		{"with coord role", []RoleView{
			{RoleID: 1, OrchID: 1, Name: "coord", Kind: db.HeraKindCoordinator},
			{RoleID: 2, OrchID: 1, Name: "alpha", Kind: db.HeraKindWorker},
			{RoleID: 3, OrchID: 1, Name: "zlast", Kind: db.HeraKindWorker},
		}},
		{"no coord role", []RoleView{
			{RoleID: 2, OrchID: 1, Name: "alpha", Kind: db.HeraKindWorker},
			{RoleID: 3, OrchID: 1, Name: "zlast", Kind: db.HeraKindWorker},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDetailsView()
			d.SetOrch(&OrchView{ID: 1, Name: "o", Roles: tc.roles}, nil)
			ch := d.ContentHeight()
			// The Summary placeholder is the final rendered line; the last worker
			// row sits above it and must always be visible at full height.
			testutil.Equal(t, rosterContains(t, d, ch, "zlast"), true)
			testutil.Equal(t, rosterContains(t, d, ch, "auto-generated"), true)    // fits exactly
			testutil.Equal(t, rosterContains(t, d, ch-1, "auto-generated"), false) // one short → truncated
		})
	}
}

func TestDeriveCoordMeta_LastActivityMax(t *testing.T) {
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	orch := &OrchView{
		ID:        1,
		CreatedAt: base,
		Roles: []RoleView{
			// Coordinator: created later than the orch, with a still-later status
			// update — the status update is the global max.
			{
				RoleID: 1, Kind: db.HeraKindCoordinator,
				CreatedAt:        base.Add(1 * time.Hour),
				BindingStartedAt: base.Add(2 * time.Hour),
				StatusUpdatedAt:  base.Add(9 * time.Hour), // <- max
			},
			// Worker: every timestamp earlier than the coordinator's status update.
			{
				RoleID: 2, Kind: db.HeraKindWorker,
				CreatedAt:        base.Add(3 * time.Hour),
				BindingStartedAt: base.Add(4 * time.Hour),
				StatusUpdatedAt:  base.Add(5 * time.Hour),
			},
		},
	}
	m := deriveCoordMeta(orch)
	testutil.Equal(t, m.Created.Equal(base), true)
	testutil.Equal(t, m.LastActivity.Equal(base.Add(9*time.Hour)), true)
}

func TestDeriveCoordMeta_LastActivityFallsBackToCreated(t *testing.T) {
	base := time.Date(2026, 6, 2, 8, 30, 0, 0, time.UTC)
	// All role timestamps zero (unbound, no status) → last activity == orch created.
	orch := &OrchView{
		ID:        1,
		CreatedAt: base,
		Roles:     []RoleView{{RoleID: 1, Kind: db.HeraKindCoordinator}},
	}
	m := deriveCoordMeta(orch)
	testutil.Equal(t, m.LastActivity.Equal(base), true)
}

func TestDeriveCoordMeta_ReposDistinctSorted(t *testing.T) {
	orch := &OrchView{
		ID: 1,
		Roles: []RoleView{
			{RoleID: 1, Kind: db.HeraKindCoordinator, ArgusProject: "b"},
			{RoleID: 2, Kind: db.HeraKindWorker, ArgusProject: "a"},
			{RoleID: 3, Kind: db.HeraKindWorker, ArgusProject: "a"}, // dup
			{RoleID: 4, Kind: db.HeraKindWorker, ArgusProject: ""},  // blank skipped
		},
	}
	m := deriveCoordMeta(orch)
	testutil.DeepEqual(t, m.Repos, []string{"a", "b"})
}

func TestDeriveCoordMeta_AgentAndWorktreeFromCoord(t *testing.T) {
	orch := &OrchView{
		ID: 1,
		Roles: []RoleView{
			{
				RoleID: 1, Kind: db.HeraKindCoordinator,
				TaskName:     "the-hera-detail",
				WorktreePath: "/home/u/.argus/worktrees/Hera/the-hera-detail",
			},
			{RoleID: 2, Kind: db.HeraKindWorker, TaskName: "wk", WorktreePath: "/x/wk"},
		},
	}
	m := deriveCoordMeta(orch)
	testutil.Equal(t, m.AgentName, "the-hera-detail")
	testutil.Equal(t, m.Worktree, "/home/u/.argus/worktrees/Hera/the-hera-detail")
}

// TestDetails_RendersMetadataBlock asserts the restored metadata fields render
// for a bound coordinator (Created, Last activity, Agent, Worktree, Repos in
// scope, and the Summary placeholder), alongside the existing roster.
func TestDetails_RendersMetadataBlock(t *testing.T) {
	base := time.Date(2026, 6, 10, 9, 0, 0, 0, time.UTC)
	orch := &OrchView{
		ID:        1,
		Name:      "my-orch",
		CreatedAt: base,
		Roles: []RoleView{
			{
				RoleID: 1, Name: "coord", Kind: db.HeraKindCoordinator, Live: true,
				TaskID: "t-c", TaskName: "coord-task", ArgusProject: "repo-z",
				WorktreePath: "/wt/coord-task", CreatedAt: base,
				StatusUpdatedAt: base.Add(2 * time.Hour),
			},
			{RoleID: 2, Name: "alpha", Kind: db.HeraKindWorker, Live: true, TaskID: "t-a", ArgusProject: "repo-a"},
		},
	}
	d := NewDetailsView()
	d.SetOrch(orch, nil)

	want := []string{
		"my-orch",         // title
		"Created:",        // metadata
		"Last activity:",  //
		"Agent:",          //
		"coord-task",      // agent value
		"Worktree:",       //
		"Repos in scope:", //
		"repo-a",          // sorted before repo-z
		"repo-z",          //
		"Agents (1)",      // roster header (coord excluded)
		"alpha",           // worker
		"Summary:",        // reserved placeholder
		"auto-generated",  //
	}
	for _, sub := range want {
		if !rosterContains(t, d, 40, sub) {
			t.Errorf("expected Details render to contain %q", sub)
		}
	}
}

// TestDetails_UnboundCoordOmitsAgentWorktree pins that Agent/Worktree are
// dropped when the coordinator has no live binding, while the rest renders.
func TestDetails_UnboundCoordOmitsAgentWorktree(t *testing.T) {
	orch := &OrchView{
		ID:   1,
		Name: "o",
		Roles: []RoleView{
			{RoleID: 1, Name: "coord", Kind: db.HeraKindCoordinator}, // unbound: no TaskName/WorktreePath
		},
	}
	d := NewDetailsView()
	d.SetOrch(orch, nil)
	h := d.ContentHeight()
	testutil.Equal(t, rosterContains(t, d, h, "Agent:"), false)
	testutil.Equal(t, rosterContains(t, d, h, "Worktree:"), false)
	testutil.Equal(t, rosterContains(t, d, h, "Created:"), true)
	testutil.Equal(t, rosterContains(t, d, h, "auto-generated"), true)
}

func TestFmtDetailTime(t *testing.T) {
	testutil.Equal(t, fmtDetailTime(time.Time{}), "–")
	got := fmtDetailTime(time.Date(2026, 6, 1, 14, 5, 0, 0, time.UTC).Local())
	// Format is "YYYY-MM-DD HH:MM" — assert the shape, not the local offset.
	testutil.Equal(t, len(got), len("2026-06-01 14:05"))
}

func TestWorktreeDisplay(t *testing.T) {
	full := "/home/u/.argus/worktrees/Hera/the-task"
	testutil.Equal(t, worktreeDisplay(full, 100), full)           // fits → verbatim
	testutil.Equal(t, worktreeDisplay(full, 20), "Hera/the-task") // overflow → last two
	testutil.Equal(t, worktreeDisplay(full, 8), "the-task")       // still overflow → base
	testutil.Equal(t, worktreeDisplay(full, 0), full)             // nonpositive width → verbatim
}

func TestCoordRoleStatusLabel(t *testing.T) {
	// A genuinely active coordinator (live + running session + in_progress) reads
	// "working".
	testutil.Equal(t, coordRoleStatusLabel(&RoleView{HasStatus: true, Status: db.HeraStatusWorking, Live: true, SessionRunning: true, TaskStatus: "in_progress"}), "working")
	// BUG-003: a STALE "working" role-status that isn't backed by real activity
	// must not claim "working". A dead/stopped binding (not Live) reads "stopped".
	testutil.Equal(t, coordRoleStatusLabel(&RoleView{HasStatus: true, Status: db.HeraStatusWorking}), "stopped")
	// BUG-C: a DEAD coordinator whose binding lingers (Live but session not in the
	// running set) reads "live", NOT "working" — IsActive is false because the
	// session is not running.
	testutil.Equal(t, coordRoleStatusLabel(&RoleView{HasStatus: true, Status: db.HeraStatusWorking, Live: true, SessionRunning: false, TaskStatus: "in_review"}), "live")
	// BUG-036: a live+running-but-session-idle binding (parked, not producing)
	// reads "live" regardless of task status — IsActive false because SessionIdle.
	testutil.Equal(t, coordRoleStatusLabel(&RoleView{HasStatus: true, Status: db.HeraStatusWorking, Live: true, SessionRunning: true, TaskStatus: "in_review", SessionIdle: true}), "live")
	// BUG-C: a live, running, content-active coordinator in in_review (session
	// still producing during #707 close-out) is genuinely "working" — no longer
	// masked by the bound-task status.
	testutil.Equal(t, coordRoleStatusLabel(&RoleView{HasStatus: true, Status: db.HeraStatusWorking, Live: true, SessionRunning: true, TaskStatus: "in_review"}), "working")
	// Non-working role-status assertions pass through unchanged.
	testutil.Equal(t, coordRoleStatusLabel(&RoleView{HasStatus: true, Status: db.HeraStatusIdle}), "idle")
	testutil.Equal(t, coordRoleStatusLabel(&RoleView{Live: true}), "live")
	testutil.Equal(t, coordRoleStatusLabel(&RoleView{}), "—")
}

// BUG-015: the Details coordinator status line is task-aware — it appends a
// terminal bound-task state (in_review / complete / failed) to the role-status
// label, while ongoing/unbound tasks add no suffix.
func TestCoordTaskStatusLabel(t *testing.T) {
	// Terminal task states surface.
	testutil.Equal(t, coordTaskStatusLabel(&RoleView{TaskStatus: "complete"}), "complete")
	testutil.Equal(t, coordTaskStatusLabel(&RoleView{TaskStatus: "in_review"}), "in_review")
	// failed (from the opaque result blob) wins over the workflow status.
	testutil.Equal(t, coordTaskStatusLabel(&RoleView{TaskStatus: "complete", TaskResult: `{"failed":true}`}), "failed")
	testutil.Equal(t, coordTaskStatusLabel(&RoleView{TaskStatus: "in_review", TaskResult: `{"failed":true}`}), "failed")
	// Ongoing / unbound add no signal.
	testutil.Equal(t, coordTaskStatusLabel(&RoleView{TaskStatus: "in_progress"}), "")
	testutil.Equal(t, coordTaskStatusLabel(&RoleView{TaskStatus: "pending"}), "")
	testutil.Equal(t, coordTaskStatusLabel(&RoleView{}), "")
	// A non-failed or malformed result blob is tolerated (no failed suffix).
	testutil.Equal(t, coordTaskStatusLabel(&RoleView{TaskStatus: "complete", TaskResult: `{"failed":false}`}), "complete")
	testutil.Equal(t, coordTaskStatusLabel(&RoleView{TaskStatus: "complete", TaskResult: `{not json`}), "complete")
}

func TestCoordStatusLabel_Combined(t *testing.T) {
	// Active coordinator (live + running session), ongoing task → role status only.
	testutil.Equal(t, coordStatusLabel(&RoleView{HasStatus: true, Status: db.HeraStatusWorking, Live: true, SessionRunning: true, TaskStatus: "in_progress"}), "working")
	// Role + terminal task signal combine.
	testutil.Equal(t, coordStatusLabel(&RoleView{HasStatus: true, Status: db.HeraStatusDone, Live: true, TaskStatus: "complete"}), "done · task complete")
	// Stale-working honesty preserved when session-idle (BUG-036) AND the terminal
	// task state appended.
	testutil.Equal(t, coordStatusLabel(&RoleView{HasStatus: true, Status: db.HeraStatusWorking, Live: true, SessionRunning: true, TaskStatus: "in_review", SessionIdle: true}), "live · task in_review")
	// BUG-C: a live, running, content-active coordinator in in_review reads
	// "working" and still appends the terminal task state.
	testutil.Equal(t, coordStatusLabel(&RoleView{HasStatus: true, Status: db.HeraStatusWorking, Live: true, SessionRunning: true, TaskStatus: "in_review"}), "working · task in_review")
	// failed result blob.
	testutil.Equal(t, coordStatusLabel(&RoleView{HasStatus: true, Status: db.HeraStatusIdle, Live: true, TaskStatus: "complete", TaskResult: `{"failed":true}`}), "idle · task failed")
	// Unbound coordinator → no suffix.
	testutil.Equal(t, coordStatusLabel(&RoleView{}), "—")
}

// findRune scans the full w×h rect drawn by draw and reports whether r appears
// anywhere on screen — used below to locate the needs-input glyph without
// hardcoding row offsets that would drift with the panel's content layout.
func findRune(t *testing.T, draw func(tcell.Screen), w, h int, r rune) bool {
	t.Helper()
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	t.Cleanup(sim.Fini)
	sim.SetSize(w, h)
	draw(sim)
	sim.Show()
	cells, sw, sh := sim.GetContents()
	for i := 0; i < sw*sh; i++ {
		for _, cr := range cells[i].Runes {
			if cr == r {
				return true
			}
		}
	}
	return false
}

// TestDetails_CoordinatorStatusLine_NeedsInputOwnSignalOnly
// (remove-needs-input-rollup-glyph) pins the Details pane's `coordinator:`
// status line glyph to the SAME own-signal-only rule the rail now uses: the
// glyph reuses statusIcon (via roleStatusInputs -> ShowsNeedsInput), so a
// coordinator with only a blocked DESCENDANT (no own signal) must not show
// needs-input on its own status line, while a coordinator that is itself
// blocked must. Neither case was previously covered — existing Details
// coordinator-status tests (TestCoordStatusLabel_Combined etc.) only assert
// the TEXT label, never the glyph.
func TestDetails_CoordinatorStatusLine_NeedsInputOwnSignalOnly(t *testing.T) {
	draw := func(coord RoleView) func(tcell.Screen) {
		orch := &OrchView{ID: 1, Name: "orch", Roles: []RoleView{coord}}
		d := NewDetailsView()
		d.SetOrch(orch, nil)
		return func(s tcell.Screen) { d.Draw(s, 0, 0, 60, 20, false) }
	}

	t.Run("own signal shows the glyph", func(t *testing.T) {
		coord := RoleView{RoleID: 1, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "t-c", NeedsInput: true}
		testutil.Equal(t, findRune(t, draw(coord), 60, 20, theme.IconNeedsInput), true)
	})

	t.Run("descendant-only rollup does not show the glyph", func(t *testing.T) {
		// Simulates what BuildModel's rollupNeedsInput would stamp on a coordinator
		// whose descendant (not itself) is blocked: SubtreeNeedsInput true, own
		// signal false.
		coord := RoleView{RoleID: 1, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "t-c", HasStatus: true, Status: db.HeraStatusWorking, SubtreeNeedsInput: true}
		testutil.Equal(t, findRune(t, draw(coord), 60, 20, theme.IconNeedsInput), false)
	})
}

// TestDetails_RosterRow_NeedsInputOwnSignalOnly
// (remove-needs-input-rollup-glyph) is the roster-row half of the same rule: a
// bridging worker row that is itself a nested sub-coordinator (only reachable
// via a rollup on the un-narrowed classifier) must not show "(?)" from a
// descendant's rollup, in either the glyph (drawRosterRow -> statusIcon) or the
// text label (rosterStatusText). A row with its own signal set still must.
func TestDetails_RosterRow_NeedsInputOwnSignalOnly(t *testing.T) {
	t.Run("own signal: glyph and text both show needs-input", func(t *testing.T) {
		row := &RoleView{RoleID: 2, Name: "sub-coord", Kind: db.HeraKindWorker, Live: true, TaskID: "t-w", BridgeTaskID: "t-w", NeedsInput: true}
		testutil.Equal(t, rosterStatusText(row, false), "needs-input")

		orch := &OrchView{ID: 1, Name: "orch", Roles: []RoleView{
			{RoleID: 1, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "t-c"},
			*row,
		}}
		d := NewDetailsView()
		d.SetOrch(orch, nil)
		draw := func(s tcell.Screen) { d.Draw(s, 0, 0, 60, 20, false) }
		testutil.Equal(t, findRune(t, draw, 60, 20, theme.IconNeedsInput), true)
	})

	t.Run("descendant-only rollup: neither glyph nor text shows needs-input", func(t *testing.T) {
		// A bridging worker row that is itself a nested sub-coordinator, genuinely
		// active, with only a blocked descendant in its bridged child orchestrator
		// (SubtreeNeedsInput true, own signal false) — exactly the shape
		// TestPlanNodeIcon_BridgingSubCoordUnaffectedByDescendantRollup exercises
		// for the plan view.
		row := &RoleView{RoleID: 2, Name: "sub-coord", Kind: db.HeraKindWorker, Live: true, TaskID: "t-w", BridgeTaskID: "t-w", SessionRunning: true, SubtreeNeedsInput: true}
		text := rosterStatusText(row, false)
		if text == "needs-input" {
			t.Fatalf("expected a non-needs-input status text, got %q", text)
		}

		orch := &OrchView{ID: 1, Name: "orch", Roles: []RoleView{
			{RoleID: 1, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "t-c"},
			*row,
		}}
		d := NewDetailsView()
		d.SetOrch(orch, nil)
		draw := func(s tcell.Screen) { d.Draw(s, 0, 0, 60, 20, false) }
		testutil.Equal(t, findRune(t, draw, 60, 20, theme.IconNeedsInput), false)
	})
}

// testContains is a tiny substring helper (avoids importing strings just here).
func testContains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestRosterTruncate(t *testing.T) {
	testutil.Equal(t, rosterTruncate("hello", 10), "hello") // fits verbatim
	testutil.Equal(t, rosterTruncate("hello", 5), "hello")  // exact fit
	testutil.Equal(t, rosterTruncate("hello world", 5), "hell…")
	testutil.Equal(t, rosterTruncate("hello", 1), "h")
	testutil.Equal(t, rosterTruncate("hello", 0), "")
	testutil.Equal(t, rosterTruncate("hello", -1), "")
	// Rune-safe: a multibyte string must clip on a rune boundary, never a byte
	// mid-codepoint (the gotchas/pty-terminal.md rune-vs-byte truncation rule).
	testutil.Equal(t, rosterTruncate("café-société", 5), "café…")
	testutil.Equal(t, rosterTruncate("日本語のテキスト", 3), "日本…")
}

func TestArchetypeDisplay_ModelDisplay(t *testing.T) {
	testutil.Equal(t, archetypeDisplay(""), "—")
	testutil.Equal(t, archetypeDisplay("code_slice"), "code_slice")
	testutil.Equal(t, modelDisplay(""), "—")
	testutil.Equal(t, modelDisplay("claude-sonnet-5"), "claude-sonnet-5")
}

// TestRosterStatusText_Precedence pins the status-cell text to the SAME
// precedence widget.RoleStatusIcon uses (via roleStatusInputs), so the icon
// and the label never disagree (BUG-A: needs-input outranks ready_to_close).
func TestRosterStatusText_Precedence(t *testing.T) {
	testutil.Equal(t, rosterStatusText(&RoleView{}, false), "—")
	testutil.Equal(t, rosterStatusText(&RoleView{Live: true}, false), "live")
	testutil.Equal(t, rosterStatusText(&RoleView{HasStatus: true, Status: db.HeraStatusIdle}, false), "idle")
	testutil.Equal(t, rosterStatusText(&RoleView{HasStatus: true, Status: db.HeraStatusDone}, false), "done")
	testutil.Equal(t, rosterStatusText(&RoleView{HasStatus: true, Status: db.HeraStatusFailed}, false), "failed")
	testutil.Equal(t, rosterStatusText(&RoleView{ReadyToClose: true}, false), "ready")
	testutil.Equal(t, rosterStatusText(&RoleView{ReadyToClose: true}, true), "ready PR")
	testutil.Equal(t, rosterStatusText(&RoleView{Live: true, SessionRunning: true}, false), "working") // IsActive
	// NeedsInput outranks ReadyToClose (BUG-A) and Active.
	needsInput := &RoleView{Live: true, SessionRunning: true, NeedsInput: true, ReadyToClose: true}
	testutil.Equal(t, rosterStatusText(needsInput, false), "needs-input")
	// PR suffix composes with any underlying status, not just "ready".
	testutil.Equal(t, rosterStatusText(&RoleView{HasStatus: true, Status: db.HeraStatusIdle}, true), "idle PR")
}

func TestComputeRosterColumns(t *testing.T) {
	workers := []RoleView{
		{Name: "alpha", Archetype: "code_slice", AppliedModel: "claude-sonnet-5"},
		{Name: "a-very-long-agent-name-indeed-and-then-some", Archetype: "big_build", AppliedModel: "claude-opus-4-8"},
		{Name: "b", Archetype: "", AppliedModel: ""},
	}

	// Plenty of room: columns size to the widest cell, capped at the max
	// constants. "a-very-long-agent-name-indeed-and-then-some" is 43 runes
	// (clamps to rosterNameMax, which fits real project names up to ~30 chars
	// in full); "code_slice"/"claude-sonnet-5" are 10/15 runes (under their
	// caps, so the column sizes to the content, not the min/max).
	cols := computeRosterColumns(workers, 200)
	testutil.Equal(t, cols.status, rosterStatusWidth)
	testutil.Equal(t, cols.name, rosterNameMax)
	testutil.Equal(t, cols.archetype, 10)
	testutil.Equal(t, cols.model, 15)

	// An oversized archetype/model value clamps to the max constants too.
	capped := computeRosterColumns([]RoleView{{
		Name:         "x",
		Archetype:    "an-absurdly-long-archetype-name",
		AppliedModel: "an-absurdly-long-fully-qualified-model-identifier",
	}}, 200)
	testutil.Equal(t, capped.archetype, rosterArchMax)
	testutil.Equal(t, capped.model, rosterModelMax)

	// A narrow pane must never make computeRosterColumns return negative
	// widths, and once avail covers the fixed icon-gutter + inter-column gap
	// overhead, the computed columns must fit within it. Below that floor
	// (an extreme narrow pane) columns shrink to zero rather than corrupting
	// the layout — TestDetails_RosterTableNarrowPaneNoPanic pins that this
	// never panics.
	const fixedOverhead = rosterIconGutter + 3*rosterColGap
	for _, avail := range []int{0, -5, 1, 5, 8, 10, 20, 40, 80} {
		got := computeRosterColumns(workers, avail)
		testutil.Equal(t, got.status >= 0 && got.name >= 0 && got.archetype >= 0 && got.model >= 0, true)
		if avail >= fixedOverhead {
			testutil.Equal(t, rosterTotalWidth(got) <= avail, true)
		}
	}
}

// TestDetails_RosterTableColumns is the render smoke test: a coordinator's
// roster must show the NAME/ARCHETYPE/MODEL/STATUS header — in that order,
// name first and status last — and each worker's resolved archetype + model
// rendered brightly (theme.StyleNormal), with an unresolved worker rendering
// a dimmed "—" placeholder rather than a blank cell.
func TestDetails_RosterTableColumns(t *testing.T) {
	orch := &OrchView{
		ID:   1,
		Name: "my-orch",
		Roles: []RoleView{
			{RoleID: 1, OrchID: 1, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "t-c"},
			{
				RoleID: 2, OrchID: 1, Name: "alpha", Kind: db.HeraKindWorker, Live: true, TaskID: "t-a",
				Archetype: "code_slice", AppliedModel: "claude-sonnet-5",
			},
			{
				RoleID: 3, OrchID: 1, Name: "beta", Kind: db.HeraKindWorker, Live: true, TaskID: "t-b",
				// No archetype/model resolved (fail-open / no profile).
			},
		},
	}
	d := NewDetailsView()
	d.SetOrch(orch, nil)
	h := d.ContentHeight()
	const w = 90 // wide enough that no column shrinks/truncates the fixture values

	for _, want := range []string{"STATUS", "NAME", "ARCHETYPE", "MODEL", "code_slice", "claude-sonnet-5", "alpha", "beta"} {
		if !rosterContainsWidth(t, d, w, h, want) {
			t.Errorf("expected roster table to contain %q", want)
		}
	}
	// The unresolved worker's archetype/model cells render "—", not blank.
	if !rosterContainsWidth(t, d, w, h, "—") {
		t.Errorf("expected roster table to render \"—\" for the unresolved worker's archetype/model")
	}

	// Column ORDER: NAME first, then ARCHETYPE, then MODEL, then STATUS last
	// (the icon+label trailing verdict) — pinned on both the header row and
	// alpha's data row.
	headerRow, dataRow := "", ""
	for y := 0; y < h; y++ {
		got := drawnText(t, func(s tcell.Screen) { d.Draw(s, 0, 0, w, h, false) }, 0, y, w)
		if strings.Contains(got, "ARCHETYPE") {
			headerRow = got
		}
		if strings.Contains(got, "code_slice") {
			dataRow = got
		}
	}
	if headerRow == "" || dataRow == "" {
		t.Fatalf("expected to locate the roster header row and alpha's data row; header=%q data=%q", headerRow, dataRow)
	}
	nameH := strings.Index(headerRow, "NAME")
	archH := strings.Index(headerRow, "ARCHETYPE")
	modelH := strings.Index(headerRow, "MODEL")
	statusH := strings.Index(headerRow, "STATUS")
	testutil.Equal(t, nameH >= 0 && archH > nameH && modelH > archH && statusH > modelH, true)

	nameD := strings.Index(dataRow, "alpha")
	archD := strings.Index(dataRow, "code_slice")
	modelD := strings.Index(dataRow, "claude-sonnet-5")
	statusD := strings.Index(dataRow, "live") // alpha: Live, not SessionRunning → "live"
	testutil.Equal(t, nameD >= 0 && archD > nameD && modelD > archD && statusD > modelD, true)

	// Bright values: the resolved archetype/model cells render in the same
	// readable foreground the NAME cell uses (theme.StyleNormal), not the
	// dimmed placeholder style reserved for an unresolved "—" cell.
	testutil.Equal(t, rosterValueStyle("code_slice"), theme.StyleNormal)
	testutil.Equal(t, rosterValueStyle("claude-sonnet-5"), theme.StyleNormal)
	testutil.Equal(t, rosterValueStyle(""), theme.StyleDimmed)
}

// TestDetails_RosterTableNarrowPaneNoPanic pins that a details pane too
// narrow to fit the ideal column widths still renders (shrunk/truncated
// columns) without panicking or hanging.
func TestDetails_RosterTableNarrowPaneNoPanic(t *testing.T) {
	orch := &OrchView{
		ID:   1,
		Name: "my-orch",
		Roles: []RoleView{
			{RoleID: 1, OrchID: 1, Name: "coord", Kind: db.HeraKindCoordinator},
			{
				RoleID: 2, OrchID: 1, Name: "a-very-long-worker-name", Kind: db.HeraKindWorker,
				Archetype: "big_build", AppliedModel: "claude-opus-4-8",
			},
		},
	}
	d := NewDetailsView()
	d.SetOrch(orch, nil)
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(80, 30)
	for _, w := range []int{2, 5, 10, 20} {
		d.Draw(sim, 0, 0, w, 20, false)
	}
}

// bigRoster builds a coordinator with n workers, far more than a modest pane
// can show at once — the fixture the roster-scroll tests need.
func bigRoster(n int) *OrchView {
	roles := []RoleView{{RoleID: 1, OrchID: 1, Name: "coord", Kind: db.HeraKindCoordinator}}
	for i := 1; i <= n; i++ {
		roles = append(roles, RoleView{
			RoleID: int64(i + 1), OrchID: 1, Name: fmt.Sprintf("agent-%02d", i), Kind: db.HeraKindWorker,
		})
	}
	return &OrchView{ID: 1, Name: "big-orch", Roles: roles}
}

// TestDetails_RosterScrolls is the render smoke test the scroll requirement
// needs: a 20-agent roster drawn in a pane too short to show them all must
// make the LAST agent reachable by scrolling down, and the FIRST agent
// reachable again by scrolling back up — nothing is permanently cut off.
func TestDetails_RosterScrolls(t *testing.T) {
	d := NewDetailsView()
	d.SetOrch(bigRoster(20), nil)
	const w, h = 60, 20

	// Draw once so rosterVisibleRows reflects this pane's real budget (mirrors
	// a live app: Draw always runs before any keypress can act on it).
	testutil.Equal(t, rosterContainsWidth(t, d, w, h, "agent-01"), true)
	testutil.Equal(t, rosterContainsWidth(t, d, w, h, "agent-20"), false) // cut off before scrolling

	for i := 0; i < 30; i++ { // far more presses than needed; ScrollRoster clamps
		if !d.ScrollRoster(1) {
			break
		}
	}
	testutil.Equal(t, rosterContainsWidth(t, d, w, h, "agent-20"), true) // reachable now
	testutil.Equal(t, d.ScrollRoster(1), false)                          // already at the bound

	for i := 0; i < 30; i++ {
		if !d.ScrollRoster(-1) {
			break
		}
	}
	testutil.Equal(t, rosterContainsWidth(t, d, w, h, "agent-01"), true) // back at the top
	testutil.Equal(t, d.ScrollRoster(-1), false)                         // already at the bound
}

// TestDetails_ScrollRosterNoopsBeforeFirstDraw pins that ScrollRoster is a
// harmless no-op before any Draw has established a real row budget (avoids
// scrolling against a stale/zero rosterVisibleRows).
func TestDetails_ScrollRosterNoopsBeforeFirstDraw(t *testing.T) {
	d := NewDetailsView()
	d.SetOrch(bigRoster(20), nil)
	testutil.Equal(t, d.ScrollRoster(1), false)
	testutil.Equal(t, d.ScrollRoster(-1), false)
}

// TestDetails_ScrollRosterNoopsWhenEverythingFits pins that a small roster
// (fits entirely within the pane) never consumes a scroll keypress — so
// j/k/Up/Down fall straight through to the embedded plan widget as before.
func TestDetails_ScrollRosterNoopsWhenEverythingFits(t *testing.T) {
	d := NewDetailsView()
	d.SetOrch(bigRoster(2), nil)
	testutil.Equal(t, rosterContainsWidth(t, d, 60, 30, "agent-01"), true)
	testutil.Equal(t, rosterContainsWidth(t, d, 60, 30, "agent-02"), true)
	testutil.Equal(t, d.ScrollRoster(1), false)
	testutil.Equal(t, d.ScrollRoster(-1), false)
}

// TestDetails_SetOrchScrollReset pins WHEN the roster's scroll offset resets:
// a genuine selection change (different orchestrator) resets to the top, but
// re-selecting the SAME orchestrator (the ~1s refresh-tick case) preserves
// the operator's scroll position instead of snapping it back every tick.
func TestDetails_SetOrchScrollReset(t *testing.T) {
	d := NewDetailsView()
	orchA := bigRoster(20)
	d.SetOrch(orchA, nil)
	rosterContainsWidth(t, d, 60, 20, "agent-01") // Draw once to seed the budget
	testutil.Equal(t, d.ScrollRoster(5), true)
	testutil.Equal(t, d.rosterScroll > 0, true)

	// Same orchestrator (same ID) re-selected — e.g. a refresh tick rebuilding
	// the model — must NOT reset the scroll position.
	orchARefreshed := bigRoster(20)
	d.SetOrch(orchARefreshed, nil)
	testutil.Equal(t, d.rosterScroll > 0, true)

	// A genuinely different orchestrator resets to the top.
	orchB := &OrchView{ID: 2, Name: "other", Roles: []RoleView{
		{RoleID: 1, OrchID: 2, Name: "coord", Kind: db.HeraKindCoordinator},
	}}
	d.SetOrch(orchB, nil)
	testutil.Equal(t, d.rosterScroll, 0)
}

// TestDetails_ClampRosterScrollNegative pins the defensive floor: a
// scrollOffset that somehow went negative (never possible via ScrollRoster,
// which floors at 0 itself, but clampRosterScroll is the general re-bound
// contract) is corrected to 0, not left negative.
func TestDetails_ClampRosterScrollNegative(t *testing.T) {
	d := NewDetailsView()
	d.SetOrch(bigRoster(20), nil)
	d.rosterScroll = -7
	d.clampRosterScroll(20)
	testutil.Equal(t, d.rosterScroll, 0)
}

// TestDetails_ClampRosterScrollAboveMax pins the other half of the re-bound:
// a scroll offset past the current maxScroll (e.g. the roster SHRANK — an
// agent completed and dropped off) is pulled back down to the new max.
func TestDetails_ClampRosterScrollAboveMax(t *testing.T) {
	d := NewDetailsView()
	d.SetOrch(bigRoster(20), nil)
	d.rosterVisibleRows = 5
	d.rosterScroll = 100
	d.clampRosterScroll(20)
	testutil.Equal(t, d.rosterScroll, d.rosterMaxScroll(20))
	testutil.Equal(t, d.rosterScroll < 100, true)
}

func TestRosterScrollDelta(t *testing.T) {
	down, ok := rosterScrollDelta(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	testutil.Equal(t, ok, true)
	testutil.Equal(t, down, 1)

	up, ok := rosterScrollDelta(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone))
	testutil.Equal(t, ok, true)
	testutil.Equal(t, up, -1)

	jDown, ok := rosterScrollDelta(tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone))
	testutil.Equal(t, ok, true)
	testutil.Equal(t, jDown, 1)

	kUp, ok := rosterScrollDelta(tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModNone))
	testutil.Equal(t, ok, true)
	testutil.Equal(t, kUp, -1)

	// Any other key (h/l, Enter, Esc, an unrelated rune) is not a scroll key.
	for _, ev := range []*tcell.EventKey{
		tcell.NewEventKey(tcell.KeyRune, 'h', tcell.ModNone),
		tcell.NewEventKey(tcell.KeyRune, 'l', tcell.ModNone),
		tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone),
		tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone),
		tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone),
	} {
		_, ok := rosterScrollDelta(ev)
		testutil.Equal(t, ok, false)
	}
}
