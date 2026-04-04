package tui2

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestFuzzyMatch(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		str     string
		want    bool
	}{
		{"exact", "hello", "hello", true},
		{"prefix", "hel", "hello", true},
		{"subsequence", "hlo", "hello", true},
		{"case insensitive", "HLO", "hello", true},
		{"no match", "xyz", "hello", false},
		{"empty pattern", "", "hello", true},
		{"empty str", "a", "", false},
		{"both empty", "", "", true},
		{"url match", "git", "https://github.com/foo/bar", true},
		{"url partial", "ghub", "https://github.com/foo/bar", true},
		{"out of order", "ba", "abc", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fuzzyMatch(tt.pattern, tt.str)
			testutil.Equal(t, got, tt.want)
		})
	}
}

func TestFuzzyLinkPickerModal_Refilter(t *testing.T) {
	links := []Link{
		{Label: "GitHub PR", URL: "https://github.com/foo/bar/pull/1"},
		{Label: "Docs", URL: "https://docs.example.com"},
		{Label: "Jira", URL: "https://jira.example.com/browse/FOO-123"},
	}
	m := NewFuzzyLinkPickerModal(links)

	testutil.Equal(t, len(m.filtered), 3)

	// Type "git" — should match GitHub PR
	for _, r := range "git" {
		m.query = append(m.query, r)
		m.qCursor++
	}
	m.refilter()
	testutil.Equal(t, len(m.filtered), 1)
	testutil.Equal(t, m.filtered[0].URL, "https://github.com/foo/bar/pull/1")

	// Clear query — should show all
	m.query = nil
	m.qCursor = 0
	m.refilter()
	testutil.Equal(t, len(m.filtered), 3)

	// Type "example" — matches docs and jira
	for _, r := range "example" {
		m.query = append(m.query, r)
		m.qCursor++
	}
	m.refilter()
	testutil.Equal(t, len(m.filtered), 2)
}

func TestFuzzyLinkPickerModal_CursorClamp(t *testing.T) {
	links := []Link{
		{Label: "A", URL: "https://a.com"},
		{Label: "B", URL: "https://b.com"},
		{Label: "C", URL: "https://c.com"},
	}
	m := NewFuzzyLinkPickerModal(links)
	m.cursor = 2 // on "C"

	// Filter to just "A" — cursor should clamp to 0
	m.query = []rune("a.com")
	m.qCursor = 5
	m.refilter()
	testutil.Equal(t, len(m.filtered), 1)
	testutil.Equal(t, m.cursor, 0)
}

func TestFuzzyLinkPickerModal_KeyNavigation(t *testing.T) {
	links := []Link{
		{Label: "A", URL: "https://a.com"},
		{Label: "B", URL: "https://b.com"},
	}
	m := NewFuzzyLinkPickerModal(links)
	handler := m.InputHandler()

	// Down arrow
	handler(tcell.NewEventKey(tcell.KeyDown, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.cursor, 1)

	// Down at bottom — stays
	handler(tcell.NewEventKey(tcell.KeyDown, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.cursor, 1)

	// Up arrow
	handler(tcell.NewEventKey(tcell.KeyUp, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.cursor, 0)

	// Escape cancels
	handler(tcell.NewEventKey(tcell.KeyEscape, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.Canceled(), true)
}

func TestFuzzyLinkPickerModal_TypeAndSelect(t *testing.T) {
	links := []Link{
		{Label: "GitHub", URL: "https://github.com/foo"},
		{Label: "Google", URL: "https://google.com"},
	}
	m := NewFuzzyLinkPickerModal(links)
	handler := m.InputHandler()

	// Type "gle" — filters to Google
	for _, r := range "gle" {
		handler(tcell.NewEventKey(tcell.KeyRune, r, 0), func(tview.Primitive) {})
	}
	testutil.Equal(t, len(m.filtered), 1)
	testutil.Equal(t, m.filtered[0].Label, "Google")

	// Enter selects
	handler(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.Selected(), true)
	testutil.Equal(t, m.SelectedLink().URL, "https://google.com")
}

func TestFuzzyLinkPickerModal_BackspaceAndWordDelete(t *testing.T) {
	links := []Link{
		{Label: "A", URL: "https://a.com"},
	}
	m := NewFuzzyLinkPickerModal(links)
	handler := m.InputHandler()

	// Type "hello"
	for _, r := range "hello" {
		handler(tcell.NewEventKey(tcell.KeyRune, r, 0), func(tview.Primitive) {})
	}
	testutil.Equal(t, string(m.query), "hello")

	// Backspace
	handler(tcell.NewEventKey(tcell.KeyBackspace2, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, string(m.query), "hell")

	// Ctrl+W word delete
	handler(tcell.NewEventKey(tcell.KeyCtrlW, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, string(m.query), "")
}

func TestFuzzyLinkPickerModal_EnterNoMatches(t *testing.T) {
	links := []Link{
		{Label: "A", URL: "https://a.com"},
	}
	m := NewFuzzyLinkPickerModal(links)
	handler := m.InputHandler()

	// Type something that matches nothing
	for _, r := range "zzzzz" {
		handler(tcell.NewEventKey(tcell.KeyRune, r, 0), func(tview.Primitive) {})
	}
	testutil.Equal(t, len(m.filtered), 0)

	// Enter should NOT select when no matches
	handler(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, m.Selected(), false)
}

func TestFuzzyLinkPickerModal_MatchesLabel(t *testing.T) {
	links := []Link{
		{Label: "My Docs Page", URL: "https://example.com/docs"},
		{Label: "Other", URL: "https://other.com"},
	}
	m := NewFuzzyLinkPickerModal(links)

	// Search by label text
	m.query = []rune("docs page")
	m.qCursor = len(m.query)
	m.refilter()
	testutil.Equal(t, len(m.filtered), 1)
	testutil.Equal(t, m.filtered[0].Label, "My Docs Page")
}
