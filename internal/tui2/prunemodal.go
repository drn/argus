package tui2

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const pruneModalWidth = 50

// PruneModal shows progress during worktree/branch cleanup.
// All keys are absorbed while displayed — the background goroutine
// auto-dismisses the modal via closePruneModal when cleanup finishes.
type PruneModal struct {
	*tview.Box
	total    int
	tickEven bool // toggles for animated dots
}

// NewPruneModal creates a prune progress modal.
func NewPruneModal(total int) *PruneModal {
	return &PruneModal{
		Box:   tview.NewBox(),
		total: total,
	}
}

// Tick toggles the animation state.
func (m *PruneModal) Tick() { m.tickEven = !m.tickEven }

// InputHandler absorbs all key events while cleanup is running.
func (m *PruneModal) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return m.WrapInputHandler(func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		// All keys absorbed — modal is dismissed by the cleanup goroutine.
	})
}

// Draw renders the prune modal as a centered dialog.
func (m *PruneModal) Draw(screen tcell.Screen) {
	m.Box.DrawForSubclass(screen, m)
	x, y, width, height := m.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	formW := min(pruneModalWidth, width-4)
	formH := 5
	formX := x + (width-formW)/2
	// Clamp to top of inner rect when viewport is too small.
	formY := max(y, y+(height-formH)/2)

	// Clear the modal area.
	clearStyle := tcell.StyleDefault
	for row := formY; row < formY+formH && row < y+height; row++ {
		for col := formX; col < formX+formW; col++ {
			screen.SetContent(col, row, ' ', nil, clearStyle)
		}
	}

	drawBorder(screen, formX, formY, formW, formH, StyleFocusedBorder)

	dots := ".  "
	if m.tickEven {
		dots = ".. "
	}
	msg := fmt.Sprintf("Cleaning up %d worktree(s)%s", m.total, dots)
	drawText(screen, formX+2, formY+2, formW-4, msg, StyleNormal)
}
