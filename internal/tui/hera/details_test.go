package hera

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
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

	// roleMark composes ready + PR.
	d.prMeta = map[string]map[string]string{"t": {"url": "u"}}
	rc := &RoleView{TaskID: "t", ReadyToClose: true}
	testutil.Equal(t, d.roleMark(rc), "ready PR")
	noMark := &RoleView{TaskID: "none"}
	testutil.Equal(t, d.roleMark(noMark), "")
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

// rosterContains reports whether any row of a DetailsView drawn at the given
// height contains sub (scans the full inner width).
func rosterContains(t *testing.T, d *DetailsView, h int, sub string) bool {
	t.Helper()
	for y := range h {
		if testContains(drawnText(t, func(s tcell.Screen) { d.Draw(s, 0, 0, 40, h, false) }, 1, y, 36), sub) {
			return true
		}
	}
	return false
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
		// + reposRows(1 "(none)") + workerRows(max(n,1)). The test roles carry no
		// ArgusProject and the coord is unbound, so agent/worktree are omitted and
		// repos is the "(none)" line.
		{"nil orch", nil, 3}, // border + placeholder line
		{"coord, no workers", &OrchView{ID: 1, Roles: []RoleView{coord}}, 16},                           // 2 + 11 + 1 + 1(repos none) + 1(workers none)
		{"coord + 2 workers", &OrchView{ID: 1, Roles: []RoleView{coord, wkr(2, "a"), wkr(3, "b")}}, 17}, // 2 + 11 + 1 + 1 + 2
		{"no coord role, 2 workers", &OrchView{ID: 1, Roles: []RoleView{wkr(2, "a"), wkr(3, "b")}}, 16}, // 2 + 11 + 0 + 1 + 2
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

func TestCoordStatusLabel(t *testing.T) {
	// A genuinely active coordinator (live binding + bound task in_progress) reads
	// "working".
	testutil.Equal(t, coordStatusLabel(&RoleView{HasStatus: true, Status: db.HeraStatusWorking, Live: true, TaskStatus: "in_progress"}), "working")
	// BUG-003: a STALE "working" role-status that isn't backed by real activity
	// must not claim "working". A dead/stopped binding reads "stopped"; a live
	// binding no longer in_progress reads "live".
	testutil.Equal(t, coordStatusLabel(&RoleView{HasStatus: true, Status: db.HeraStatusWorking}), "stopped")
	testutil.Equal(t, coordStatusLabel(&RoleView{HasStatus: true, Status: db.HeraStatusWorking, Live: true, TaskStatus: "in_review"}), "live")
	// Non-working role-status assertions pass through unchanged.
	testutil.Equal(t, coordStatusLabel(&RoleView{HasStatus: true, Status: db.HeraStatusIdle}), "idle")
	testutil.Equal(t, coordStatusLabel(&RoleView{Live: true}), "live")
	testutil.Equal(t, coordStatusLabel(&RoleView{}), "—")
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
