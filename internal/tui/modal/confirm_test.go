package modal

import (
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
