package taskview

import (
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// RailPage draws the existing list and agent content once each. Keeping this
// wrapper focused lets mouse focus and paste follow the same region routing.
type RailPage struct {
	*tview.Box
	list        *TaskListView
	content     tview.Primitive
	terminal    tview.Primitive
	visible     bool
	Bound       bool
	PaneFocused func() bool
	OnRailFocus func()
}

func NewRailPage(list *TaskListView, content, terminal tview.Primitive) *RailPage {
	return &RailPage{Box: tview.NewBox(), list: list, content: content, terminal: terminal}
}

func (p *RailPage) ContentVisible() bool { return p.visible }

func (p *RailPage) Draw(screen tcell.Screen) {
	p.DrawForSubclass(screen, p)
	x, y, w, h := p.GetInnerRect()
	widget.FillArea(screen, x, y, w, h, ' ', theme.StyleDefault)
	rw := min(widget.RailWidth, w)
	p.list.SetRect(x, y, rw, h)
	p.list.Draw(screen)
	p.visible = w > rw && h > 0
	if !p.visible {
		if p.OnRailFocus != nil {
			p.OnRailFocus()
		}
		return
	}
	if !p.Bound {
		widget.DrawText(screen, x+rw+2, y+2, max(w-rw-2, 0), "Select a task · n creates a task", theme.StyleDimmed)
		return
	}
	p.content.SetRect(x+rw, y, w-rw, h)
	p.content.Draw(screen)
}

func (p *RailPage) InputHandler() func(*tcell.EventKey, func(tview.Primitive)) {
	return p.WrapInputHandler(func(e *tcell.EventKey, setFocus func(tview.Primitive)) {
		if p.PaneFocused == nil || !p.PaneFocused() {
			p.list.InputHandler()(e, func(tview.Primitive) { setFocus(p) })
		}
	})
}

func (p *RailPage) PasteHandler() func(string, func(tview.Primitive)) {
	return p.WrapPasteHandler(func(s string, setFocus func(tview.Primitive)) {
		target := tview.Primitive(p.list)
		if p.visible && p.Bound && p.PaneFocused != nil && p.PaneFocused() {
			target = p.terminal
		}
		if handler := target.PasteHandler(); handler != nil {
			handler(s, func(tview.Primitive) { setFocus(p) })
		}
	})
}

func (p *RailPage) MouseHandler() func(tview.MouseAction, *tcell.EventMouse, func(tview.Primitive)) (bool, tview.Primitive) {
	return p.WrapMouseHandler(func(action tview.MouseAction, e *tcell.EventMouse, setFocus func(tview.Primitive)) (bool, tview.Primitive) {
		if !p.InRect(e.Position()) {
			return false, nil
		}
		guard := func(tview.Primitive) { setFocus(p) }
		if p.list.InRect(e.Position()) {
			if p.OnRailFocus != nil {
				p.OnRailFocus()
			}
			return p.list.MouseHandler()(action, e, guard)
		}
		if p.visible && p.Bound {
			if handler := p.content.MouseHandler(); handler != nil {
				return handler(action, e, guard)
			}
		}
		return false, nil
	})
}
