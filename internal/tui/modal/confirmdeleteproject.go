package modal

import (
	"fmt"

	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// ConfirmDeleteProjectModal shows a confirmation dialog before deleting a project.
// Pressing Enter confirms, Esc cancels.
type ConfirmDeleteProjectModal struct {
	*tview.Box
	name      string
	taskCount int
	confirmed bool
	canceled  bool
}

// NewConfirmDeleteProjectModal creates a confirm dialog for the given project.
func NewConfirmDeleteProjectModal(name string, taskCount int) *ConfirmDeleteProjectModal {
	return &ConfirmDeleteProjectModal{
		Box:       tview.NewBox(),
		name:      name,
		taskCount: taskCount,
	}
}

func (m *ConfirmDeleteProjectModal) Confirmed() bool { return m.confirmed }
func (m *ConfirmDeleteProjectModal) Canceled() bool  { return m.canceled }
func (m *ConfirmDeleteProjectModal) Name() string    { return m.name }

// InputHandler handles key events for the confirm dialog.
func (m *ConfirmDeleteProjectModal) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return m.WrapInputHandler(func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		switch event.Key() {
		case tcell.KeyEnter:
			m.confirmed = true
		case tcell.KeyEscape, tcell.KeyCtrlQ:
			m.canceled = true
		}
	})
}

// Draw renders the confirm delete project modal as a centered dialog.
func (m *ConfirmDeleteProjectModal) Draw(screen tcell.Screen) {
	m.Box.DrawForSubclass(screen, m)
	x, y, width, height := m.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	formW := min(60, width-4)
	formH := 7
	if m.taskCount > 0 {
		formH = 9
	}
	formX := x + (width-formW)/2
	formY := y + (height-formH)/2
	if formY < y {
		formY = y
	}

	// Clear the modal area.
	clearStyle := tcell.StyleDefault
	for row := formY; row < formY+formH && row < y+height; row++ {
		for col := formX; col < formX+formW; col++ {
			screen.SetContent(col, row, ' ', nil, clearStyle)
		}
	}

	widget.DrawBorder(screen, formX, formY, formW, formH, theme.StyleFocusedBorder)
	widget.DrawText(screen, formX+2, formY+1, formW-4, "Delete project?", theme.StyleTitle)

	// Project name.
	widget.DrawText(screen, formX+4, formY+3, formW-6, m.name, theme.StyleNormal)

	row := formY + 4
	if m.taskCount > 0 {
		warning := fmt.Sprintf("  %d task(s) will be orphaned", m.taskCount)
		widget.DrawText(screen, formX+2, row, formW-4, warning, tcell.StyleDefault.Foreground(theme.ColorError))
		row += 2
	}

	// Hint.
	widget.DrawText(screen, formX+4, row+1, formW-6, "[enter] confirm  [esc] cancel", theme.StyleDimmed)
}
