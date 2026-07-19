package modal

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/keymap"
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
	// Height must clear the full section list (the modal scrolls when it can't);
	// 140 leaves headroom so every section — including the last ones, Hera plan
	// DAG and Modals & Forms — renders without scrolling.
	sim := drawAt(t, 100, 140)
	m := NewHelpModal()
	m.SetRect(0, 0, 100, 140)
	m.Draw(sim)
	sim.Sync()

	// Guard against silent under-testing: if a future section pushes the content
	// past the window, the body-substring asserts below would pass only because
	// the overflowed rows scrolled off. Require everything to fit (no scroll) so
	// such an overflow fails loudly here instead.
	testutil.Equal(t, m.maxScroll, 0)

	body := screenString(sim)
	testutil.Contains(t, body, "Keybindings")
	testutil.Contains(t, body, "Task List")
	testutil.Contains(t, body, "Projects View (rail)")
	testutil.Contains(t, body, "Agent View")
	// Plan-DAG view keys (Stage 7): the plan diagram nav + fan-out + drill must
	// be discoverable — fail the build if any binding is silently dropped.
	testutil.Contains(t, body, "Projects View (plan DAG)")
	testutil.Contains(t, body, "move between plan stages")
	testutil.Contains(t, body, "fan out / collapse a group (toggle, no open)")
	testutil.Contains(t, body, "fan group; open member/leaf, drill sub-coord")
	testutil.Contains(t, body, "collapse fanned group / drill out to parent plan")
	testutil.Contains(t, body, "File Panel")
	testutil.Contains(t, body, "Settings")
	testutil.Contains(t, body, "[esc / ?] close")
	// Sample a few bindings to catch regressions in the section list.
	testutil.Contains(t, body, "new task")
	testutil.Contains(t, body, "fork task")
	testutil.Contains(t, body, "task switcher")
	testutil.Contains(t, body, "show/hide hera-managed (workers+coords)")
	// Task-list `c` opens the copy menu (name / prompt) — fail the build if the
	// binding text is silently reverted (keybinding-help contract).
	testutil.Contains(t, body, "copy name / prompt")
	// Hera rail Ctrl+Z fullscreen binding (closes the suspend footgun) must be
	// discoverable — fail the build if it's ever silently dropped.
	testutil.Contains(t, body, "fullscreen pane")
	// Hera rail Enter label must advertise reviving a dead/suspended session —
	// fail the build if the revive wording is silently reverted (tasks.md 4.1).
	testutil.Contains(t, body, "revive dead/suspended session")
	// Hera focused-pane ctrl+y copy-staged-clipboard binding must be discoverable
	// — fail the build if it's ever silently dropped.
	testutil.Contains(t, body, "copy staged text (focused pane)")
	// The `J` adopt/reparent rail key must be discoverable in the overlay so a
	// future removal of the binding fails the build (keybinding-help contract).
	testutil.Contains(t, body, "adopt freelancer / reparent coordinator")
	// The `B` force-recycle rail key (add-coordinator-context-management) must be
	// discoverable in the overlay — fail the build if it's ever silently dropped.
	testutil.Contains(t, body, "force recycle coordinator")
	testutil.Contains(t, body, "filter rail by name") // Hera rail `/` filter
	// add-hera-kanban-status: the `m`/`M` kanban-status keys must be
	// discoverable in the overlay — fail the build if silently dropped.
	testutil.Contains(t, body, "advance kanban status (top-level coord)")
	testutil.Contains(t, body, "revert kanban status (top-level coord)")
	// Rail key family (BUG-005/006/010/011/012): every new/changed rail key must
	// be discoverable in the overlay.
	testutil.Contains(t, body, "spawn worker under coordinator (new-task modal)")
	testutil.Contains(t, body, "new coordinator (new-task modal)")
	// BUG-022 two-state EOL: `a` HIDE, `C` clear-archive (nuke), `Ctrl+D` nuke;
	// `R` retire and the rail-wide `Ctrl+R` prune are GONE.
	testutil.Contains(t, body, "hide worker in coord's archive (reversible)")
	testutil.Contains(t, body, "clear coord's archive (nuke hidden agents)")
	testutil.Contains(t, body, "nuke role/orchestrator")
	if strings.Contains(body, "retire worker") {
		t.Errorf("help overlay still lists the removed `R` retire key")
	}
	if strings.Contains(body, "prune all finished coords") {
		t.Errorf("help overlay still lists the removed rail-wide `Ctrl+R` prune")
	}
	// BUG-002: Cmd+Up/Down rail-selection key must be discoverable in the overlay.
	testutil.Contains(t, body, "move rail selection (stay focused in pane)")
	// BUG-016: Left rail parent-nav must be discoverable — fail if dropped.
	testutil.Contains(t, body, "move to parent coordinator (rail focused)")
	// BUG-019: once a pane is focused, Tab/Shift-Tab pass through to the agent PTY
	// (autocomplete) and the focus ladder is Ctrl+Alt+←/→. Both must be
	// discoverable so the no-Tab-ladder semantics aren't silently reverted.
	testutil.Contains(t, body, "agent autocomplete")
	testutil.Contains(t, body, "move between panes")
	// The `/` filter must advertise that ↑/↓ navigate the filtered set while
	// typing — fail the build if that discoverability is dropped (the fix that
	// made the filtered list selectable).
	testutil.Contains(t, body, "↑/↓ navigate")
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
	sim := drawAt(t, 100, 140)
	m := NewHelpModal()
	m.SetRect(0, 0, 100, 140)
	m.Draw(sim)
	sim.Sync()
	body := screenString(sim)
	testutil.Contains(t, body, "[esc / ?] close")
	if strings.Contains(body, "[↑↓ / jk]") {
		t.Errorf("scroll position indicator should not render when all rows fit")
	}
}

func TestNewHelpModalWith_CustomTitleAndSections(t *testing.T) {
	sim := drawAt(t, 100, 40)
	sections := []HelpSection{
		{Title: "Ludwig", Bindings: []HelpBinding{
			{"^F", "next pane"},
			{"r", "refresh"},
		}},
	}
	m := NewHelpModalWith("Ludwig keys", sections)
	m.SetRect(0, 0, 100, 40)
	m.Draw(sim)
	sim.Sync()

	body := screenString(sim)
	// Custom title is used instead of "Keybindings".
	testutil.Contains(t, body, "Ludwig keys")
	// All plugin entries render (bar flag is irrelevant at this layer).
	testutil.Contains(t, body, "next pane")
	testutil.Contains(t, body, "refresh")
	// Argus's own bindings must NOT appear.
	if strings.Contains(body, "fork task") {
		t.Errorf("plugin help overlay must not show argus bindings")
	}
	if strings.Contains(body, "Keybindings") {
		t.Errorf("custom-title modal must not render the default Keybindings title")
	}
}

func TestNewHelpModalWith_DismissOnKey(t *testing.T) {
	m := NewHelpModalWith("X", []HelpSection{{Title: "X", Bindings: []HelpBinding{{"a", "b"}}}})
	ev := tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone)
	m.InputHandler()(ev, nil)
	testutil.True(t, m.Closed())
}

func TestHelpSections_NonEmpty(t *testing.T) {
	sections := SectionsFromKeymap(keymap.DefaultKeymap())
	testutil.True(t, len(sections) > 0)
	for _, sec := range sections {
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
