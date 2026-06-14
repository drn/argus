package hera

import (
	"testing"

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
		{"nil orch", nil, 3}, // border + placeholder line
		{"coord, no workers", &OrchView{ID: 1, Roles: []RoleView{coord}}, 8},                           // 2 + (4+1) + 1(none)
		{"coord + 2 workers", &OrchView{ID: 1, Roles: []RoleView{coord, wkr(2, "a"), wkr(3, "b")}}, 9}, // 2 + 5 + 2
		{"no coord role, 2 workers", &OrchView{ID: 1, Roles: []RoleView{wkr(2, "a"), wkr(3, "b")}}, 8}, // 2 + 4 + 2 (no coord line)
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
// the EXACT minimum height at which Draw renders the full roster: at h ==
// ContentHeight the last worker is visible; at h-1 it is truncated. This guards
// the formula against drifting from Draw's actual row budget. Both the
// coordinator-present (content=5) and coordinator-absent (content=4, the W1 fix)
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
			testutil.Equal(t, rosterContains(t, d, ch, "zlast"), true)    // fits exactly
			testutil.Equal(t, rosterContains(t, d, ch-1, "zlast"), false) // one short → truncated
		})
	}
}

func TestCoordStatusLabel(t *testing.T) {
	testutil.Equal(t, coordStatusLabel(&RoleView{HasStatus: true, Status: db.HeraStatusWorking}), "working")
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
