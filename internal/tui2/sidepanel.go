package tui2

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// SidePanel is a bordered panel that displays text content.
// Used for the git status (left) and file explorer (right) panels.
type SidePanel struct {
	*tview.Box
	title   string
	lines   []string
	focused bool
}

// NewSidePanel creates a bordered side panel with a title.
func NewSidePanel(title string) *SidePanel {
	return &SidePanel{
		Box:   tview.NewBox(),
		title: title,
	}
}

// SetLines updates the content lines.
func (sp *SidePanel) SetLines(lines []string) {
	sp.lines = lines
}

// SetFocused sets the focus state for border rendering.
func (sp *SidePanel) SetFocused(f bool) {
	sp.focused = f
}

// Draw renders the side panel with border and content.
func (sp *SidePanel) Draw(screen tcell.Screen) {
	sp.Box.DrawForSubclass(screen, sp)
	x, y, width, height := sp.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	// Draw border around the full box area (1 cell outside inner rect)
	borderStyle := StyleBorder
	if sp.focused {
		borderStyle = StyleFocusedBorder
	}
	drawBorder(screen, x-1, y-1, width+2, height+2, borderStyle)

	// Draw title in the top border
	if sp.title != "" {
		titleText := " " + sp.title + " "
		titleX := x
		for i, r := range titleText {
			if titleX+i >= x+width {
				break
			}
			screen.SetContent(titleX+i, y-1, r, nil, borderStyle.Bold(true))
		}
	}

	// Draw content lines
	for i, line := range sp.lines {
		if i >= height {
			break
		}
		drawText(screen, x, y+i, width, line, StyleNormal)
	}
}
