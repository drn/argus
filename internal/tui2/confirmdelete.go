package tui2

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/model"
)

// ConfirmDeleteModal shows a confirmation dialog before deleting a task.
// Pressing Enter confirms, Esc cancels.
type ConfirmDeleteModal struct {
	*tview.Box
	task      *model.Task
	confirmed bool
	canceled  bool
}

// NewConfirmDeleteModal creates a confirm dialog for the given task.
func NewConfirmDeleteModal(task *model.Task) *ConfirmDeleteModal {
	return &ConfirmDeleteModal{
		Box:  tview.NewBox(),
		task: task,
	}
}

func (m *ConfirmDeleteModal) Confirmed() bool { return m.confirmed }
func (m *ConfirmDeleteModal) Canceled() bool  { return m.canceled }
func (m *ConfirmDeleteModal) Task() *model.Task { return m.task }

// InputHandler handles key events for the confirm dialog.
func (m *ConfirmDeleteModal) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return m.WrapInputHandler(func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		switch event.Key() {
		case tcell.KeyEnter:
			m.confirmed = true
		case tcell.KeyEscape:
			m.canceled = true
		}
	})
}

// Draw renders the confirm delete modal as a centered dialog.
func (m *ConfirmDeleteModal) Draw(screen tcell.Screen) {
	m.Box.DrawForSubclass(screen, m)
	x, y, width, height := m.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	formW := min(60, width-4)
	formH := 9
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

	drawBorder(screen, formX, formY, formW, formH, StyleFocusedBorder)
	drawText(screen, formX+2, formY+1, formW-4, "Delete task?", StyleTitle)

	// Task name.
	drawText(screen, formX+4, formY+3, formW-6, m.task.Name, StyleNormal)

	// Worktree/branch details.
	if m.task.Worktree != "" {
		drawText(screen, formX+4, formY+4, formW-6, "worktree: "+m.task.Worktree, StyleDimmed)
	}
	if m.task.Branch != "" {
		drawText(screen, formX+4, formY+5, formW-6, "branch: "+m.task.Branch, StyleDimmed)
	}

	// Hint.
	drawText(screen, formX+4, formY+7, formW-6, "[enter] confirm  [esc] cancel", StyleDimmed)
}
