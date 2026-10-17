package tui

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
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
	// Status is conveyed by the leading icon now, so the row text is the task
	// name followed by its project — never the status display name.
	e := taskSwitcherEntry{Name: "Do thing", Project: "argus", Status: model.StatusInReview}
	got := taskSwitcherRowText(e)
	testutil.Contains(t, got, "Do thing")
	testutil.Contains(t, got, "argus")
	if strings.Contains(got, model.StatusInReview.DisplayName()) {
		t.Errorf("row text should not spell out the status, got %q", got)
	}

	// No project → just the name, with no "  ·  project" tail.
	noProj := taskSwitcherRowText(taskSwitcherEntry{Name: "Solo", Project: "", Status: model.StatusPending})
	testutil.Equal(t, noProj, "Solo")
}

// TestTaskSwitcherEntry_StatusIcon pins the switcher's entry→glyph wiring (the
// entry flags flow through statusIcon to the shared
// classifier). Covers the spinner (actively-running) branch specifically, which
// the draw tests can't assert deterministically because the frame is time-based.
func TestTaskSwitcherEntry_StatusIcon(t *testing.T) {
	spinnerFrames := make(map[rune]bool, widget.SpinnerFrameCount())
	for i := range widget.SpinnerFrameCount() {
		spinnerFrames[widget.SpinnerFrame(i)] = true
	}
	// Actively-running in_progress → some spinner frame, in-progress style.
	glyph, style := taskSwitcherEntry{Status: model.StatusInProgress, Running: true}.statusIcon()
	if !spinnerFrames[glyph] {
		t.Errorf("running in_progress entry: glyph %q is not a spinner frame", glyph)
	}
	testutil.Equal(t, style, theme.StyleInProgress)

	// Idle in_progress (session present but quiet) → moon outline, not a spinner.
	idleGlyph, idleStyle := taskSwitcherEntry{Status: model.StatusInProgress, Running: true, Idle: true}.statusIcon()
	testutil.Equal(t, idleGlyph, theme.IconMoonOutline)
	testutil.Equal(t, idleStyle, theme.StyleInReview)

	// In-review → clipboard glyph.
	revGlyph, _ := taskSwitcherEntry{Status: model.StatusInReview}.statusIcon()
	testutil.Equal(t, revGlyph, theme.IconReview)
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

// TestTaskSwitcher_DrawShowsStatusIconNotName: the switcher renders the task
// list's status glyph to the left of each name instead of spelling out the
// status, so the in_review task shows IconReview and no "In Review"/"In Progress"
// text appears anywhere.
func TestTaskSwitcher_DrawShowsStatusIconNotName(t *testing.T) {
	m := NewTaskSwitcherModal(sampleSwitcherEntries())
	out := drawSwitcherToString(t, m, 100, 24)
	// in_review task's clipboard icon and the blocked task's needs-input icon.
	testutil.Contains(t, out, string(theme.IconReview))
	testutil.Contains(t, out, string(theme.IconNeedsInput))
	// Status display names are no longer spelled out.
	for _, name := range []string{
		model.StatusInReview.DisplayName(),
		model.StatusInProgress.DisplayName(),
	} {
		if strings.Contains(out, name) {
			t.Errorf("switcher should not render status name %q; got:\n%s", name, out)
		}
	}
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

// Meta (Alt/Option) word-motion keys on the task switcher query.
func TestTaskSwitcher_AltWordMotion(t *testing.T) {
	m := NewTaskSwitcherModal(sampleSwitcherEntries())
	h := m.InputHandler()
	m.query = []rune("foo bar")
	m.qCursor = 7

	h(tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModAlt), func(tview.Primitive) {})
	testutil.Equal(t, m.qCursor, 4)

	m.qCursor = 7
	h(tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModAlt), func(tview.Primitive) {})
	testutil.Equal(t, string(m.query), "foo ")

	m.query = []rune("foo bar")
	m.qCursor = 0
	h(tcell.NewEventKey(tcell.KeyDelete, 0, tcell.ModAlt), func(tview.Primitive) {})
	testutil.Equal(t, string(m.query), " bar")
}

func TestTaskSwitcher_SetTitles(t *testing.T) {
	m := NewTaskSwitcherModal(sampleSwitcherEntries())
	m.SetTitles(" Pick parent ", "")
	testutil.Equal(t, m.title, " Pick parent ")
	testutil.Equal(t, m.help, "↑/↓ select  Enter switch  Esc cancel") // "" keeps current
	m.SetTitles("", "Enter pick")
	testutil.Equal(t, m.title, " Pick parent ")
	testutil.Equal(t, m.help, "Enter pick")
	testutil.Contains(t, drawSwitcherToString(t, m, 100, 24), "Pick parent")
}
