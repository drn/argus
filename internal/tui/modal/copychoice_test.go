package modal

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestCopyChoiceModal_Decisions(t *testing.T) {
	noFocus := func(tview.Primitive) {}

	t.Run("enter selects name (default cursor)", func(t *testing.T) {
		m := NewCopyChoiceModal("Copy", true)
		m.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), noFocus)
		testutil.Equal(t, m.Selected(), CopyName)
		testutil.Equal(t, m.Canceled(), false)
	})

	t.Run("down then enter selects prompt", func(t *testing.T) {
		m := NewCopyChoiceModal("Copy", true)
		m.InputHandler()(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), noFocus)
		m.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), noFocus)
		testutil.Equal(t, m.Selected(), CopyPrompt)
	})

	t.Run("j/k navigate", func(t *testing.T) {
		m := NewCopyChoiceModal("Copy", true)
		m.InputHandler()(tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone), noFocus)
		m.InputHandler()(tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModNone), noFocus)
		m.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), noFocus)
		testutil.Equal(t, m.Selected(), CopyName)
	})

	t.Run("cursor clamps at bounds", func(t *testing.T) {
		m := NewCopyChoiceModal("Copy", true)
		// Two down moves on a 2-item list lands (and stays) on the last item.
		m.InputHandler()(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), noFocus)
		m.InputHandler()(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), noFocus)
		m.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), noFocus)
		testutil.Equal(t, m.Selected(), CopyPrompt)
	})

	t.Run("esc cancels", func(t *testing.T) {
		m := NewCopyChoiceModal("Copy", true)
		m.InputHandler()(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), noFocus)
		testutil.Equal(t, m.Canceled(), true)
		testutil.Equal(t, m.Selected(), CopyNone)
	})

	t.Run("ctrl+q cancels", func(t *testing.T) {
		m := NewCopyChoiceModal("Copy", true)
		m.InputHandler()(tcell.NewEventKey(tcell.KeyCtrlQ, 0, tcell.ModNone), noFocus)
		testutil.Equal(t, m.Canceled(), true)
	})
}

func TestCopyChoiceModal_PromptUnavailable(t *testing.T) {
	noFocus := func(tview.Primitive) {}
	m := NewCopyChoiceModal("Copy", false)

	// Only Name is offered; pressing down cannot reach a prompt choice.
	m.InputHandler()(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), noFocus)
	m.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, m.Selected(), CopyName)
}

func TestCopyChoiceModal_DrawNoPanic(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	const w, h = 80, 24
	sim.SetSize(w, h)

	m := NewCopyChoiceModal("Copy to clipboard", true)
	m.SetRect(0, 0, w, h)
	m.Draw(sim)
	sim.Show()

	rows := screenText(sim, w, h)
	var hasName, hasPrompt bool
	for _, r := range rows {
		if strings.Contains(r, "Name") {
			hasName = true
		}
		if strings.Contains(r, "Prompt") {
			hasPrompt = true
		}
	}
	testutil.Equal(t, hasName, true)
	testutil.Equal(t, hasPrompt, true)
}

func TestCopyChoiceModal_DrawTinyTerminal(t *testing.T) {
	for _, h := range []int{1, 2, 3, 4, 5, 6, 7, 8} {
		sim := tcell.NewSimulationScreen("UTF-8")
		testutil.NoError(t, sim.Init())
		const w = 40
		sim.SetSize(w, h)
		m := NewCopyChoiceModal("Copy to clipboard", true)
		m.SetRect(0, 0, w, h)
		m.Draw(sim) // must not panic at any small height
		sim.Fini()
	}
}
