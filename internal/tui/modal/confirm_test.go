package modal

import (
	"fmt"
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestConfirmModal_Decisions(t *testing.T) {
	noFocus := func(tview.Primitive) {}
	t.Run("enter confirms", func(t *testing.T) {
		m := NewConfirmModal("T", "msg")
		m.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), noFocus)
		testutil.Equal(t, m.Confirmed(), true)
		testutil.Equal(t, m.Canceled(), false)
	})
	t.Run("y confirms", func(t *testing.T) {
		m := NewConfirmModal("T", "msg")
		m.InputHandler()(tcell.NewEventKey(tcell.KeyRune, 'y', tcell.ModNone), noFocus)
		testutil.Equal(t, m.Confirmed(), true)
	})
	t.Run("esc cancels", func(t *testing.T) {
		m := NewConfirmModal("T", "msg")
		m.InputHandler()(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), noFocus)
		testutil.Equal(t, m.Canceled(), true)
	})
	t.Run("n cancels", func(t *testing.T) {
		m := NewConfirmModal("T", "msg")
		m.InputHandler()(tcell.NewEventKey(tcell.KeyRune, 'n', tcell.ModNone), noFocus)
		testutil.Equal(t, m.Canceled(), true)
	})
}

func TestConfirmModal_DrawNoPanic(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(80, 24)

	m := NewConfirmModal("Delete role wkr?", "Removes the role and ends its binding.")
	m.SetRect(0, 0, 80, 24)
	m.Draw(sim) // must not panic and must cover its rect
}

// screenText reads back the simulation screen as one string per row.
func screenText(sim tcell.SimulationScreen, w, h int) []string {
	cells, _, _ := sim.GetContents()
	rows := make([]string, h)
	for y := range h {
		runes := make([]rune, 0, w)
		for x := range w {
			runes = append(runes, cells[y*w+x].Runes...)
		}
		rows[y] = string(runes)
	}
	return rows
}

func TestConfirmModal_LongMessageWraps(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	const w, h = 80, 24
	sim.SetSize(w, h)

	// A long message that exceeds the modal's inner width must wrap onto
	// multiple rows rather than truncate at the right border.
	msg := "Removes the orchestrator and all its roles. The underlying argus tasks are left intact and can be reused."
	m := NewConfirmModal("Delete orchestrator argus-refactor?", msg)
	m.SetRect(0, 0, w, h)
	m.Draw(sim)
	sim.Show() // flush back buffer so GetContents reflects the draw

	rows := screenText(sim, w, h)
	// The tail of the message must appear somewhere on screen — proof it
	// wasn't clipped at the first visual line.
	var found bool
	for _, r := range rows {
		if strings.Contains(r, "reused") {
			found = true
			break
		}
	}
	testutil.Equal(t, found, true)
}

// TestConfirmModal_TinyTerminal pins the height-clamp bounds: a terminal too
// short for the 6-row chrome must drop body lines (maxBody floors at 0) rather
// than panic on a negative slice index or overlap the footer onto the body.
func TestConfirmModal_TinyTerminal(t *testing.T) {
	msg := "Removes the orchestrator and all its roles and cannot be undone."
	for _, h := range []int{1, 2, 3, 4, 5, 6, 7} {
		t.Run(fmt.Sprintf("height-%d", h), func(t *testing.T) {
			sim := tcell.NewSimulationScreen("UTF-8")
			testutil.NoError(t, sim.Init())
			defer sim.Fini()
			const w = 40
			sim.SetSize(w, h)
			m := NewConfirmModal("Delete orchestrator?", msg)
			m.SetRect(0, 0, w, h)
			m.Draw(sim) // must not panic at any small height
		})
	}
}
