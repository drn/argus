package hera

import (
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/drn/argus/internal/uxlog"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// heraRailWidth is the fixed character width of the navigation rail (matches
// Hera's RailWidth ballpark, trimmed for Argus chrome).
const heraRailWidth = 34

// HeraPage is the top-level Hera-view page added to the App's Pages. It lays
// out the three Hera regions — rail | coordinator pane | details/agent pane —
// but in 6a only the LEFT rail is live; the two right panes are placeholders
// (their PTY feeds are 6b). Layout is computed in Draw (like DAGPage) rather
// than via a tview.Flex, so focus/input routing stays simple: the wrapper holds
// focus and delegates input to the rail.
//
// Remote-mode degradation: when the App can't resolve a local *db.DB hera
// reader (i.e. --remote mode, where apistore has no hera methods), it
// constructs the page with a nil reader. The page then renders an
// "Hera unavailable in remote mode" banner instead of a rail — it never
// panics and never breaks the --remote build (see gotchas/remote-tui.md).
type HeraPage struct {
	*tview.Box

	rail      *Rail
	focus     *FocusMachine
	refresher *Refresher
	reader    HeraReader
	remote    bool
}

// NewHeraPage builds the page against a hera reader. Pass nil for remote mode.
func NewHeraPage(reader HeraReader) *HeraPage {
	p := &HeraPage{
		Box:    tview.NewBox(),
		rail:   NewRail(),
		focus:  NewFocusMachine(),
		reader: reader,
		remote: reader == nil,
	}
	p.rail.SetFocused(true) // rail is the only interactive region in 6a
	p.refresher = NewRefresher(DefaultRefreshDebounce, p.doRefresh)
	return p
}

// Rail exposes the inner rail (test seam + 6b wiring).
func (p *HeraPage) Rail() *Rail { return p.rail }

// Machine exposes the focus machine (test seam + 6b wiring). Not named Focus()
// because that collides with tview.Primitive's Focus(func(tview.Primitive)).
func (p *HeraPage) Machine() *FocusMachine { return p.focus }

// IsRemote reports whether the page is in the remote-degraded mode.
func (p *HeraPage) IsRemote() bool { return p.remote }

// ScheduleRefresh requests a debounced rail rebuild. Called from the app tick
// while the Hera tab is active; bursts coalesce to one rebuild per window.
// MUST run on the tview thread.
func (p *HeraPage) ScheduleRefresh() { p.refresher.Schedule() }

// Refresh forces an immediate rail rebuild (used on tab entry so the rail is
// fresh the instant the tab opens). MUST run on the tview thread.
func (p *HeraPage) Refresh() {
	p.refresher.Schedule()
	p.refresher.Flush()
}

// doRefresh rebuilds the model from the reader and hands it to the rail. In
// remote mode the reader is nil → BuildModel returns an empty model and Draw
// renders the unavailable banner, so this stays a cheap no-op.
func (p *HeraPage) doRefresh() {
	m, err := BuildModel(p.reader)
	if err != nil {
		uxlog.Log("[hera-view] rail refresh failed: %v", err)
		return
	}
	p.rail.SetModel(m)
	uxlog.Log("[hera-view] rail refreshed: pinned=%d active=%d archived=%d freelance=%d (remote=%v)",
		len(m.Pinned), len(m.Active), len(m.Archived), len(m.Freelance), p.remote)
}

// Draw computes the three-region layout and paints each region, covering the
// full bounding rect (DrawBorderedPanel / FillArea) so no stale cells survive —
// per the CLAUDE.md UX-rendering rules (no Sync; full-rect coverage instead).
func (p *HeraPage) Draw(screen tcell.Screen) {
	p.DrawForSubclass(screen, p)
	x, y, w, h := p.GetInnerRect()
	if w <= 0 || h <= 0 {
		return
	}

	if p.remote {
		p.drawRemoteBanner(screen, x, y, w, h)
		return
	}

	railW := heraRailWidth
	if railW > w {
		railW = w
	}
	p.rail.SetFocused(p.focus.State() == FocusRail)
	p.rail.SetRect(x, y, railW, h)
	p.rail.Draw(screen)

	// Right area: split into coordinator + agent placeholders. When the
	// terminal is too narrow for a right area, the rail simply takes it all.
	rx := x + railW
	rw := w - railW
	if rw <= 0 {
		return
	}
	coordW := rw / 2
	agentW := rw - coordW
	p.drawPlaceholder(screen, rx, y, coordW, h, " HERA ",
		"coordinator pane", p.focus.State() == FocusCoord)
	p.drawPlaceholder(screen, rx+coordW, y, agentW, h, " AGENT ",
		"details / agent pane", p.focus.State() == FocusAgent)
}

// drawPlaceholder renders a bordered region with a centered "wired in 6b"
// banner. 6b replaces the banner body with the live coordinator/agent PTY feed.
func (p *HeraPage) drawPlaceholder(screen tcell.Screen, x, y, w, h int, title, what string, focused bool) {
	if w < 2 || h < 2 {
		return
	}
	style := theme.StyleBorder
	if focused {
		style = theme.StyleFocusedBorder
	}
	inner := widget.DrawBorderedPanel(screen, x, y, w, h, title, style)
	if inner.W <= 0 || inner.H <= 0 {
		return
	}
	// M6b: replace this banner with the in-process runner-fed PTY pane.
	msg := what
	row := inner.Y + inner.H/2
	col := inner.X + (inner.W-len(msg))/2
	if col < inner.X {
		col = inner.X
	}
	widget.DrawText(screen, col, row, inner.W, msg, theme.StyleDimmed)
	hint := "select a role →"
	if inner.H > 2 {
		hcol := inner.X + (inner.W-len(hint))/2
		if hcol < inner.X {
			hcol = inner.X
		}
		widget.DrawText(screen, hcol, row+1, inner.W, hint, theme.StyleDimmed)
	}
}

// drawRemoteBanner renders the remote-mode degradation message over the whole
// page area.
func (p *HeraPage) drawRemoteBanner(screen tcell.Screen, x, y, w, h int) {
	inner := widget.DrawBorderedPanel(screen, x, y, w, h, " Hera ", theme.StyleBorder)
	if inner.W <= 0 || inner.H <= 0 {
		return
	}
	msg := "Hera unavailable in remote mode"
	row := inner.Y + inner.H/2
	col := inner.X + (inner.W-len(msg))/2
	if col < inner.X {
		col = inner.X
	}
	widget.DrawText(screen, col, row, inner.W, msg, theme.StyleDimmed)
}

// InputHandler delegates to the rail (the only interactive region in 6a).
// 6b: route Tab / Ctrl+arrows here to drive focus.Advance/Retreat once the
// panes carry live feeds.
func (p *HeraPage) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return p.rail.InputHandler()
}

// PasteHandler is a no-op — the rail accepts no text input in 6a. Declared
// explicitly to satisfy the CLAUDE.md page-wrapper rule (widgets that route
// input must declare a PasteHandler so bracket paste doesn't silently vanish).
func (p *HeraPage) PasteHandler() func(pastedText string, setFocus func(p tview.Primitive)) {
	return p.WrapPasteHandler(func(string, func(tview.Primitive)) {})
}

// MouseHandler forwards clicks for hit-testing, then anchors focus back on the
// page wrapper. The wrapper's InputHandler delegates to the rail, so keyboard
// input keeps flowing even though tview's focus tracker sees the wrapper. This
// is the CLAUDE.md page-wrapper focus guard: without unconditional setFocus, a
// click on a non-interactive placeholder pane would let the default
// Box.MouseHandler leave focus there and silently drop keystrokes. Pattern
// matches DAGPage / TaskPage / SettingsPage.
func (p *HeraPage) MouseHandler() func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
	return p.WrapMouseHandler(func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
		consumed := false
		if !p.remote {
			handler := p.rail.MouseHandler()
			consumed, _ = handler(action, event, setFocus)
		}
		if action == tview.MouseLeftClick || action == tview.MouseLeftDown {
			setFocus(p)
		}
		return consumed, nil
	})
}
