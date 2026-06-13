package modal

import (
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
