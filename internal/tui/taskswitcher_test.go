package tui

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func sampleSwitcherEntries() []taskSwitcherEntry {
	return []taskSwitcherEntry{
		{ID: "1", Name: "Blocked on prompt", Project: "argus", Status: model.StatusInProgress, NeedsInput: true},
		{ID: "2", Name: "Fix the resume bug", Project: "argus", Status: model.StatusInProgress},
		{ID: "3", Name: "Review handoff docs", Project: "cortex", Status: model.StatusInReview},
	}
}

func TestTaskSwitcher_RefilterMatchesNameAndProject(t *testing.T) {
	m := NewTaskSwitcherModal(sampleSwitcherEntries())
	testutil.Equal(t, len(m.filtered), 3)

	// Name match.
	m.query = []rune("resume")
	m.qCursor = 6
	m.refilter()
	testutil.Equal(t, len(m.filtered), 1)
	testutil.Equal(t, m.filtered[0].Name, "Fix the resume bug")

	// Project match (two on argus).
	m.query = []rune("argus")
	m.qCursor = 5
	m.refilter()
	testutil.Equal(t, len(m.filtered), 2)

	// Clear → all.
	m.query = nil
	m.qCursor = 0
	m.refilter()
	testutil.Equal(t, len(m.filtered), 3)
}

func TestTaskSwitcher_RefilterPreservesOrder(t *testing.T) {
	// The needs-input entry is first; a fuzzy match that keeps it must keep
	// it at the front of the filtered slice.
	m := NewTaskSwitcherModal(sampleSwitcherEntries())
	m.query = []rune("o") // matches "Blocked on prompt", "Review handoff docs"
	m.qCursor = 1
	m.refilter()
	if len(m.filtered) < 2 {
		t.Fatalf("expected at least 2 matches, got %d", len(m.filtered))
	}
	testutil.Equal(t, m.filtered[0].NeedsInput, true)
}

func TestTaskSwitcher_CursorClampOnFilter(t *testing.T) {
	m := NewTaskSwitcherModal(sampleSwitcherEntries())
	m.cursor = 2
	m.query = []rune("resume")
	m.qCursor = 6
	m.refilter()
	testutil.Equal(t, len(m.filtered), 1)
	testutil.Equal(t, m.cursor, 0)
}

func TestTaskSwitcher_NavigateAndSelect(t *testing.T) {
	m := NewTaskSwitcherModal(sampleSwitcherEntries())
	h := m.InputHandler()

	h(tcell.NewEventKey(tcell.KeyDown, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.cursor, 1)
	h(tcell.NewEventKey(tcell.KeyUp, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.cursor, 0)
	h(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.Selected(), true)
	testutil.Equal(t, m.SelectedTask(), "1")
}

func TestTaskSwitcher_EscapeCancels(t *testing.T) {
	m := NewTaskSwitcherModal(sampleSwitcherEntries())
	h := m.InputHandler()
	h(tcell.NewEventKey(tcell.KeyEscape, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.Canceled(), true)
	testutil.Equal(t, m.Selected(), false)
}

func TestTaskSwitcher_EnterWithNoMatchesIsNoop(t *testing.T) {
	m := NewTaskSwitcherModal(sampleSwitcherEntries())
	h := m.InputHandler()
	for _, r := range "zzzznomatch" {
		h(tcell.NewEventKey(tcell.KeyRune, r, 0), func(tview.Primitive) {})
	}
	testutil.Equal(t, len(m.filtered), 0)
	h(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.Selected(), false)
	testutil.Equal(t, m.SelectedTask(), "")
}

func TestTaskSwitcher_FilterEditing(t *testing.T) {
	m := NewTaskSwitcherModal(sampleSwitcherEntries())
	h := m.InputHandler()
	for _, r := range "resume" {
		h(tcell.NewEventKey(tcell.KeyRune, r, 0), func(tview.Primitive) {})
	}
	testutil.Equal(t, string(m.query), "resume")
	h(tcell.NewEventKey(tcell.KeyBackspace2, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, string(m.query), "resum")
	h(tcell.NewEventKey(tcell.KeyCtrlW, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, string(m.query), "")
}

func TestTaskSwitcher_CursorEditingKeys(t *testing.T) {
	m := NewTaskSwitcherModal(sampleSwitcherEntries())
	h := m.InputHandler()
	for _, r := range "alpha" {
		h(tcell.NewEventKey(tcell.KeyRune, r, 0), func(tview.Primitive) {})
	}
	testutil.Equal(t, m.qCursor, 5)

	// Home / left / right / end move the cursor without mutating the query.
	h(tcell.NewEventKey(tcell.KeyHome, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.qCursor, 0)
	h(tcell.NewEventKey(tcell.KeyRight, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.qCursor, 1)
	h(tcell.NewEventKey(tcell.KeyLeft, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.qCursor, 0)
	h(tcell.NewEventKey(tcell.KeyEnd, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.qCursor, 5)
	testutil.Equal(t, string(m.query), "alpha")

	// Ctrl+A jumps home; Ctrl+U clears from cursor to start.
	h(tcell.NewEventKey(tcell.KeyCtrlA, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.qCursor, 0)
	h(tcell.NewEventKey(tcell.KeyEnd, 0, 0), func(tview.Primitive) {})
	h(tcell.NewEventKey(tcell.KeyCtrlU, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, string(m.query), "")
	testutil.Equal(t, m.qCursor, 0)

	// Down past the end clamps at the last row.
	for range 10 {
		h(tcell.NewEventKey(tcell.KeyDown, 0, 0), func(tview.Primitive) {})
	}
	testutil.Equal(t, m.cursor, len(m.filtered)-1)
}

func TestTaskSwitcher_Paste(t *testing.T) {
	m := NewTaskSwitcherModal(sampleSwitcherEntries())
	paste := m.PasteHandler()
	paste("resume", func(tview.Primitive) {})
	testutil.Equal(t, string(m.query), "resume")
	testutil.Equal(t, len(m.filtered), 1)
}

func TestTaskSwitcherRowText(t *testing.T) {
	e := taskSwitcherEntry{Name: "Do thing", Project: "argus", Status: model.StatusInReview}
	got := taskSwitcherRowText(e)
	testutil.Contains(t, got, "Do thing")
	testutil.Contains(t, got, "argus")
	testutil.Contains(t, got, model.StatusInReview.DisplayName())

	// No project → the meta tail is just the status, with no "project · " prefix.
	noProj := taskSwitcherRowText(taskSwitcherEntry{Name: "Solo", Project: "", Status: model.StatusPending})
	testutil.Equal(t, noProj, "Solo  ·  "+model.StatusPending.DisplayName())
}

func drawSwitcherToString(t *testing.T, m *TaskSwitcherModal, w, h int) string {
	t.Helper()
	m.SetRect(0, 0, w, h)
	screen := tcell.NewSimulationScreen("")
	testutil.NoError(t, screen.Init())
	screen.SetSize(w, h)
	m.Draw(screen)
	var b strings.Builder
	for row := range h {
		for col := range w {
			s, _, _ := screen.Get(col, row)
			b.WriteString(s)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func TestTaskSwitcher_DrawRendersRows(t *testing.T) {
	m := NewTaskSwitcherModal(sampleSwitcherEntries())
	out := drawSwitcherToString(t, m, 100, 24)
	testutil.Contains(t, out, "Switch task")
	testutil.Contains(t, out, "Blocked on prompt")
	testutil.Contains(t, out, "Fix the resume bug")
	testutil.Contains(t, out, "Enter switch")
	// Needs-input marker icon present for the blocked task.
	testutil.Contains(t, out, string(theme.IconNeedsInput))
}

func TestTaskSwitcher_DrawEmptyState(t *testing.T) {
	out := drawSwitcherToString(t, NewTaskSwitcherModal(nil), 80, 20)
	testutil.Contains(t, out, "No other tasks to switch to")
}

func TestTaskSwitcher_DrawNoMatches(t *testing.T) {
	m := NewTaskSwitcherModal(sampleSwitcherEntries())
	m.query = []rune("zzznope")
	m.qCursor = 7
	m.refilter()
	out := drawSwitcherToString(t, m, 80, 20)
	testutil.Contains(t, out, "No matches")
}

// groupedSwitcher returns a grouped switcher over the sample entries (argus: 2
// tasks, cortex: 1), with the cursor parked on the first task.
func groupedSwitcher() *TaskSwitcherModal {
	m := NewTaskSwitcherModal(sampleSwitcherEntries())
	m.SetGrouped(true)
	return m
}

func TestSwitcherGrouped_BuildsFolderRows(t *testing.T) {
	m := groupedSwitcher()
	// argus (expanded, 2 tasks) sorts before cortex (collapsed).
	// rows: [header argus, id1, id2, header cortex]
	testutil.Equal(t, len(m.rows), 4)
	testutil.Equal(t, m.rows[0].kind, switcherRowProjectHeader)
	testutil.Equal(t, m.rows[0].project, "argus")
	testutil.Equal(t, m.rows[0].count, 2)
	testutil.Equal(t, m.rows[1].kind, switcherRowTaskItem)
	testutil.Equal(t, m.rows[1].entry.ID, "1") // needs-input first within project
	testutil.Equal(t, m.rows[2].entry.ID, "2")
	testutil.Equal(t, m.rows[3].kind, switcherRowProjectHeader)
	testutil.Equal(t, m.rows[3].project, "cortex")
	// cortex is collapsed, so its task is not emitted.
	testutil.Equal(t, m.expanded, "argus")
	// Cursor parked on the first task, not the header.
	testutil.Equal(t, m.cursor, 1)
	testutil.Equal(t, m.SelectedTask(), "1")
}

func TestSwitcherGrouped_EmptyProjectFolder(t *testing.T) {
	m := NewTaskSwitcherModal([]taskSwitcherEntry{{ID: "x", Name: "Solo", Project: ""}})
	m.SetGrouped(true)
	testutil.Equal(t, m.rows[0].project, "(no project)")
}

func TestSwitcherGrouped_SearchMultiTermSubstring(t *testing.T) {
	m := groupedSwitcher()
	// Two terms, AND across name+project — exactly like the task list.
	// "argus" matches the project of id1 and id2; "resume" only matches id2's
	// name, so only id2 survives.
	m.query = []rune("argus resume")
	m.qCursor = len(m.query)
	m.refilter()
	testutil.Equal(t, len(m.filtered), 1)
	testutil.Equal(t, m.filtered[0].ID, "2")
	// Filter active → every matching folder is expanded.
	// rows: [header argus, id2]
	testutil.Equal(t, len(m.rows), 2)
	testutil.Equal(t, m.rows[1].entry.ID, "2")
	testutil.Equal(t, m.SelectedTask(), "2")
}

func TestSwitcherGrouped_SearchAcrossProjects(t *testing.T) {
	m := groupedSwitcher()
	// A bare project term expands only that folder's tasks.
	m.query = []rune("cortex")
	m.qCursor = len(m.query)
	m.refilter()
	testutil.Equal(t, len(m.filtered), 1)
	testutil.Equal(t, m.filtered[0].ID, "3")
}

func TestSwitcherGrouped_NavigateDownAcrossFoldersAutoExpands(t *testing.T) {
	m := groupedSwitcher()
	h := m.InputHandler()
	// id1 → id2 (within argus)
	h(tcell.NewEventKey(tcell.KeyDown, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.SelectedTask(), "2")
	// id2 → crossing into cortex auto-expands it (collapsing argus) and lands
	// on cortex's first task.
	h(tcell.NewEventKey(tcell.KeyDown, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.expanded, "cortex")
	testutil.Equal(t, m.SelectedTask(), "3")
}

func TestSwitcherGrouped_NavigateUpAcrossFoldersAutoExpands(t *testing.T) {
	m := groupedSwitcher()
	h := m.InputHandler()
	// Walk down into cortex...
	h(tcell.NewEventKey(tcell.KeyDown, 0, 0), func(tview.Primitive) {})
	h(tcell.NewEventKey(tcell.KeyDown, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.SelectedTask(), "3")
	// ...then back up: re-expands argus and lands on its last task (id2).
	h(tcell.NewEventKey(tcell.KeyUp, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.expanded, "argus")
	testutil.Equal(t, m.SelectedTask(), "2")
}

func TestSwitcherGrouped_DownAtBottomStaysOnLastTask(t *testing.T) {
	m := groupedSwitcher()
	h := m.InputHandler()
	// Walk to the last task (id3 in cortex)...
	h(tcell.NewEventKey(tcell.KeyDown, 0, 0), func(tview.Primitive) {})
	h(tcell.NewEventKey(tcell.KeyDown, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.SelectedTask(), "3")
	// ...pressing Down again clamps and stays on the last task (never strands
	// on a header where Enter would no-op).
	h(tcell.NewEventKey(tcell.KeyDown, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.SelectedTask(), "3")
	testutil.Equal(t, m.rows[m.cursor].kind, switcherRowTaskItem)
}

func TestSwitcherGrouped_SingleProjectNavigation(t *testing.T) {
	m := NewTaskSwitcherModal([]taskSwitcherEntry{
		{ID: "a", Name: "First", Project: "solo"},
		{ID: "b", Name: "Second", Project: "solo"},
	})
	m.SetGrouped(true)
	// rows: [header solo, a, b]; cursor parked on first task.
	testutil.Equal(t, m.SelectedTask(), "a")
	h := m.InputHandler()
	// Up at the top stays on the first task (not the header).
	h(tcell.NewEventKey(tcell.KeyUp, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.SelectedTask(), "a")
	// Down to the last, then Down again clamps on the last task.
	h(tcell.NewEventKey(tcell.KeyDown, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.SelectedTask(), "b")
	h(tcell.NewEventKey(tcell.KeyDown, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.SelectedTask(), "b")
	testutil.Equal(t, m.rows[m.cursor].kind, switcherRowTaskItem)
}

func TestSwitcherGrouped_Paste(t *testing.T) {
	m := groupedSwitcher()
	paste := m.PasteHandler()
	paste("cortex", func(tview.Primitive) {})
	testutil.Equal(t, string(m.query), "cortex")
	testutil.Equal(t, len(m.filtered), 1)
	testutil.Equal(t, m.SelectedTask(), "3")
}

func TestSwitcherGrouped_UpAtTopStaysOnFirstTask(t *testing.T) {
	m := groupedSwitcher()
	h := m.InputHandler()
	h(tcell.NewEventKey(tcell.KeyUp, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.SelectedTask(), "1")
}

func TestSwitcherGrouped_EnterSelectsTask(t *testing.T) {
	m := groupedSwitcher()
	h := m.InputHandler()
	h(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.Selected(), true)
	testutil.Equal(t, m.SelectedTask(), "1")
}

func TestSwitcherGrouped_EnterNoMatchesIsNoop(t *testing.T) {
	m := groupedSwitcher()
	m.query = []rune("zzznope")
	m.qCursor = len(m.query)
	m.refilter()
	testutil.Equal(t, len(m.rows), 0)
	h := m.InputHandler()
	h(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.Selected(), false)
	testutil.Equal(t, m.SelectedTask(), "")
}

func TestSwitcherGrouped_DrawRendersFolders(t *testing.T) {
	out := drawSwitcherToString(t, groupedSwitcher(), 100, 24)
	testutil.Contains(t, out, "Switch task")
	testutil.Contains(t, out, "argus")  // expanded folder header
	testutil.Contains(t, out, "cortex") // collapsed folder header
	testutil.Contains(t, out, "▾")      // expanded chevron
	testutil.Contains(t, out, "▸")      // collapsed chevron
	testutil.Contains(t, out, "Blocked on prompt")
	testutil.Contains(t, out, "Fix the resume bug")
	// cortex collapsed → its task is hidden.
	if strings.Contains(out, "Review handoff docs") {
		t.Fatalf("collapsed cortex folder should hide its task:\n%s", out)
	}
	// Needs-input marker present for the blocked task.
	testutil.Contains(t, out, string(theme.IconNeedsInput))
}

func TestSwitcherGrouped_DrawNoMatches(t *testing.T) {
	m := groupedSwitcher()
	m.query = []rune("zzznope")
	m.qCursor = len(m.query)
	m.refilter()
	out := drawSwitcherToString(t, m, 80, 20)
	testutil.Contains(t, out, "No matches")
}

func TestSwitcherGrouped_DrawVariousSizesNoPanic(t *testing.T) {
	cases := []struct{ w, h int }{{1, 10}, {5, 20}, {8, 20}, {9, 20}, {12, 20}, {30, 5}, {80, 24}, {200, 60}, {0, 0}}
	for _, tc := range cases {
		m := groupedSwitcher()
		m.SetRect(0, 0, tc.w, tc.h)
		screen := tcell.NewSimulationScreen("")
		testutil.NoError(t, screen.Init())
		screen.SetSize(tc.w, tc.h)
		m.Draw(screen) // must not panic
	}
}

func TestTaskSwitcher_DrawVariousSizesNoPanic(t *testing.T) {
	// Widths 1–9 produce a sub-zero filter-field width; the Draw must clamp
	// it rather than panic on the scroll-truncation slice.
	cases := []struct{ w, h int }{{1, 10}, {5, 20}, {8, 20}, {9, 20}, {12, 20}, {30, 5}, {80, 24}, {200, 60}, {0, 0}}
	for _, tc := range cases {
		m := NewTaskSwitcherModal(sampleSwitcherEntries())
		m.SetRect(0, 0, tc.w, tc.h)
		screen := tcell.NewSimulationScreen("")
		testutil.NoError(t, screen.Init())
		screen.SetSize(tc.w, tc.h)
		m.Draw(screen) // must not panic
	}
}
