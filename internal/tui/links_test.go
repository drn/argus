package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/testutil"
)

// Pure ExtractLinks coverage lives in internal/links/links_test.go. This file
// keeps tests for the TUI-specific picker modal and openURL helper.

func TestLinkPickerModal_Navigation(t *testing.T) {
	links := []Link{
		{Label: "First", URL: "https://first.com"},
		{Label: "Second", URL: "https://second.com"},
		{Label: "Third", URL: "https://third.com"},
	}

	t.Run("initial state", func(t *testing.T) {
		m := NewLinkPickerModal(links)
		testutil.Equal(t, m.Selected(), false)
		testutil.Equal(t, m.Canceled(), false)
		testutil.Equal(t, m.SelectedLink().URL, "https://first.com")
	})

	t.Run("down arrow moves cursor", func(t *testing.T) {
		m := NewLinkPickerModal(links)
		handler := m.InputHandler()
		handler(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), func(p tview.Primitive) {})
		testutil.Equal(t, m.SelectedLink().URL, "https://second.com")
	})

	t.Run("j key moves down", func(t *testing.T) {
		m := NewLinkPickerModal(links)
		handler := m.InputHandler()
		handler(tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone), func(p tview.Primitive) {})
		testutil.Equal(t, m.SelectedLink().URL, "https://second.com")
	})

	t.Run("k key moves up", func(t *testing.T) {
		m := NewLinkPickerModal(links)
		handler := m.InputHandler()
		handler(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), func(p tview.Primitive) {})
		handler(tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModNone), func(p tview.Primitive) {})
		testutil.Equal(t, m.SelectedLink().URL, "https://first.com")
	})

	t.Run("up at top stays at top", func(t *testing.T) {
		m := NewLinkPickerModal(links)
		handler := m.InputHandler()
		handler(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone), func(p tview.Primitive) {})
		testutil.Equal(t, m.SelectedLink().URL, "https://first.com")
	})

	t.Run("down at bottom stays at bottom", func(t *testing.T) {
		m := NewLinkPickerModal(links)
		handler := m.InputHandler()
		handler(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), func(p tview.Primitive) {})
		handler(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), func(p tview.Primitive) {})
		handler(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), func(p tview.Primitive) {})
		testutil.Equal(t, m.SelectedLink().URL, "https://third.com")
	})

	t.Run("enter selects", func(t *testing.T) {
		m := NewLinkPickerModal(links)
		handler := m.InputHandler()
		handler(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), func(p tview.Primitive) {})
		handler(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(p tview.Primitive) {})
		testutil.Equal(t, m.Selected(), true)
		testutil.Equal(t, m.SelectedLink().URL, "https://second.com")
	})

	t.Run("escape cancels", func(t *testing.T) {
		m := NewLinkPickerModal(links)
		handler := m.InputHandler()
		handler(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), func(p tview.Primitive) {})
		testutil.Equal(t, m.Canceled(), true)
		testutil.Equal(t, m.Selected(), false)
	})

	t.Run("ctrl+q cancels", func(t *testing.T) {
		m := NewLinkPickerModal(links)
		handler := m.InputHandler()
		handler(tcell.NewEventKey(tcell.KeyCtrlQ, 0, tcell.ModNone), func(p tview.Primitive) {})
		testutil.Equal(t, m.Canceled(), true)
		testutil.Equal(t, m.Selected(), false)
	})
}

func TestOpenURL_RejectsNonHTTP(t *testing.T) {
	// openURL should silently reject non-http schemes.
	// We can't easily test exec.Command didn't fire, but we verify no panic.
	openURL("file:///etc/passwd")
	openURL("javascript:alert(1)")
	openURL("")
	// Valid schemes should not panic either.
	// (We can't stop "open" from actually launching, so just verify no crash.)
}
