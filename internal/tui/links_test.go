package tui

import (
	"strings"
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

func TestFuzzyLinkPickerModal_Draw(t *testing.T) {
	links := []Link{
		{Label: "GitHub", URL: "https://github.com/foo"},
		{Label: "Docs", URL: "https://docs.example.com"},
	}
	m := NewFuzzyLinkPickerModal(links)
	m.SetRect(0, 0, 80, 24)
	m.Draw(drawSim(t))
}

func TestFuzzyLinkPickerModal_Draw_NoMatchesWithQuery(t *testing.T) {
	links := []Link{{Label: "A", URL: "https://a"}}
	m := NewFuzzyLinkPickerModal(links)
	m.query = []rune("zzz")
	m.qCursor = 3
	m.refilter()
	m.SetRect(0, 0, 80, 24)
	m.Draw(drawSim(t))
}

func TestFuzzyLinkPickerModal_Draw_TinyRect(t *testing.T) {
	links := []Link{{Label: "A", URL: "https://a"}}
	m := NewFuzzyLinkPickerModal(links)
	m.SetRect(0, 0, 0, 0)
	m.Draw(drawSim(t))
}

func TestFuzzyLinkPickerModal_Draw_LongQueryScrolls(t *testing.T) {
	links := []Link{{Label: "A", URL: "https://a"}}
	m := NewFuzzyLinkPickerModal(links)
	m.query = []rune(strings.Repeat("x", 100))
	m.qCursor = len(m.query)
	m.refilter()
	m.SetRect(0, 0, 50, 10)
	m.Draw(drawSim(t))
}

func TestFuzzyLinkPickerModal_PasteHandler(t *testing.T) {
	links := []Link{{Label: "A", URL: "https://a.com"}}
	m := NewFuzzyLinkPickerModal(links)
	paste := m.PasteHandler()
	paste("a", func(p tview.Primitive) {})
	testutil.Equal(t, string(m.query), "a")
}

func TestFuzzyLinkPickerModal_PasteHandler_EmptyNoOp(t *testing.T) {
	m := NewFuzzyLinkPickerModal(nil)
	paste := m.PasteHandler()
	paste("", func(p tview.Primitive) {})
	testutil.Equal(t, len(m.query), 0)
}

func TestLinkPickerModal_Draw(t *testing.T) {
	links := []Link{
		{Label: "A short", URL: "https://a.com"},
		{Label: "Another link", URL: "https://b.com"},
	}
	m := NewLinkPickerModal(links)
	m.SetRect(0, 0, 80, 24)
	m.Draw(drawSim(t))
}

func TestLinkPickerModal_Draw_LongLabelTruncated(t *testing.T) {
	links := []Link{{Label: strings.Repeat("x", 100), URL: "https://a"}}
	m := NewLinkPickerModal(links)
	m.SetRect(0, 0, 30, 10)
	m.Draw(drawSim(t))
}

func TestLinkPickerModal_Draw_TinyRect(t *testing.T) {
	m := NewLinkPickerModal([]Link{{Label: "x", URL: "https://x"}})
	m.SetRect(0, 0, 0, 0)
	m.Draw(drawSim(t))
}

func TestLinkPickerModal_PasteHandler(t *testing.T) {
	m := NewLinkPickerModal(nil)
	paste := m.PasteHandler()
	paste("anything", func(p tview.Primitive) {})
}

func TestLinkPickerModal_SelectedLink_OutOfBounds(t *testing.T) {
	m := NewLinkPickerModal(nil)
	link := m.SelectedLink()
	testutil.Equal(t, link.URL, "")
}
