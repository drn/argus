package tui

import (
	"github.com/drn/argus/internal/tui/dagview"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// DAGPage wraps the dagview.Widget with a tview-compatible container that
// can be added as a Page in the app's Pages.
//
// Focus contract: the page wrapper holds keyboard focus; its InputHandler /
// PasteHandler delegate to the inner widget. MouseHandler forwards clicks
// to the inner widget for cursor hit-testing but always re-anchors focus on
// the page on left click, so a click outside any node (border, blank area)
// does not let tview's default Box.MouseHandler park focus on an inert
// primitive and drop subsequent keystrokes. See gotchas/tasklist-ui.md and
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
	p.DrawForSubclass(screen, p)
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

// MouseHandler forwards clicks to the inner widget for cursor hit-testing,
// then anchors focus back on the page wrapper. The wrapper's InputHandler /
// PasteHandler delegate to the inner widget, so keyboard input flows
// correctly even though tview's focus tracker sees the wrapper. Pattern
// matches SettingsPage and TaskPage — required by CLAUDE.md's page-wrapper
// MouseHandler rule (without unconditional setFocus, a click on a
// non-interactive area would let the default Box.MouseHandler leave focus
// on whichever primitive had it before, silently dropping keystrokes).
func (p *DAGPage) MouseHandler() func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
	return p.WrapMouseHandler(func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
		handler := p.dag.MouseHandler()
		consumed, _ := handler(action, event, setFocus)
		if action == tview.MouseLeftClick || action == tview.MouseLeftDown {
			setFocus(p)
		}
		return consumed, nil
	})
}
