package taskview

import (
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/gdamore/tcell/v2"
)

// jumpTasks is a fixture spanning two active projects plus one pinned task,
// with needs-input candidates in both active projects so a scan must reach a
// project folded by the one-expanded-project rule.
func jumpTasks() []*model.Task {
	return []*model.Task{
		{ID: "a1", Name: "alpha-one", Project: "alpha", Status: model.StatusInProgress},
		{ID: "a2", Name: "alpha-two", Project: "alpha", Status: model.StatusInProgress},
		{ID: "b1", Name: "beta-one", Project: "beta", Status: model.StatusInProgress},
		{ID: "b2", Name: "beta-two", Project: "beta", Status: model.StatusPending},
		{ID: "p1", Name: "pinned-one", Project: "gamma", Status: model.StatusInProgress, Pinned: true},
	}
}

func TestTaskListView_SetFilter(t *testing.T) {
	t.Run("narrows rows and lands on the first match", func(t *testing.T) {
		tl := NewTaskListView()
		tl.SetTasks(jumpTasks())
		tl.SelectByID("a2")

		tl.SetFilter("beta")

		testutil.Equal(t, tl.Filter(), "beta")
		testutil.DeepEqual(t, tl.VisibleTaskIDs(), []string{"b1", "b2"})
		testutil.Equal(t, tl.SelectedTask().ID, "b1")
		testutil.False(t, tl.Filtering()) // non-interactive: never enters input mode
	})

	t.Run("uses matchesFilter semantics (all terms, project or name)", func(t *testing.T) {
		tl := NewTaskListView()
		tl.SetTasks(jumpTasks())
		tl.SetFilter("alpha two")
		testutil.DeepEqual(t, tl.VisibleTaskIDs(), []string{"a2"})
	})

	t.Run("no match leaves no selectable task", func(t *testing.T) {
		tl := NewTaskListView()
		tl.SetTasks(jumpTasks())
		tl.SetFilter("zzz")
		testutil.Equal(t, len(tl.VisibleTaskIDs()), 0)
		testutil.Nil(t, tl.SelectedTask())
	})

	t.Run("empty query restores the pre-filter cursor", func(t *testing.T) {
		tl := NewTaskListView()
		tl.SetTasks(jumpTasks())
		tl.SelectByID("a2")

		tl.SetFilter("b")
		tl.SetFilter("be")
		testutil.Equal(t, tl.SelectedTask().ID, "b1")
		tl.SetFilter("")

		testutil.Equal(t, tl.Filter(), "")
		testutil.Equal(t, tl.SelectedTask().ID, "a2")
	})

	t.Run("empty query falls back to the current selection when the pre-filter task vanished", func(t *testing.T) {
		tl := NewTaskListView()
		tl.SetTasks(jumpTasks())
		tl.SelectByID("a2")
		tl.SetFilter("beta")
		// The pre-filter task disappears while filtered.
		var kept []*model.Task
		for _, task := range jumpTasks() {
			if task.ID != "a2" {
				kept = append(kept, task)
			}
		}
		tl.SetTasks(kept)
		tl.CursorDown() // b2
		tl.SetFilter("")
		testutil.Equal(t, tl.SelectedTask().ID, "b2")
	})

	t.Run("same query is a no-op", func(t *testing.T) {
		tl := NewTaskListView()
		tl.SetTasks(jumpTasks())
		tl.SetFilter("beta")
		tl.CursorDown()
		tl.SetFilter("beta")
		testutil.Equal(t, tl.SelectedTask().ID, "b2")
	})
}

func TestTaskListView_NeedsInputScan(t *testing.T) {
	newList := func() *TaskListView {
		tl := NewTaskListView()
		tl.SetTasks(jumpTasks())
		tl.SetNeedsInput([]string{"a2", "b1", "p1", "b2"}) // b2 is pending → no (?) glyph
		return tl
	}

	t.Run("NeedsInputTaskIDs lists candidates in full scan order", func(t *testing.T) {
		tl := newList()
		// Pinned first, then active projects alphabetically, folded or not.
		testutil.DeepEqual(t, tl.NeedsInputTaskIDs(), []string{"p1", "a2", "b1"})
	})

	t.Run("NeedsInputTaskIDsAfter does not wrap", func(t *testing.T) {
		tl := newList()
		testutil.DeepEqual(t, tl.NeedsInputTaskIDsAfter(""), []string{"p1", "a2", "b1"})
		testutil.DeepEqual(t, tl.NeedsInputTaskIDsAfter("a1"), []string{"a2", "b1"})
		testutil.DeepEqual(t, tl.NeedsInputTaskIDsAfter("a2"), []string{"b1"})
		testutil.Equal(t, len(tl.NeedsInputTaskIDsAfter("b1")), 0)
		testutil.DeepEqual(t, tl.NeedsInputTaskIDsAfter("missing"), []string{"p1", "a2", "b1"})
	})

	t.Run("NextNeedsInputTaskID wraps", func(t *testing.T) {
		tl := newList()
		for _, tc := range []struct{ after, want string }{
			{"", "p1"},
			{"p1", "a2"},
			{"a1", "a2"},
			{"a2", "b1"},
			{"b1", "p1"}, // wrap
			{"b2", "p1"}, // past the last candidate → wrap
		} {
			t.Run(tc.after+"->"+tc.want, func(t *testing.T) {
				got, ok := tl.NextNeedsInputTaskID(tc.after)
				testutil.True(t, ok)
				testutil.Equal(t, got, tc.want)
			})
		}
	})

	t.Run("single candidate re-selects itself", func(t *testing.T) {
		tl := NewTaskListView()
		tl.SetTasks(jumpTasks())
		tl.SetNeedsInput([]string{"b1"})
		got, ok := tl.NextNeedsInputTaskID("b1")
		testutil.True(t, ok)
		testutil.Equal(t, got, "b1")
	})

	t.Run("no candidate", func(t *testing.T) {
		tl := NewTaskListView()
		tl.SetTasks(jumpTasks())
		got, ok := tl.NextNeedsInputTaskID("")
		testutil.False(t, ok)
		testutil.Equal(t, got, "")
	})

	t.Run("selecting the hit expands its folded project", func(t *testing.T) {
		tl := newList()
		tl.SelectByID("a1")
		id, ok := tl.NextNeedsInputTaskID("a2")
		testutil.True(t, ok)
		testutil.Equal(t, id, "b1")
		tl.SelectByID(id)
		testutil.Equal(t, tl.SelectedTask().ID, "b1")
		testutil.Equal(t, tl.Expanded(), "beta")
	})

	t.Run("respects filter and hidden hera-managed tasks", func(t *testing.T) {
		tl := newList()
		tl.SetManagedTasks(map[string]bool{"p1": true})
		tl.SetHideHeraManaged(true)
		tl.SetFilter("beta")
		testutil.DeepEqual(t, tl.NeedsInputTaskIDs(), []string{"b1"})
	})

	t.Run("archived candidates scan last", func(t *testing.T) {
		tasks := append(jumpTasks(), &model.Task{ID: "z1", Name: "old", Project: "aaa", Status: model.StatusInProgress, Archived: true})
		tl := NewTaskListView()
		tl.SetTasks(tasks)
		tl.SetNeedsInput([]string{"z1", "a2"})
		testutil.DeepEqual(t, tl.NeedsInputTaskIDs(), []string{"a2", "z1"})
	})
}

func TestTaskListView_Expanded(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks(jumpTasks())
	testutil.Equal(t, tl.Expanded(), "alpha") // auto-expanded first project
	tl.SetExpanded("beta")
	testutil.Equal(t, tl.Expanded(), "beta")
}

func TestTaskListView_FocusAndCursorDim(t *testing.T) {
	draw := func(tl *TaskListView) tcell.SimulationScreen {
		t.Helper()
		screen := tcell.NewSimulationScreen("UTF-8")
		testutil.NoError(t, screen.Init())
		screen.SetSize(40, 10)
		tl.SetRect(0, 0, 40, 10)
		tl.Draw(screen)
		return screen
	}
	cursorRow := 2 // border(0) + project header(1) + first task(2)

	t.Run("default draws the unfocused border and a selected cursor", func(t *testing.T) {
		tl := NewTaskListView()
		tl.SetTasks(jumpTasks()[:2])
		s := draw(tl)
		_, _, border, _ := s.GetContent(0, 0)
		testutil.Equal(t, border, theme.StyleBorder)
		_, _, name, _ := s.GetContent(8, cursorRow)
		testutil.Equal(t, name, theme.StyleSelected)
	})

	t.Run("SetFocused highlights the border", func(t *testing.T) {
		tl := NewTaskListView()
		tl.SetTasks(jumpTasks()[:2])
		tl.SetFocused(true)
		s := draw(tl)
		_, _, border, _ := s.GetContent(0, 0)
		testutil.Equal(t, border, theme.StyleFocusedBorder)
	})

	t.Run("SetCursorDimmed draws a marker instead of the selected style", func(t *testing.T) {
		tl := NewTaskListView()
		tl.SetTasks(jumpTasks()[:2])
		tl.SetCursorDimmed(true)
		s := draw(tl)
		_, _, name, _ := s.GetContent(8, cursorRow)
		testutil.Equal(t, name, theme.StyleNormal)
		mainc, _, markStyle, _ := s.GetContent(2, cursorRow)
		testutil.Equal(t, mainc, '›')
		testutil.Equal(t, markStyle, theme.StyleDimmed)
	})
}
