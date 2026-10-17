package tui

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/db"
	heramodel "github.com/drn/argus/internal/hera/model"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/hera"
	"github.com/drn/argus/internal/tui/taskview"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// switcherTasks: two plain projects, one hera worker, one hera coordinator,
// and one archived task.
func switcherTasks() []*model.Task {
	return []*model.Task{
		{ID: "a1", Name: "apple-one", Project: "alpha", Status: model.StatusInProgress},
		{ID: "a2", Name: "apple-two", Project: "alpha", Status: model.StatusInProgress},
		{ID: "b1", Name: "banana-one", Project: "beta", Status: model.StatusInProgress},
		{ID: "hw", Name: "hera-worker", Project: "alpha", Status: model.StatusInProgress},
		{ID: "hc", Name: "hera-coord", Project: "alpha", Status: model.StatusInProgress},
		{ID: "old", Name: "old-archived", Project: "alpha", Status: model.StatusInProgress, Archived: true},
	}
}

// switcherModel: orch 1 "orchard" (coordinator hc) with worker "wkr-cherry"
// (task hw), and orch 2 "lonely" with no coordinator and worker "wkr-date".
func switcherModel() heramodel.Model {
	return heramodel.Model{Active: []heramodel.OrchView{
		{ID: 1, Name: "orchard", Roles: []heramodel.RoleView{
			{RoleID: 11, OrchID: 1, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "hc", BridgeTaskID: "hc"},
			{RoleID: 12, OrchID: 1, Name: "wkr-cherry", Kind: db.HeraKindWorker, Live: true, TaskID: "hw", BridgeTaskID: "hw"},
		}},
		{ID: 2, Name: "lonely", Roles: []heramodel.RoleView{
			{RoleID: 21, OrchID: 2, Name: "wkr-date", Kind: db.HeraKindWorker, Live: true, TaskID: "hd", BridgeTaskID: "hd"},
		}},
	}}
}

func switcherFeed() SwitcherFeed {
	return SwitcherFeed{
		Tasks:       switcherTasks(),
		Managed:     map[string]bool{"hc": true},
		HeraWorkers: map[string]bool{"hw": true},
		HeraModel:   switcherModel(),
	}
}

func newPopup(t *testing.T, feed SwitcherFeed, seed SwitcherSeed) *SwitcherPopup {
	t.Helper()
	return NewSwitcherPopup(feed, seed)
}

func press(p *SwitcherPopup, key tcell.Key, r rune) {
	p.InputHandler()(tcell.NewEventKey(key, r, tcell.ModNone), func(tview.Primitive) {})
}

func typeText(p *SwitcherPopup, s string) {
	for _, r := range s {
		press(p, tcell.KeyRune, r)
	}
}

func selTask(p *SwitcherPopup) string {
	if t := p.tasks.SelectedTask(); t != nil {
		return t.ID
	}
	return ""
}

func selRailName(p *SwitcherPopup) string {
	if role := p.rail.Selected(); role != nil {
		return role.Name
	}
	if o := p.rail.SelectedOrch(); o != nil {
		return o.Name
	}
	return ""
}

func drawPopup(t *testing.T, p *SwitcherPopup, w, h int) tcell.SimulationScreen {
	t.Helper()
	screen := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, screen.Init())
	screen.SetSize(w, h)
	p.SetRect(0, 0, w, h)
	p.Draw(screen)
	return screen
}

func screenRow(s tcell.SimulationScreen, y, w int) string {
	var b strings.Builder
	for x := 0; x < w; x++ {
		c, _, _ := s.Get(x, y)
		b.WriteString(c)
	}
	return b.String()
}

func screenText(s tcell.SimulationScreen, w, h int) string {
	var b strings.Builder
	for y := 0; y < h; y++ {
		b.WriteString(screenRow(s, y, w))
		b.WriteByte('\n')
	}
	return b.String()
}

func TestSwitcherPopup_Layout(t *testing.T) {
	p := newPopup(t, switcherFeed(), SwitcherSeed{})
	s := drawPopup(t, p, 120, 30)
	text := screenText(s, 120, 30)

	t.Run("Tasks left, Projects right", func(t *testing.T) {
		var tasksX, projX = -1, -1
		for y := 0; y < 30; y++ {
			row := screenRow(s, y, 120)
			if i := strings.Index(row, " Tasks "); i >= 0 && tasksX < 0 {
				tasksX = i
			}
			if i := strings.Index(row, " Projects "); i >= 0 && projX < 0 {
				projX = i
			}
		}
		testutil.True(t, tasksX >= 0)
		testutil.True(t, projX > tasksX)
		for y := 0; y < 30; y++ {
			row := []rune(screenRow(s, y, 120))
			if i := strings.Index(string(row), "apple-one"); i >= 0 {
				testutil.True(t, len([]rune(string(row)[:i])) < 60)
			}
		}
		testutil.Contains(t, text, "orchard")
	})

	t.Run("footer hint", func(t *testing.T) {
		testutil.Contains(t, text, "ctrl+g next (?)")
		testutil.Contains(t, text, "esc clear/close")
	})

	t.Run("full-rect coverage clears stale cells", func(t *testing.T) {
		screen := tcell.NewSimulationScreen("UTF-8")
		testutil.NoError(t, screen.Init())
		screen.SetSize(100, 30)
		for y := 0; y < 30; y++ {
			for x := 0; x < 100; x++ {
				screen.SetContent(x, y, 'X', nil, tcell.StyleDefault)
			}
		}
		p.SetRect(0, 0, 100, 30)
		p.Draw(screen)
		ox, oy, ow, oh := p.overlayRect()
		for y := oy; y < oy+oh; y++ {
			for x := ox; x < ox+ow; x++ {
				c, _, _ := screen.Get(x, y)
				if c == "X" {
					t.Fatalf("stale cell at %d,%d", x, y)
				}
			}
		}
	})

	t.Run("tiny screen does not panic", func(t *testing.T) {
		for _, sz := range [][2]int{{1, 1}, {8, 4}, {20, 6}, {0, 0}} {
			drawPopup(t, p, sz[0], sz[1])
		}
	})
}

func TestSwitcherPopup_TasksColumnExclusions(t *testing.T) {
	p := newPopup(t, switcherFeed(), SwitcherSeed{TasksExpanded: "alpha"})
	ids := strings.Join(p.tasks.VisibleTaskIDs(), ",")
	for _, hidden := range []string{"hw", "hc", "old"} {
		t.Run(hidden, func(t *testing.T) {
			testutil.False(t, strings.Contains(ids, hidden))
		})
	}
	testutil.True(t, p.tasks.HideHeraManaged())
	testutil.Contains(t, ids, "a1")
}

// TestSwitcherPopup_StaleCoordinatorMetaStaysVisible: hera.role=coordinator
// task_meta is never cleared when a coordinator binding ends, so it is NOT a
// hide signal — an ex-coordinator with no live binding has no Projects row and
// must stay reachable in the Tasks column (the real list hides only
// isHeraSpawnedWorker || managed with H on).
func TestSwitcherPopup_StaleCoordinatorMetaStaysVisible(t *testing.T) {
	f := switcherFeed()
	f.HeraCoords = map[string]bool{"a2": true, "hc": true}
	p := newPopup(t, f, SwitcherSeed{TasksExpanded: "alpha"})
	ids := strings.Join(p.tasks.VisibleTaskIDs(), ",")
	testutil.Contains(t, ids, "a2")
	testutil.True(t, p.taskIDs["a2"])
	testutil.False(t, p.taskIDs["hc"]) // still hidden: it holds a live binding (Managed)
}

func TestSwitcherPopup_Navigation(t *testing.T) {
	p := newPopup(t, switcherFeed(), SwitcherSeed{})
	testutil.Equal(t, p.ActiveColumn(), SwitcherColumnTasks)
	testutil.Equal(t, selTask(p), "a1")

	press(p, tcell.KeyDown, 0)
	testutil.Equal(t, selTask(p), "a2")

	press(p, tcell.KeyRight, 0)
	testutil.Equal(t, p.ActiveColumn(), SwitcherColumnProjects)
	startRail := selRailName(p)
	press(p, tcell.KeyDown, 0)
	testutil.NotEqual(t, selRailName(p), startRail)

	press(p, tcell.KeyLeft, 0)
	testutil.Equal(t, p.ActiveColumn(), SwitcherColumnTasks)
	testutil.Equal(t, selTask(p), "a2") // kept its own cursor

	press(p, tcell.KeyUp, 0)
	testutil.Equal(t, selTask(p), "a1")
	press(p, tcell.KeyRight, 0)
	press(p, tcell.KeyUp, 0)
	testutil.Equal(t, selRailName(p), startRail)
}

func TestSwitcherPopup_SpaceFoldsProjects(t *testing.T) {
	p := newPopup(t, switcherFeed(), SwitcherSeed{})
	press(p, tcell.KeyRune, ' ') // Tasks column: no fold, no query
	testutil.Equal(t, p.Query(), "")

	press(p, tcell.KeyRight, 0)
	testutil.Equal(t, selRailName(p), "orchard")
	press(p, tcell.KeyRune, ' ')
	testutil.True(t, p.rail.OrchCollapsed(1))
	testutil.Equal(t, p.Query(), "")
	press(p, tcell.KeyRune, ' ')
	testutil.False(t, p.rail.OrchCollapsed(1))
}

func TestSwitcherPopup_Filtering(t *testing.T) {
	t.Run("first keystroke filters both columns", func(t *testing.T) {
		p := newPopup(t, switcherFeed(), SwitcherSeed{})
		railRows := p.rail.Rows()
		press(p, tcell.KeyRune, 't') // ONE rune
		testutil.Equal(t, p.Query(), "t")
		// Tasks: apple-two (name) and banana-one (project "beta"); apple-one gone.
		testutil.DeepEqual(t, p.tasks.VisibleTaskIDs(), []string{"a2", "b1"})
		// Projects: only wkr-date (and its ancestry) survives.
		testutil.Equal(t, p.rail.Filter(), "t")
		testutil.True(t, p.rail.Rows() < railRows)
		press(p, tcell.KeyRight, 0)
		testutil.Equal(t, selRailName(p), "wkr-date")
	})

	t.Run("empty active column hands off to the other", func(t *testing.T) {
		p := newPopup(t, switcherFeed(), SwitcherSeed{})
		typeText(p, "cherry")
		testutil.Equal(t, p.ActiveColumn(), SwitcherColumnProjects)
		testutil.Equal(t, selRailName(p), "wkr-cherry")
	})

	t.Run("no handoff when neither column matches", func(t *testing.T) {
		p := newPopup(t, switcherFeed(), SwitcherSeed{})
		typeText(p, "zzz")
		testutil.Equal(t, p.ActiveColumn(), SwitcherColumnTasks)
	})

	t.Run("backspace, ctrl+w and ctrl+u edit the query", func(t *testing.T) {
		p := newPopup(t, switcherFeed(), SwitcherSeed{})
		typeText(p, "apple tw")
		testutil.Equal(t, p.Query(), "appletw") // Space is not filter text
		press(p, tcell.KeyBackspace2, 0)
		testutil.Equal(t, p.Query(), "applet")
		p.query = []rune("apple two")
		p.applyQuery()
		press(p, tcell.KeyCtrlW, 0)
		testutil.Equal(t, p.Query(), "apple ")
		press(p, tcell.KeyCtrlU, 0)
		testutil.Equal(t, p.Query(), "")
		press(p, tcell.KeyBackspace, 0) // empty: no-op
		testutil.Equal(t, p.Query(), "")
		typeText(p, "ab")
		p.InputHandler()(tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModAlt), func(tview.Primitive) {})
		testutil.Equal(t, p.Query(), "")
	})

	t.Run("alt-modified runes are not filter text", func(t *testing.T) {
		p := newPopup(t, switcherFeed(), SwitcherSeed{})
		p.InputHandler()(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModAlt), func(tview.Primitive) {})
		testutil.Equal(t, p.Query(), "")
	})

	t.Run("paste appends to the query without newlines", func(t *testing.T) {
		p := newPopup(t, switcherFeed(), SwitcherSeed{})
		p.PasteHandler()("ban\r\nana", func(tview.Primitive) {})
		testutil.Equal(t, p.Query(), "banana")
		testutil.DeepEqual(t, p.tasks.VisibleTaskIDs(), []string{"b1"})
		p.PasteHandler()("\n", func(tview.Primitive) {})
		testutil.Equal(t, p.Query(), "banana")
	})
}

func needsInputFeed() SwitcherFeed {
	f := switcherFeed()
	f.NeedsInput = []string{"b1"}
	f.HeraModel.Active[1].Roles[0].NeedsInput = true // wkr-date
	return f
}

func TestSwitcherPopup_CtrlG(t *testing.T) {
	t.Run("walks Tasks then Projects and wraps, popup stays open", func(t *testing.T) {
		p := newPopup(t, needsInputFeed(), SwitcherSeed{})
		testutil.Equal(t, selTask(p), "b1") // start on the first (?)
		press(p, tcell.KeyCtrlG, 0)
		testutil.Equal(t, p.ActiveColumn(), SwitcherColumnProjects)
		testutil.Equal(t, selRailName(p), "wkr-date")
		press(p, tcell.KeyCtrlG, 0)
		testutil.Equal(t, p.ActiveColumn(), SwitcherColumnTasks)
		testutil.Equal(t, selTask(p), "b1")
		testutil.False(t, p.Canceled())
		testutil.False(t, p.Selected())
	})

	t.Run("from a non-candidate task row", func(t *testing.T) {
		p := newPopup(t, needsInputFeed(), SwitcherSeed{})
		p.tasks.SelectByID("a1")
		press(p, tcell.KeyCtrlG, 0)
		testutil.Equal(t, selTask(p), "b1")
	})

	t.Run("wraps within Projects when Tasks has none", func(t *testing.T) {
		f := switcherFeed()
		f.HeraModel.Active[0].Roles[1].NeedsInput = true // wkr-cherry
		f.HeraModel.Active[1].Roles[0].NeedsInput = true // wkr-date
		p := newPopup(t, f, SwitcherSeed{})
		testutil.Equal(t, p.ActiveColumn(), SwitcherColumnProjects)
		testutil.Equal(t, selRailName(p), "wkr-cherry")
		press(p, tcell.KeyCtrlG, 0)
		testutil.Equal(t, selRailName(p), "wkr-date")
		press(p, tcell.KeyCtrlG, 0)
		testutil.Equal(t, selRailName(p), "wkr-cherry")
	})

	t.Run("wraps within Tasks when Projects has none", func(t *testing.T) {
		f := switcherFeed()
		f.NeedsInput = []string{"a2", "b1"}
		p := newPopup(t, f, SwitcherSeed{})
		testutil.Equal(t, selTask(p), "a2")
		press(p, tcell.KeyCtrlG, 0)
		testutil.Equal(t, selTask(p), "b1")
		press(p, tcell.KeyCtrlG, 0)
		testutil.Equal(t, selTask(p), "a2")
	})

	t.Run("no-op when nothing needs input", func(t *testing.T) {
		p := newPopup(t, switcherFeed(), SwitcherSeed{})
		press(p, tcell.KeyDown, 0)
		press(p, tcell.KeyCtrlG, 0)
		testutil.Equal(t, selTask(p), "a2")
		testutil.Equal(t, p.ActiveColumn(), SwitcherColumnTasks)
		press(p, tcell.KeyRight, 0)
		before := selRailName(p)
		press(p, tcell.KeyCtrlG, 0)
		testutil.Equal(t, selRailName(p), before)
		testutil.False(t, p.Canceled())
	})
}

func TestSwitcherPopup_StartCursor(t *testing.T) {
	t.Run("first needs-input row, Tasks first", func(t *testing.T) {
		p := newPopup(t, needsInputFeed(), SwitcherSeed{CurrentTaskID: "a2"})
		testutil.Equal(t, p.ActiveColumn(), SwitcherColumnTasks)
		testutil.Equal(t, selTask(p), "b1")
		press(p, tcell.KeyEnter, 0)
		testutil.True(t, p.Selected())
		testutil.Equal(t, p.Selection().TaskID, "b1")
	})

	t.Run("first needs-input row in Projects when no task needs input", func(t *testing.T) {
		f := switcherFeed()
		f.HeraModel.Active[1].Roles[0].NeedsInput = true
		p := newPopup(t, f, SwitcherSeed{CurrentTaskID: "a2"})
		testutil.Equal(t, p.ActiveColumn(), SwitcherColumnProjects)
		testutil.Equal(t, selRailName(p), "wkr-date")
	})

	t.Run("current task in the Tasks column", func(t *testing.T) {
		p := newPopup(t, switcherFeed(), SwitcherSeed{CurrentTaskID: "b1"})
		testutil.Equal(t, p.ActiveColumn(), SwitcherColumnTasks)
		testutil.Equal(t, selTask(p), "b1")
	})

	t.Run("current task in the Projects column", func(t *testing.T) {
		p := newPopup(t, switcherFeed(), SwitcherSeed{CurrentTaskID: "hw"})
		testutil.Equal(t, p.ActiveColumn(), SwitcherColumnProjects)
		testutil.Equal(t, selRailName(p), "wkr-cherry")
	})

	t.Run("top of Tasks otherwise", func(t *testing.T) {
		p := newPopup(t, switcherFeed(), SwitcherSeed{CurrentTaskID: "nope", TasksExpanded: "beta"})
		testutil.Equal(t, p.ActiveColumn(), SwitcherColumnTasks)
		testutil.Equal(t, selTask(p), "b1")
	})

	t.Run("stale expanded seed still lands on a task", func(t *testing.T) {
		p := newPopup(t, switcherFeed(), SwitcherSeed{TasksExpanded: "gone"})
		testutil.Equal(t, selTask(p), "a1")
	})

	t.Run("empty Tasks column starts in Projects", func(t *testing.T) {
		f := switcherFeed()
		f.Tasks = nil
		p := newPopup(t, f, SwitcherSeed{})
		testutil.Equal(t, p.ActiveColumn(), SwitcherColumnProjects)
	})
}

func TestSwitcherPopup_EscAndCtrlQ(t *testing.T) {
	t.Run("Esc clears the filter first, restoring rows, cursors and column", func(t *testing.T) {
		p := newPopup(t, switcherFeed(), SwitcherSeed{})
		press(p, tcell.KeyDown, 0)
		preTasks := p.tasks.VisibleTaskIDs()
		preRailRows, preRailCursor := p.rail.Rows(), p.rail.CursorIndex()
		typeText(p, "cherry")
		testutil.Equal(t, p.ActiveColumn(), SwitcherColumnProjects)
		testutil.Equal(t, len(p.tasks.VisibleTaskIDs()), 0)
		testutil.True(t, p.rail.Rows() < preRailRows)
		press(p, tcell.KeyEscape, 0)
		testutil.Equal(t, p.Query(), "")
		testutil.False(t, p.Canceled())
		testutil.Equal(t, p.ActiveColumn(), SwitcherColumnTasks)
		testutil.Equal(t, selTask(p), "a2")
		testutil.DeepEqual(t, p.tasks.VisibleTaskIDs(), preTasks)
		testutil.Equal(t, p.rail.Rows(), preRailRows)
		testutil.Equal(t, p.rail.CursorIndex(), preRailCursor)
		testutil.Equal(t, p.rail.Filter(), "")
		press(p, tcell.KeyEscape, 0)
		testutil.True(t, p.Canceled())
		testutil.False(t, p.Selected())
	})

	t.Run("ctrl+q closes immediately with a query", func(t *testing.T) {
		p := newPopup(t, switcherFeed(), SwitcherSeed{})
		typeText(p, "app")
		press(p, tcell.KeyCtrlQ, 0)
		testutil.True(t, p.Canceled())
	})

	t.Run("ctrl+j inside the popup is ignored", func(t *testing.T) {
		p := newPopup(t, switcherFeed(), SwitcherSeed{})
		press(p, tcell.KeyCtrlJ, 0)
		testutil.False(t, p.Canceled())
		testutil.False(t, p.Selected())
		testutil.Equal(t, p.Query(), "")
	})
}

func TestSwitcherPopup_Enter(t *testing.T) {
	t.Run("Tasks row", func(t *testing.T) {
		p := newPopup(t, switcherFeed(), SwitcherSeed{})
		press(p, tcell.KeyDown, 0)
		press(p, tcell.KeyEnter, 0)
		testutil.True(t, p.Selected())
		testutil.DeepEqual(t, p.Selection(), SwitcherSelection{Column: SwitcherColumnTasks, TaskID: "a2"})
	})

	t.Run("Projects role row", func(t *testing.T) {
		p := newPopup(t, switcherFeed(), SwitcherSeed{CurrentTaskID: "hw"})
		press(p, tcell.KeyEnter, 0)
		testutil.DeepEqual(t, p.Selection(), SwitcherSelection{Column: SwitcherColumnProjects, TaskID: "hw", RoleID: 12, OrchID: 1})
	})

	t.Run("Projects coordinator header resolves to the coordinator task", func(t *testing.T) {
		p := newPopup(t, switcherFeed(), SwitcherSeed{})
		press(p, tcell.KeyRight, 0)
		testutil.Equal(t, selRailName(p), "orchard")
		press(p, tcell.KeyEnter, 0)
		testutil.DeepEqual(t, p.Selection(), SwitcherSelection{Column: SwitcherColumnProjects, TaskID: "hc", OrchID: 1})
	})

	t.Run("task-less Projects row carries the orchestrator", func(t *testing.T) {
		p := newPopup(t, switcherFeed(), SwitcherSeed{})
		press(p, tcell.KeyRight, 0)
		testutil.True(t, p.rail.SelectOrch(2))
		press(p, tcell.KeyEnter, 0)
		testutil.DeepEqual(t, p.Selection(), SwitcherSelection{Column: SwitcherColumnProjects, OrchID: 2})
	})

	t.Run("fold-only Projects row toggles instead of selecting", func(t *testing.T) {
		f := switcherFeed()
		f.HeraModel.Freelance = []heramodel.RoleView{{RoleID: 91, Name: "free", Kind: db.HeraKindFreelance}}
		p := newPopup(t, f, SwitcherSeed{})
		press(p, tcell.KeyRight, 0)
		for range 10 {
			if p.rail.Selected() == nil && p.rail.SelectedOrch() == nil {
				break
			}
			press(p, tcell.KeyDown, 0)
		}
		testutil.True(t, p.rail.Selected() == nil && p.rail.SelectedOrch() == nil)
		rows := p.rail.Rows()
		press(p, tcell.KeyEnter, 0)
		testutil.False(t, p.Selected())
		testutil.NotEqual(t, p.rail.Rows(), rows)
	})

	t.Run("no selectable row: Enter does nothing", func(t *testing.T) {
		p := newPopup(t, switcherFeed(), SwitcherSeed{})
		typeText(p, "zzz")
		press(p, tcell.KeyEnter, 0)
		testutil.False(t, p.Selected())
	})

	t.Run("selection is a snapshot that survives a refeed", func(t *testing.T) {
		p := newPopup(t, switcherFeed(), SwitcherSeed{})
		press(p, tcell.KeyEnter, 0)
		p.Refeed(SwitcherFeed{})
		testutil.Equal(t, p.Selection().TaskID, "a1")
	})
}

func TestSwitcherPopup_Remote(t *testing.T) {
	f := needsInputFeed()
	f.Remote = true
	p := newPopup(t, f, SwitcherSeed{CurrentTaskID: "hw"})

	t.Run("placeholder drawn", func(t *testing.T) {
		s := drawPopup(t, p, 120, 30)
		testutil.Contains(t, screenText(s, 120, 30), "Projects unavailable in remote mode")
	})
	t.Run("start cursor stays in Tasks", func(t *testing.T) {
		testutil.Equal(t, p.ActiveColumn(), SwitcherColumnTasks)
		testutil.Equal(t, selTask(p), "b1")
	})
	t.Run("Right is skipped", func(t *testing.T) {
		press(p, tcell.KeyRight, 0)
		testutil.Equal(t, p.ActiveColumn(), SwitcherColumnTasks)
	})
	t.Run("ctrl+g skips Projects", func(t *testing.T) {
		press(p, tcell.KeyCtrlG, 0)
		testutil.Equal(t, p.ActiveColumn(), SwitcherColumnTasks)
		testutil.Equal(t, selTask(p), "b1")
	})
	t.Run("no handoff to Projects", func(t *testing.T) {
		typeText(p, "cherry")
		testutil.Equal(t, p.ActiveColumn(), SwitcherColumnTasks)
		testutil.Equal(t, p.rail.Filter(), "")
	})
	t.Run("empty Tasks never lands in Projects", func(t *testing.T) {
		g := f
		g.Tasks = nil
		q := newPopup(t, g, SwitcherSeed{})
		testutil.Equal(t, q.ActiveColumn(), SwitcherColumnTasks)
	})
}

func TestSwitcherPopup_Refeed(t *testing.T) {
	t.Run("preserves cursors by identity and keeps the filter", func(t *testing.T) {
		p := newPopup(t, switcherFeed(), SwitcherSeed{})
		typeText(p, "apple")
		press(p, tcell.KeyDown, 0)
		testutil.Equal(t, selTask(p), "a2")
		f := switcherFeed()
		f.Tasks = append([]*model.Task{{ID: "a0", Name: "apple-zero", Project: "alpha", Status: model.StatusPending}}, f.Tasks...)
		p.Refeed(f)
		testutil.Equal(t, selTask(p), "a2")
		testutil.Contains(t, strings.Join(p.tasks.VisibleTaskIDs(), ","), "a0")
	})

	t.Run("hands off when the active column empties", func(t *testing.T) {
		p := newPopup(t, switcherFeed(), SwitcherSeed{})
		f := switcherFeed()
		f.Tasks = nil
		p.Refeed(f)
		testutil.Equal(t, p.ActiveColumn(), SwitcherColumnProjects)
	})

	t.Run("refeed never mutates the caller's model", func(t *testing.T) {
		p := newPopup(t, switcherFeed(), SwitcherSeed{})
		f := switcherFeed()
		f.HeraModel.Active[0].Roles[1].NeedsInput = true
		p.Refeed(f)
		testutil.False(t, f.HeraModel.Active[0].SubtreeNeedsInput)
	})
}

func TestSwitcherPopup_Mouse(t *testing.T) {
	p := newPopup(t, switcherFeed(), SwitcherSeed{})
	drawPopup(t, p, 120, 30)
	stolen := false
	setFocus := func(tview.Primitive) { stolen = true }
	lx, ly := p.colRects[SwitcherColumnTasks].x, p.colRects[SwitcherColumnTasks].y
	rx, ry := p.colRects[SwitcherColumnProjects].x, p.colRects[SwitcherColumnProjects].y
	h := p.MouseHandler()

	consumed, _ := h(tview.MouseScrollUp, tcell.NewEventMouse(lx+3, ly+3, tcell.WheelUp, 0), setFocus)
	testutil.True(t, consumed)
	testutil.Equal(t, selTask(p), "a2")
	h(tview.MouseScrollDown, tcell.NewEventMouse(lx+3, ly+3, tcell.WheelDown, 0), setFocus)
	testutil.Equal(t, selTask(p), "a1")

	before := selRailName(p)
	h(tview.MouseScrollUp, tcell.NewEventMouse(rx+3, ry+3, tcell.WheelUp, 0), setFocus)
	testutil.NotEqual(t, selRailName(p), before)
	h(tview.MouseScrollDown, tcell.NewEventMouse(rx+3, ry+3, tcell.WheelDown, 0), setFocus)
	testutil.Equal(t, selRailName(p), before)

	consumed, _ = h(tview.MouseLeftDown, tcell.NewEventMouse(lx+3, ly+3, tcell.Button1, 0), setFocus)
	testutil.True(t, consumed)
	consumed, _ = h(tview.MouseLeftClick, tcell.NewEventMouse(0, 0, tcell.Button1, 0), setFocus)
	testutil.True(t, consumed)
	testutil.False(t, stolen)
	testutil.Equal(t, p.ActiveColumn(), SwitcherColumnTasks)
}

// TestSwitcherPopup_RealWidgetsUntouched pins D1: navigating, folding and
// filtering inside the popup never changes the real tabs' cursor or folds.
func TestSwitcherPopup_RealWidgetsUntouched(t *testing.T) {
	realTasks := taskview.NewTaskListView()
	realTasks.SetTasks(switcherTasks())
	realTasks.SelectByID("b1")
	realModel := switcherModel()
	realModel.Active[1].Roles[0].NeedsInput = true // wkr-date
	realRail := hera.NewRail()
	realRail.SetModel(realModel)
	testutil.True(t, realRail.SelectByTaskID("hd"))
	realRail.CursorUp()

	f := switcherFeed()
	f.HeraModel = realRail.Model() // shares backing arrays with the real rail
	realCursor, realRows := realRail.CursorIndex(), realRail.Rows()
	realSel := realTasks.SelectedTask().ID
	realExpanded := realTasks.Expanded()

	p := newPopup(t, f, SwitcherSeed{TasksExpanded: realTasks.Expanded(), Rail: realRail})
	testutil.Equal(t, selRailName(p), "wkr-date") // started on the (?)
	press(p, tcell.KeyUp, 0)
	press(p, tcell.KeyUp, 0)
	press(p, tcell.KeyUp, 0)
	testutil.Equal(t, selRailName(p), "orchard")
	press(p, tcell.KeyRune, ' ')
	testutil.True(t, p.rail.OrchCollapsed(1))
	p.Refeed(f) // sticky reveal stamps the popup's model copy, never the real one
	press(p, tcell.KeyLeft, 0)
	press(p, tcell.KeyDown, 0)
	press(p, tcell.KeyCtrlG, 0)
	typeText(p, "a")
	press(p, tcell.KeyEscape, 0)
	press(p, tcell.KeyEscape, 0)
	testutil.True(t, p.Canceled())

	testutil.Equal(t, realRail.CursorIndex(), realCursor)
	testutil.Equal(t, realRail.Rows(), realRows)
	testutil.False(t, realRail.OrchCollapsed(1))
	testutil.False(t, realRail.OrchCollapsed(2))
	testutil.Equal(t, realRail.Filter(), "")
	testutil.Equal(t, realTasks.SelectedTask().ID, realSel)
	testutil.Equal(t, realTasks.Expanded(), realExpanded)
	testutil.Equal(t, realTasks.Filter(), "")
	m := realRail.Model()
	testutil.False(t, m.Active[0].SubtreeNeedsInput)
	testutil.False(t, m.Active[1].SubtreeNeedsInput)
	testutil.False(t, m.Active[0].Roles[1].SubtreeNeedsInput)
}

func TestSwitcherPopup_SeedsFoldsFromRealRail(t *testing.T) {
	realRail := hera.NewRail()
	realRail.SetModel(switcherModel())
	testutil.True(t, realRail.SelectByTaskID("hc"))
	realRail.ToggleCollapse() // fold orchard on the real rail
	testutil.True(t, realRail.OrchCollapsed(1))

	p := newPopup(t, switcherFeed(), SwitcherSeed{Rail: realRail})
	testutil.True(t, p.rail.OrchCollapsed(1))
	press(p, tcell.KeyRight, 0)
	press(p, tcell.KeyRune, ' ')
	testutil.False(t, p.rail.OrchCollapsed(1))
	testutil.True(t, realRail.OrchCollapsed(1))
}

func TestSwitcherColumn_String(t *testing.T) {
	for _, tc := range []struct {
		c    SwitcherColumn
		want string
	}{{SwitcherColumnTasks, "tasks"}, {SwitcherColumnProjects, "projects"}} {
		t.Run(tc.want, func(t *testing.T) { testutil.Equal(t, tc.c.String(), tc.want) })
	}
}

func TestSwitcherPopup_FilterLineRender(t *testing.T) {
	t.Run("empty query shows the hint", func(t *testing.T) {
		p := newPopup(t, switcherFeed(), SwitcherSeed{})
		testutil.Contains(t, screenText(drawPopup(t, p, 120, 30), 120, 30), "type to filter")
	})
	t.Run("query is drawn with a cursor", func(t *testing.T) {
		p := newPopup(t, switcherFeed(), SwitcherSeed{})
		typeText(p, "ban")
		testutil.Contains(t, screenText(drawPopup(t, p, 120, 30), 120, 30), "› ban█")
	})
	t.Run("long query keeps its tail visible", func(t *testing.T) {
		p := newPopup(t, switcherFeed(), SwitcherSeed{})
		p.PasteHandler()(strings.Repeat("x", 80)+"tail", func(tview.Primitive) {})
		text := screenText(drawPopup(t, p, 50, 20), 50, 20)
		testutil.Contains(t, text, "xtail█")
	})
}

func TestSwitcherPopup_EnterProjectsEmpty(t *testing.T) {
	p := newPopup(t, switcherFeed(), SwitcherSeed{})
	press(p, tcell.KeyRight, 0)
	typeText(p, "zzz") // neither column matches → no handoff
	testutil.Equal(t, p.ActiveColumn(), SwitcherColumnProjects)
	press(p, tcell.KeyEnter, 0)
	testutil.False(t, p.Selected())
}

// kanbanNeedsInputFeed adds two top-level orchestrators in NON-focused kanban
// groups (backlog, done), each with a worker needing input, alongside the
// active-group wkr-date candidate.
func kanbanNeedsInputFeed() SwitcherFeed {
	f := switcherFeed()
	f.HeraModel.Active[1].Roles[0].NeedsInput = true // wkr-date (active)
	f.HeraModel.Active = append(f.HeraModel.Active,
		heramodel.OrchView{ID: 3, Name: "shelved", KanbanStatus: db.HeraKanbanBacklog, Roles: []heramodel.RoleView{
			{RoleID: 31, OrchID: 3, Name: "wkr-elder", Kind: db.HeraKindWorker, Live: true, TaskID: "he", BridgeTaskID: "he", NeedsInput: true},
		}},
		heramodel.OrchView{ID: 4, Name: "finished", KanbanStatus: db.HeraKanbanDone, Roles: []heramodel.RoleView{
			{RoleID: 41, OrchID: 4, Name: "wkr-fig", Kind: db.HeraKindWorker, Live: true, TaskID: "hf", BridgeTaskID: "hf", NeedsInput: true},
		}},
	)
	return f
}

func TestSwitcherPopup_CtrlGReachesNonFocusedKanbanGroups(t *testing.T) {
	t.Run("ctrl+g cycles active -> backlog -> done and wraps", func(t *testing.T) {
		p := newPopup(t, kanbanNeedsInputFeed(), SwitcherSeed{})
		testutil.Equal(t, p.ActiveColumn(), SwitcherColumnProjects)
		testutil.Equal(t, selRailName(p), "wkr-date")
		press(p, tcell.KeyCtrlG, 0)
		testutil.Equal(t, selRailName(p), "wkr-elder")
		testutil.Equal(t, p.rail.FocusedKanban(), db.HeraKanbanBacklog)
		press(p, tcell.KeyCtrlG, 0)
		testutil.Equal(t, selRailName(p), "wkr-fig")
		testutil.Equal(t, p.rail.FocusedKanban(), db.HeraKanbanDone)
		press(p, tcell.KeyCtrlG, 0)
		testutil.Equal(t, selRailName(p), "wkr-date")
		testutil.Equal(t, p.rail.FocusedKanban(), db.HeraKanbanActive)
	})

	t.Run("start cursor reaches a needs-input row in a non-focused group", func(t *testing.T) {
		f := switcherFeed()
		f.HeraModel.Active = append(f.HeraModel.Active,
			heramodel.OrchView{ID: 3, Name: "shelved", KanbanStatus: db.HeraKanbanBlocked, Roles: []heramodel.RoleView{
				{RoleID: 31, OrchID: 3, Name: "wkr-elder", Kind: db.HeraKindWorker, Live: true, TaskID: "he", BridgeTaskID: "he", NeedsInput: true},
			}})
		p := newPopup(t, f, SwitcherSeed{})
		testutil.Equal(t, p.ActiveColumn(), SwitcherColumnProjects)
		testutil.Equal(t, selRailName(p), "wkr-elder")
		testutil.Equal(t, p.rail.FocusedKanban(), db.HeraKanbanBlocked)
	})

	t.Run("Tasks then Projects across groups then back to Tasks", func(t *testing.T) {
		f := kanbanNeedsInputFeed()
		f.NeedsInput = []string{"b1"}
		p := newPopup(t, f, SwitcherSeed{})
		testutil.Equal(t, selTask(p), "b1")
		for _, want := range []string{"wkr-date", "wkr-elder", "wkr-fig"} {
			press(p, tcell.KeyCtrlG, 0)
			testutil.Equal(t, p.ActiveColumn(), SwitcherColumnProjects)
			testutil.Equal(t, selRailName(p), want)
		}
		press(p, tcell.KeyCtrlG, 0)
		testutil.Equal(t, p.ActiveColumn(), SwitcherColumnTasks)
		testutil.Equal(t, selTask(p), "b1")
	})

	t.Run("real rail's own focused group is untouched", func(t *testing.T) {
		real := hera.NewRail()
		real.SetModel(kanbanNeedsInputFeed().HeraModel)
		p := newPopup(t, kanbanNeedsInputFeed(), SwitcherSeed{Rail: real})
		press(p, tcell.KeyCtrlG, 0)
		press(p, tcell.KeyCtrlG, 0)
		testutil.Equal(t, real.FocusedKanban(), db.HeraKanbanActive)
	})
}
