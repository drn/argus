package tui

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/claudesession"
	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func sampleSessions() []claudesession.Session {
	return []claudesession.Session{
		{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Title: "Implement session switcher", Branch: "argus/switcher", SizeBytes: 303206, PRRef: "drn/argus#700"},
		{ID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", Title: "Fix the resume bug", Branch: "argus/resume", SizeBytes: 12163481},
		{ID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", Title: "Review handoff docs", Branch: "argus/switcher", SizeBytes: 2048},
	}
}

func TestSessionPicker_RefilterMatchesTitleBranchID(t *testing.T) {
	m := NewSessionPickerModal(sampleSessions(), "")
	testutil.Equal(t, len(m.filtered), 3)

	// Title match.
	m.query = []rune("resume")
	m.qCursor = 6
	m.refilter()
	testutil.Equal(t, len(m.filtered), 1)
	testutil.Equal(t, m.filtered[0].Title, "Fix the resume bug")

	// Branch match (two sessions on argus/switcher).
	m.query = []rune("switcher")
	m.qCursor = 8
	m.refilter()
	testutil.Equal(t, len(m.filtered), 2)

	// ID prefix match.
	m.query = []rune("bbbb")
	m.qCursor = 4
	m.refilter()
	testutil.Equal(t, len(m.filtered), 1)
	testutil.Equal(t, m.filtered[0].Branch, "argus/resume")

	// Clear → all.
	m.query = nil
	m.qCursor = 0
	m.refilter()
	testutil.Equal(t, len(m.filtered), 3)
}

func TestSessionPicker_CursorClampOnFilter(t *testing.T) {
	m := NewSessionPickerModal(sampleSessions(), "")
	m.cursor = 2
	m.query = []rune("resume")
	m.qCursor = 6
	m.refilter()
	testutil.Equal(t, len(m.filtered), 1)
	testutil.Equal(t, m.cursor, 0)
}

func TestSessionPicker_NavigateAndSelect(t *testing.T) {
	m := NewSessionPickerModal(sampleSessions(), "")
	h := m.InputHandler()

	h(tcell.NewEventKey(tcell.KeyDown, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.cursor, 1)
	h(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.Selected(), true)
	testutil.Equal(t, m.SelectedSession().Title, "Fix the resume bug")
}

func TestSessionPicker_EscapeCancels(t *testing.T) {
	m := NewSessionPickerModal(sampleSessions(), "")
	h := m.InputHandler()
	h(tcell.NewEventKey(tcell.KeyEscape, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.Canceled(), true)
	testutil.Equal(t, m.Selected(), false)
}

func TestSessionPicker_EnterWithNoMatchesIsNoop(t *testing.T) {
	m := NewSessionPickerModal(sampleSessions(), "")
	h := m.InputHandler()
	for _, r := range "zzzznomatch" {
		h(tcell.NewEventKey(tcell.KeyRune, r, 0), func(tview.Primitive) {})
	}
	testutil.Equal(t, len(m.filtered), 0)
	h(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.Selected(), false)
	testutil.Equal(t, m.SelectedSession().ID, "")
}

func TestSessionPicker_FilterEditing(t *testing.T) {
	m := NewSessionPickerModal(sampleSessions(), "")
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

func TestSessionPicker_Paste(t *testing.T) {
	m := NewSessionPickerModal(sampleSessions(), "")
	paste := m.PasteHandler()
	paste("resume", func(tview.Primitive) {})
	testutil.Equal(t, string(m.query), "resume")
	testutil.Equal(t, len(m.filtered), 1)
}

func TestRowText(t *testing.T) {
	s := claudesession.Session{Title: "Do thing", Branch: "argus/x", SizeBytes: 2048, PRRef: "drn/argus#5"}
	// Zero ModTime renders RelativeTime as "unknown" (deterministic).
	got := rowText(s, false)
	testutil.Contains(t, got, "Do thing")
	testutil.Contains(t, got, "unknown")
	testutil.Contains(t, got, "argus/x")
	testutil.Contains(t, got, "2.0KB")
	testutil.Contains(t, got, "drn/argus#5")
	if strings.HasPrefix(got, "●") {
		t.Fatal("non-current row must not have the current marker")
	}

	cur := rowText(s, true)
	if !strings.HasPrefix(cur, "●") {
		t.Fatalf("current row must start with marker, got %q", cur)
	}
}

func drawToString(t *testing.T, m *SessionPickerModal, w, h int) string {
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

func TestSessionPicker_DrawRendersRows(t *testing.T) {
	m := NewSessionPickerModal(sampleSessions(), "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	out := drawToString(t, m, 100, 24)
	testutil.Contains(t, out, "Switch Claude session")
	testutil.Contains(t, out, "Implement session switcher")
	testutil.Contains(t, out, "Fix the resume bug")
	testutil.Contains(t, out, "Enter switch")
}

func TestSessionPicker_DrawEmptyState(t *testing.T) {
	out := drawToString(t, NewSessionPickerModal(nil, ""), 80, 20)
	testutil.Contains(t, out, "No Claude sessions found")
}

func TestSessionPicker_DrawNoMatches(t *testing.T) {
	m := NewSessionPickerModal(sampleSessions(), "")
	m.query = []rune("zzznope")
	m.qCursor = 7
	m.refilter()
	out := drawToString(t, m, 80, 20)
	testutil.Contains(t, out, "No matches")
}

func TestSessionPicker_DrawVariousSizesNoPanic(t *testing.T) {
	// Widths 1–9 drive the filter-field width negative; Draw must clamp it
	// rather than panic on the scroll-truncation slice.
	cases := []struct{ w, h int }{{1, 10}, {5, 20}, {8, 20}, {9, 20}, {30, 5}, {80, 24}, {200, 60}, {0, 0}}
	for _, tc := range cases {
		m := NewSessionPickerModal(sampleSessions(), "")
		m.SetRect(0, 0, tc.w, tc.h)
		screen := tcell.NewSimulationScreen("")
		testutil.NoError(t, screen.Init())
		screen.SetSize(tc.w, tc.h)
		m.Draw(screen) // must not panic
	}
}
