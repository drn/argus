package hera

import (
	"testing"

	"github.com/drn/argus/internal/db"
	heramodel "github.com/drn/argus/internal/hera/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/gdamore/tcell/v2"
)

// selectedName returns the selected row's role name, or the orchestrator name
// for a header row, or "" for anything else.
func selectedName(r *Rail) string {
	if role := r.Selected(); role != nil {
		return role.Name
	}
	if o := r.SelectedOrch(); o != nil {
		return o.Name
	}
	return ""
}

func TestRail_SetFilter(t *testing.T) {
	t.Run("narrows non-interactively and auto-selects the first match", func(t *testing.T) {
		r := NewRail()
		r.SetModel(filterModel())
		r.SetFilter("gamma")
		testutil.False(t, r.Filtering()) // never enters input mode
		testutil.Equal(t, r.Filter(), "gamma")
		testutil.Equal(t, selectedName(r), "gamma")
		testutil.Equal(t, r.depthOf("alpha"), -1)
		testutil.True(t, r.HasSelectableRow())
	})

	t.Run("no match leaves no selectable row", func(t *testing.T) {
		r := NewRail()
		r.SetModel(filterModel())
		r.SetFilter("nothing-matches")
		testutil.False(t, r.HasSelectableRow())
	})

	t.Run("empty query restores the pre-filter selection and folds", func(t *testing.T) {
		r := NewRail()
		r.SetModel(filterModel())
		r.collapsed[9] = false
		testutil.True(t, r.SelectByTaskID("t-alpha"))
		r.SetFilter("g")
		r.SetFilter("gam")
		testutil.Equal(t, selectedName(r), "gamma")
		r.SetFilter("")
		testutil.Equal(t, r.Filter(), "")
		testutil.Equal(t, selectedName(r), "alpha")
	})

	t.Run("refocuses the kanban group holding the only match", func(t *testing.T) {
		r := NewRail()
		r.SetModel(fourGroupModel())
		testutil.Equal(t, r.FocusedKanban(), db.HeraKanbanActive)
		r.SetFilter("blk")
		testutil.Equal(t, r.FocusedKanban(), db.HeraKanbanBlocked)
		testutil.Equal(t, selectedName(r), "blk")
	})

	t.Run("same query is a no-op", func(t *testing.T) {
		r := NewRail()
		r.SetModel(filterModel())
		r.SetFilter("a")
		r.CursorDown()
		before := r.CursorIndex()
		r.SetFilter("a")
		testutil.Equal(t, r.CursorIndex(), before)
	})
}

func TestRail_HasSelectableRow_Empty(t *testing.T) {
	r := NewRail()
	r.SetModel(heramodel.Model{})
	testutil.False(t, r.HasSelectableRow())
}

func TestRail_CopyFoldStateFrom(t *testing.T) {
	src := NewRail()
	store := &fakeStore{}
	src.SetStateStore(store)
	src.SetModel(filterModel())
	src.collapsed[1] = true
	src.coordArchiveOpen[1] = true
	src.freelanceCollap = true
	src.archiveCollapsed = false
	src.focusedKanban = db.HeraKanbanBacklog
	saves := len(store.saved)

	cp := NewRail()
	cp.CopyFoldStateFrom(src)
	cp.SetModel(filterModel())

	t.Run("copies every fold", func(t *testing.T) {
		testutil.True(t, cp.OrchCollapsed(1))
		testutil.True(t, cp.coordArchiveOpen[1])
		testutil.True(t, cp.freelanceCollap)
		testutil.False(t, cp.archiveCollapsed)
		testutil.Equal(t, cp.FocusedKanban(), db.HeraKanbanBacklog)
	})

	t.Run("mutating the copy leaves the source unchanged and never persists", func(t *testing.T) {
		cp.collapsed[1] = false
		cp.coordArchiveOpen[1] = false
		cp.ToggleCollapse()
		cp.CursorDown()
		testutil.True(t, src.OrchCollapsed(1))
		testutil.True(t, src.coordArchiveOpen[1])
		testutil.Equal(t, len(store.saved), saves)
	})

	t.Run("rebuilds rows when the copy already has a model", func(t *testing.T) {
		live := NewRail()
		live.SetModel(filterModel())
		testutil.True(t, live.depthOf("alpha") >= 0)
		live.CopyFoldStateFrom(src)
		testutil.Equal(t, live.depthOf("alpha"), -1) // R now folded
	})

	t.Run("nil source is a no-op", func(t *testing.T) {
		r := NewRail()
		r.CopyFoldStateFrom(nil)
		testutil.Equal(t, r.FocusedKanban(), db.HeraKanbanActive)
	})
}

func TestRail_CursorToNextNeedsInput(t *testing.T) {
	m := filterModel()
	m.Active[0].Roles[1].NeedsInput = true // alpha
	m.Active[1].Roles[1].NeedsInput = true // gamma
	r := NewRail()
	r.SetModel(m)

	t.Run("from the top", func(t *testing.T) {
		testutil.True(t, r.CursorToNextNeedsInput(true))
		testutil.Equal(t, selectedName(r), "alpha")
	})
	t.Run("strictly after the cursor", func(t *testing.T) {
		testutil.True(t, r.CursorToNextNeedsInput(false))
		testutil.Equal(t, selectedName(r), "gamma")
	})
	t.Run("does not wrap past the last candidate", func(t *testing.T) {
		before := r.CursorIndex()
		testutil.False(t, r.CursorToNextNeedsInput(false))
		testutil.Equal(t, r.CursorIndex(), before)
	})
	t.Run("none at all", func(t *testing.T) {
		plain := NewRail()
		plain.SetModel(filterModel())
		testutil.False(t, plain.CursorToNextNeedsInput(true))
	})
}

func TestRail_SelectOrch(t *testing.T) {
	t.Run("top-level header", func(t *testing.T) {
		r := NewRail()
		r.SetModel(filterModel())
		testutil.True(t, r.SelectByTaskID("t-alpha"))
		testutil.True(t, r.SelectOrch(1))
		testutil.Equal(t, r.SelectedOrch().ID, int64(1))
		testutil.True(t, r.Selected() == nil)
	})

	t.Run("nested orch lands on its bridging row and expands ancestors only", func(t *testing.T) {
		r := NewRail()
		r.SetModel(filterModel())
		r.collapsed[1] = true
		r.collapsed[2] = true
		r.buildRows()
		testutil.True(t, r.SelectOrch(2))
		testutil.Equal(t, selectedName(r), "bridge")
		testutil.Equal(t, r.Selection().BridgeChildOrchID, int64(2))
		testutil.False(t, r.OrchCollapsed(1)) // ancestor expanded
		testutil.True(t, r.OrchCollapsed(2))  // target's own fold kept
	})

	t.Run("refocuses the kanban group", func(t *testing.T) {
		r := NewRail()
		r.SetModel(fourGroupModel())
		testutil.True(t, r.SelectOrch(4))
		testutil.Equal(t, r.FocusedKanban(), db.HeraKanbanDone)
		testutil.Equal(t, r.SelectedOrch().ID, int64(4))
	})

	t.Run("unknown orch", func(t *testing.T) {
		r := NewRail()
		r.SetModel(filterModel())
		testutil.False(t, r.SelectOrch(404))
		testutil.False(t, r.SelectOrch(0))
	})
}

func TestRail_PanelTitleAndCursorDim(t *testing.T) {
	draw := func(r *Rail) tcell.SimulationScreen {
		t.Helper()
		screen := tcell.NewSimulationScreen("UTF-8")
		testutil.NoError(t, screen.Init())
		screen.SetSize(40, 12)
		r.SetRect(0, 0, 40, 12)
		r.Draw(screen)
		return screen
	}
	rowText := func(s tcell.SimulationScreen, y int) string {
		var out []rune
		for x := 0; x < 40; x++ {
			c, _, _ := s.Get(x, y)
			out = append(out, []rune(c)...)
		}
		return string(out)
	}

	t.Run("default title is Hera", func(t *testing.T) {
		r := NewRail()
		r.SetModel(filterModel())
		testutil.Contains(t, rowText(draw(r), 0), " Hera ")
	})

	t.Run("custom title keeps the filter suffix", func(t *testing.T) {
		r := NewRail()
		r.SetPanelTitle("Projects")
		r.SetModel(filterModel())
		r.SetFilter("gam")
		testutil.Contains(t, rowText(draw(r), 0), " Projects /gam ")
	})

	t.Run("dimmed cursor draws a dim marker and unselected text", func(t *testing.T) {
		r := NewRail()
		r.SetModel(filterModel())
		r.SetCursorDimmed(true)
		s := draw(r)
		y := 1 + r.CursorIndex()
		mainc, style, _ := s.Get(1, y)
		testutil.Equal(t, mainc, "›")
		testutil.Equal(t, style, theme.StyleDimmed)
		found := false
		for x := 3; x < 39; x++ {
			if _, st, _ := s.Get(x, y); st == theme.StyleSelected {
				found = true
			}
		}
		testutil.False(t, found)
	})
}

func TestHeraPage_SelectOrch(t *testing.T) {
	t.Run("lands on a coordinator-less orchestrator header with rail focus", func(t *testing.T) {
		d := memDB(t)
		orch := seedOrch(t, d, "lonely")
		seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "tw")
		p := NewHeraPage(d)
		p.Refresh()
		testutil.True(t, p.Rail().SelectByTaskID("tw"))

		testutil.True(t, p.SelectOrch(orch))
		sel := p.Rail().Selection()
		testutil.True(t, sel.Role == nil)
		testutil.Equal(t, sel.Orch.ID, orch)
		testutil.Equal(t, p.Machine().State(), FocusRail)
	})

	t.Run("unknown orchestrator", func(t *testing.T) {
		d := memDB(t)
		p := NewHeraPage(d)
		p.Refresh()
		testutil.False(t, p.SelectOrch(77))
	})

	t.Run("remote mode is inert", func(t *testing.T) {
		d := memDB(t)
		orch := seedOrch(t, d, "o")
		p := NewHeraPage(d)
		p.Refresh()
		p.remote = true
		testutil.False(t, p.SelectOrch(orch))
	})
}

func TestRail_SelectOrch_ArchivedRootOpensArchive(t *testing.T) {
	r := NewRail()
	r.SetModel(filterModel())
	testutil.True(t, r.archiveCollapsed)
	testutil.True(t, r.SelectOrch(9))
	testutil.False(t, r.archiveCollapsed)
	testutil.Equal(t, r.SelectedOrch().ID, int64(9))
}
