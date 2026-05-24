package modal

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/testutil"
)

func TestHelpModal_Defaults(t *testing.T) {
	m := NewHelpModal()
	testutil.False(t, m.Closed())
}

func TestHelpModal_InputHandler(t *testing.T) {
	for _, tc := range []struct {
		name       string
		key        tcell.Key
		rune       rune
		wantClosed bool
	}{
		{"esc closes", tcell.KeyEscape, 0, true},
		{"ctrl+q closes", tcell.KeyCtrlQ, 0, true},
		{"? closes", tcell.KeyRune, '?', true},
		{"unrelated rune no-op", tcell.KeyRune, 'x', false},
		{"enter no-op", tcell.KeyEnter, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewHelpModal()
			ev := tcell.NewEventKey(tc.key, tc.rune, tcell.ModNone)
			m.InputHandler()(ev, nil)
			testutil.Equal(t, m.Closed(), tc.wantClosed)
		})
	}
}

func TestHelpModal_Draw(t *testing.T) {
	sim := drawAt(t, 100, 40)
	m := NewHelpModal()
	m.SetRect(0, 0, 100, 40)
	m.Draw(sim)
	sim.Sync()

	body := screenString(sim)
	testutil.Contains(t, body, "Keybindings")
	testutil.Contains(t, body, "Task List")
	testutil.Contains(t, body, "Agent View")
	testutil.Contains(t, body, "File Panel")
	testutil.Contains(t, body, "Settings")
	testutil.Contains(t, body, "[esc / ?] close")
	// Sample a few bindings to catch regressions in the section list.
	testutil.Contains(t, body, "new task")
	testutil.Contains(t, body, "fork task")
}

func TestHelpModal_DrawZeroSizeNoOp(t *testing.T) {
	sim := drawAt(t, 80, 24)
	m := NewHelpModal()
	m.SetRect(0, 0, 0, 0)
	m.Draw(sim) // must not panic
}

func TestHelpModal_DrawTinyArea(t *testing.T) {
	sim := drawAt(t, 80, 24)
	m := NewHelpModal()
	m.SetRect(0, 0, 6, 2) // below the 8x4 minimum — should short-circuit
	m.Draw(sim)
}

func TestHelpModal_DrawClampsToAvailableHeight(t *testing.T) {
	sim := drawAt(t, 80, 24)
	m := NewHelpModal()
	m.SetRect(0, 0, 80, 8) // height clamped; only the first sections fit
	m.Draw(sim)
	sim.Sync()
	body := screenString(sim)
	testutil.Contains(t, body, "Keybindings")
	// Hint must still render on the last inner row.
	testutil.Contains(t, body, "close")
}

func TestHelpModal_ScrollKeys(t *testing.T) {
	// Draw at a height that forces scrolling: total content is len(helpRows())
	// rows, give the modal much less.
	render := func(m *HelpModal) {
		sim := drawAt(t, 80, 12)
		m.SetRect(0, 0, 80, 12)
		m.Draw(sim)
	}

	for _, tc := range []struct {
		name string
		fire func(m *HelpModal)
		// after firing + redraw, scroll must satisfy this predicate.
		check func(t *testing.T, m *HelpModal)
	}{
		{
			"down arrow scrolls one row",
			func(m *HelpModal) {
				m.InputHandler()(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), nil)
			},
			func(t *testing.T, m *HelpModal) { testutil.Equal(t, m.scroll, 1) },
		},
		{
			"j scrolls one row",
			func(m *HelpModal) {
				m.InputHandler()(tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone), nil)
			},
			func(t *testing.T, m *HelpModal) { testutil.Equal(t, m.scroll, 1) },
		},
		{
			"k at top clamps to 0",
			func(m *HelpModal) {
				m.InputHandler()(tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModNone), nil)
			},
			func(t *testing.T, m *HelpModal) { testutil.Equal(t, m.scroll, 0) },
		},
		{
			"PgDn scrolls one page",
			func(m *HelpModal) {
				m.InputHandler()(tcell.NewEventKey(tcell.KeyPgDn, 0, tcell.ModNone), nil)
			},
			func(t *testing.T, m *HelpModal) { testutil.True(t, m.scroll == m.pageStep) },
		},
		{
			"G jumps to bottom",
			func(m *HelpModal) {
				m.InputHandler()(tcell.NewEventKey(tcell.KeyRune, 'G', tcell.ModNone), nil)
			},
			func(t *testing.T, m *HelpModal) { testutil.Equal(t, m.scroll, m.maxScroll) },
		},
		{
			"End jumps to bottom",
			func(m *HelpModal) {
				m.InputHandler()(tcell.NewEventKey(tcell.KeyEnd, 0, tcell.ModNone), nil)
			},
			func(t *testing.T, m *HelpModal) { testutil.Equal(t, m.scroll, m.maxScroll) },
		},
		{
			"g returns to top after scrolling",
			func(m *HelpModal) {
				m.scroll = 5
				m.InputHandler()(tcell.NewEventKey(tcell.KeyRune, 'g', tcell.ModNone), nil)
			},
			func(t *testing.T, m *HelpModal) { testutil.Equal(t, m.scroll, 0) },
		},
		{
			"Home returns to top after scrolling",
			func(m *HelpModal) {
				m.scroll = 5
				m.InputHandler()(tcell.NewEventKey(tcell.KeyHome, 0, tcell.ModNone), nil)
			},
			func(t *testing.T, m *HelpModal) { testutil.Equal(t, m.scroll, 0) },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewHelpModal()
			render(m) // populate maxScroll/pageStep
			tc.fire(m)
			render(m) // re-clamp scroll
			tc.check(t, m)
			testutil.False(t, m.Closed())
		})
	}
}

func TestHelpModal_MouseWheelScrolls(t *testing.T) {
	m := NewHelpModal()
	m.SetRect(0, 0, 80, 12)
	sim := drawAt(t, 80, 12)
	m.Draw(sim) // initialize maxScroll/pageStep

	handler := m.MouseHandler()
	// Wheel down 3 lines.
	consumed, _ := handler(tview.MouseScrollDown, tcell.NewEventMouse(0, 0, tcell.ButtonNone, tcell.ModNone), nil)
	testutil.True(t, consumed)
	m.Draw(sim)
	testutil.Equal(t, m.scroll, 3)

	// Wheel up 3 lines — back to top.
	consumed, _ = handler(tview.MouseScrollUp, tcell.NewEventMouse(0, 0, tcell.ButtonNone, tcell.ModNone), nil)
	testutil.True(t, consumed)
	m.Draw(sim)
	testutil.Equal(t, m.scroll, 0)
}

func TestHelpModal_DrawShowsScrollPositionWhenOverflow(t *testing.T) {
	sim := drawAt(t, 80, 12) // forces clamp/overflow
	m := NewHelpModal()
	m.SetRect(0, 0, 80, 12)
	m.Draw(sim)
	sim.Sync()
	body := screenString(sim)
	// The hint marker is unique to the scroll-position footer.
	testutil.Contains(t, body, "[↑↓ / jk]")
}

func TestHelpModal_DrawHidesScrollHintWhenFits(t *testing.T) {
	// Give the modal enough room that every row fits.
	sim := drawAt(t, 100, 80)
	m := NewHelpModal()
	m.SetRect(0, 0, 100, 80)
	m.Draw(sim)
	sim.Sync()
	body := screenString(sim)
	testutil.Contains(t, body, "[esc / ?] close")
	if strings.Contains(body, "[↑↓ / jk]") {
		t.Errorf("scroll position indicator should not render when all rows fit")
	}
}

func TestHelpSections_NonEmpty(t *testing.T) {
	testutil.True(t, len(HelpSections) > 0)
	for _, sec := range HelpSections {
		t.Run(sec.Title, func(t *testing.T) {
			testutil.True(t, sec.Title != "")
			testutil.True(t, len(sec.Bindings) > 0)
			for _, b := range sec.Bindings {
				testutil.True(t, b.Key != "")
				testutil.True(t, b.Action != "")
			}
		})
	}
}
