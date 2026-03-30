package tui2

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/model"
)

// ForkTaskModal shows a confirmation dialog before forking a task.
// Pressing Enter confirms, Esc cancels.
type ForkTaskModal struct {
	*tview.Box
	task      *model.Task
	confirmed bool
	canceled  bool
}

// NewForkTaskModal creates a fork confirmation dialog for the given task.
func NewForkTaskModal(task *model.Task) *ForkTaskModal {
	return &ForkTaskModal{
		Box:  tview.NewBox(),
		task: task,
	}
}

func (m *ForkTaskModal) Confirmed() bool    { return m.confirmed }
func (m *ForkTaskModal) Canceled() bool     { return m.canceled }
func (m *ForkTaskModal) Task() *model.Task  { return m.task }

// InputHandler handles key events for the fork dialog.
func (m *ForkTaskModal) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return m.WrapInputHandler(func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		switch event.Key() {
		case tcell.KeyEnter:
			m.confirmed = true
		case tcell.KeyEscape, tcell.KeyCtrlQ:
			m.canceled = true
		}
	})
}

// Draw renders the fork task modal as a centered dialog.
func (m *ForkTaskModal) Draw(screen tcell.Screen) {
	m.Box.DrawForSubclass(screen, m)
	x, y, width, height := m.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	formW := min(60, width-4)
	formH := 11
	formX := x + (width-formW)/2
	formY := max(y+(height-formH)/2, y)

	// Clear the modal area.
	clearStyle := tcell.StyleDefault
	for row := formY; row < formY+formH && row < y+height; row++ {
		for col := formX; col < formX+formW; col++ {
			screen.SetContent(col, row, ' ', nil, clearStyle)
		}
	}

	drawBorder(screen, formX, formY, formW, formH, StyleFocusedBorder)
	drawText(screen, formX+2, formY+1, formW-4, "Fork task?", StyleTitle)

	// Source task name.
	drawText(screen, formX+4, formY+3, formW-6, m.task.Name, StyleNormal)

	// Details.
	if m.task.Worktree != "" {
		drawText(screen, formX+4, formY+4, formW-6, "worktree: "+m.task.Worktree, StyleDimmed)
	}
	if m.task.Branch != "" {
		drawText(screen, formX+4, formY+5, formW-6, "branch: "+m.task.Branch, StyleDimmed)
	}

	drawText(screen, formX+4, formY+7, formW-6, "Creates a new task with context from", StyleDimmed)
	drawText(screen, formX+4, formY+8, formW-6, "the source agent's output and diff.", StyleDimmed)

	// Hint.
	drawText(screen, formX+4, formY+formH-2, formW-6, "[enter] confirm  [esc] cancel", StyleDimmed)
}
