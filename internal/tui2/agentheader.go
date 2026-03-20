package tui2

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// AgentHeader renders a single-row header in the agent view showing the task name
// using the same powerline style as the root Header.
type AgentHeader struct {
	*tview.Box
	taskName string
}

// NewAgentHeader creates an agent view header.
func NewAgentHeader() *AgentHeader {
	return &AgentHeader{
		Box: tview.NewBox(),
	}
}

// SetTaskName updates the displayed task name.
func (h *AgentHeader) SetTaskName(name string) {
	h.taskName = name
}

// Draw renders the header with a powerline-style segment containing the task name.
func (h *AgentHeader) Draw(screen tcell.Screen) {
	h.Box.DrawForSubclass(screen, h)
	x, y, width, _ := h.GetInnerRect()
	if width <= 0 {
		return
	}

	// Fill entire row with base background
	baseStyle := tcell.StyleDefault.Background(headerBaseBG)
	for i := 0; i < width; i++ {
		screen.SetContent(x+i, y, ' ', nil, baseStyle)
	}

	if h.taskName == "" {
		return
	}

	text := " " + h.taskName + " "

	// Compute segment width: open chevron + text + close chevron
	segWidth := 1 + len(text) + 1
	col := x + (width-segWidth)/2
	if col < x {
		col = x
	}

	// Opening separator: base → segment
	if col < x+width {
		screen.SetContent(col, y, powerlineSep, nil,
			tcell.StyleDefault.Foreground(headerBaseBG).Background(headerActiveBG))
		col++
	}

	// Text
	textStyle := tcell.StyleDefault.Foreground(headerActiveFG).Background(headerActiveBG).Bold(true)
	for _, r := range text {
		if col >= x+width {
			break
		}
		screen.SetContent(col, y, r, nil, textStyle)
		col++
	}

	// Closing separator: segment → base
	if col < x+width {
		screen.SetContent(col, y, powerlineSep, nil,
			tcell.StyleDefault.Foreground(headerActiveBG).Background(headerBaseBG))
	}
}
