package tui2

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Tab identifies the active top-level tab.
type Tab int

const (
	TabTasks Tab = iota
	TabReviews
	TabSettings
)

var tabLabels = [...]string{"Tasks", "Reviews", "Settings"}
var tabKeys = [...]string{"1", "2", "3"}

// Header renders the top tab bar.
type Header struct {
	*tview.Box
	activeTab Tab
}

// NewHeader creates a tab bar header.
func NewHeader() *Header {
	h := &Header{
		Box:       tview.NewBox(),
		activeTab: TabTasks,
	}
	return h
}

// SetTab changes the active tab.
func (h *Header) SetTab(t Tab) {
	h.activeTab = t
}

// ActiveTab returns the current tab.
func (h *Header) ActiveTab() Tab {
	return h.activeTab
}

// Draw renders the tab bar.
func (h *Header) Draw(screen tcell.Screen) {
	h.Box.DrawForSubclass(screen, h)
	x, y, width, _ := h.GetInnerRect()
	if width <= 0 {
		return
	}

	// Draw "argus" title
	titleStyle := tcell.StyleDefault.Foreground(ColorTitle).Bold(true)
	title := " argus "
	for i, r := range title {
		if x+i >= x+width {
			break
		}
		screen.SetContent(x+i, y, r, nil, titleStyle)
	}
	col := x + len(title) + 1

	// Draw tabs
	for i, label := range tabLabels {
		if col >= x+width {
			break
		}

		text := fmt.Sprintf(" %s %s ", tabKeys[i], label)
		var style tcell.Style
		if Tab(i) == h.activeTab {
			style = tcell.StyleDefault.Foreground(ColorTitle).Bold(true)
		} else {
			style = tcell.StyleDefault.Foreground(ColorDimmed)
		}

		for _, r := range text {
			if col >= x+width {
				break
			}
			screen.SetContent(col, y, r, nil, style)
			col++
		}
		// Separator
		if i < len(tabLabels)-1 && col < x+width {
			screen.SetContent(col, y, '|', nil, tcell.StyleDefault.Foreground(ColorBorder))
			col++
		}
	}
}
