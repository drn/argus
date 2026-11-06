package taskview

import (
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type railProbe struct {
	*tview.Box
	pasted string
}

func (p *railProbe) PasteHandler() func(string, func(tview.Primitive)) {
	return func(s string, setFocus func(tview.Primitive)) { p.pasted = s; setFocus(p) }
}
func (p *railProbe) MouseHandler() func(tview.MouseAction, *tcell.EventMouse, func(tview.Primitive)) (bool, tview.Primitive) {
	return func(_ tview.MouseAction, _ *tcell.EventMouse, setFocus func(tview.Primitive)) (bool, tview.Primitive) {
		setFocus(p)
		return true, nil
	}
}

func TestRailPageLayoutAndRouting(t *testing.T) {
	sim := tcell.NewSimulationScreen("")
	testutil.NoError(t, sim.Init())
	t.Cleanup(sim.Fini)
	sim.SetSize(120, 30)
	list := NewTaskListView()
	list.SetRailMode(true)
	list.SetTasks([]*model.Task{{ID: "a", Project: "A", Name: "alpha"}})
	content := &railProbe{Box: tview.NewBox()}
	page := NewRailPage(list, content, content)
	focused := false
	page.PaneFocused = func() bool { return focused }
	railFocus := 0
	page.OnRailFocus = func() { railFocus++ }
	page.SetRect(0, 0, 120, 30)
	page.Draw(sim)
	testutil.Equal(t, page.ContentVisible(), true)
	page.Bound = true
	page.Draw(sim)
	x, _, w, _ := content.GetRect()
	testutil.Equal(t, x, 34)
	testutil.Equal(t, w, 86)
	page.InputHandler()(tcell.NewEventKey(tcell.KeyDown, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, list.SelectedTask().ID, "a")
	focused = true
	page.InputHandler()(tcell.NewEventKey(tcell.KeyUp, 0, 0), func(tview.Primitive) {})
	testutil.Equal(t, list.SelectedTask().ID, "a")
	var target tview.Primitive
	page.PasteHandler()("hello", func(p tview.Primitive) { target = p })
	testutil.Equal(t, content.pasted, "hello")
	testutil.Equal(t, target, tview.Primitive(page))
	focused = false
	page.InputHandler()(tcell.NewEventKey(tcell.KeyRune, '/', 0), func(tview.Primitive) {})
	page.PasteHandler()("alpha", func(tview.Primitive) {})
	testutil.Equal(t, list.Filter(), "alpha")
	handler := page.MouseHandler()
	consumed, _ := handler(tview.MouseLeftDown, tcell.NewEventMouse(40, 5, tcell.Button1, 0), func(p tview.Primitive) { target = p })
	testutil.Equal(t, consumed, true)
	testutil.Equal(t, target, tview.Primitive(page))
	consumed, _ = handler(tview.MouseLeftDown, tcell.NewEventMouse(5, 5, tcell.Button1, 0), func(p tview.Primitive) { target = p })
	testutil.Equal(t, consumed, true)
	testutil.Equal(t, railFocus, 1)
	consumed, _ = handler(tview.MouseLeftDown, tcell.NewEventMouse(150, 5, tcell.Button1, 0), func(tview.Primitive) {})
	testutil.Equal(t, consumed, false)
	page.SetRect(0, 0, 30, 10)
	page.Draw(sim)
	testutil.Equal(t, page.ContentVisible(), false)
	testutil.Equal(t, railFocus, 2)
}
