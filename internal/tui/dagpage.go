package tui

import (
	"github.com/drn/argus/internal/tui/dagview"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// DAGPage wraps the dagview.Widget with a tview-compatible container that
// can be added as a Page in the app's Pages.
//
// The widget is non-interactive in the sense that the page itself does not
// forward focus on the wrapper Box's default MouseHandler (which would
// silently steal focus from the inner widget). The MouseHandler below
// always routes clicks to the inner widget so keyboard input goes to a
// primitive that actually handles it. See gotchas/tasklist-ui.md and
// CLAUDE.md's "page wrapper MouseHandler rule".
type DAGPage struct {
	*tview.Box
	dag *dagview.Widget
}

// NewDAGPage wraps an existing dagview.Widget.
func NewDAGPage(d *dagview.Widget) *DAGPage {
	return &DAGPage{
		Box: tview.NewBox(),
		dag: d,
	}
}

// DAG returns the inner widget so the App can wire callbacks at construction.
func (p *DAGPage) DAG() *dagview.Widget {
	return p.dag
}

func (p *DAGPage) Draw(screen tcell.Screen) {
	p.Box.DrawForSubclass(screen, p)
	x, y, width, height := p.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}
	p.dag.SetRect(x, y, width, height)
	p.dag.Draw(screen)
}

// InputHandler forwards key events to the inner widget; without this, focus
// on the page wrapper would swallow every keystroke (tview.Box default).
func (p *DAGPage) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return p.dag.InputHandler()
}

// PasteHandler is a no-op for the DAG view — the inner widget already
// declines paste, but we forward it explicitly so the focus-boundary rule
// in CLAUDE.md is satisfied.
func (p *DAGPage) PasteHandler() func(pastedText string, setFocus func(p tview.Primitive)) {
	return p.dag.PasteHandler()
}

// MouseHandler redirects all clicks to the inner widget. Without this, the
// default Box.MouseHandler would steal focus to the non-interactive page
// wrapper and drop subsequent keyboard input. Pattern matches SettingsPage
// and TaskPage.
func (p *DAGPage) MouseHandler() func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
	return p.WrapMouseHandler(func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
		handler := p.dag.MouseHandler()
		consumed, _ := handler(action, event, setFocus)
		if action == tview.MouseLeftClick && consumed {
			setFocus(p.dag)
		}
		return consumed, nil
	})
}
