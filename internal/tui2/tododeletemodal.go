package tui2

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// ConfirmDeleteToDoModal shows a confirmation dialog before deleting a to-do
// vault file. Pressing Enter confirms, Esc cancels.
type ConfirmDeleteToDoModal struct {
	*tview.Box
	item      ToDoItem
	confirmed bool
	canceled  bool
}

// NewConfirmDeleteToDoModal creates a confirm dialog for the given to-do item.
func NewConfirmDeleteToDoModal(item ToDoItem) *ConfirmDeleteToDoModal {
	return &ConfirmDeleteToDoModal{
		Box:  tview.NewBox(),
		item: item,
	}
}

func (m *ConfirmDeleteToDoModal) Confirmed() bool { return m.confirmed }
func (m *ConfirmDeleteToDoModal) Canceled() bool  { return m.canceled }
func (m *ConfirmDeleteToDoModal) Item() ToDoItem   { return m.item }

// InputHandler handles key events for the confirm dialog.
func (m *ConfirmDeleteToDoModal) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return m.WrapInputHandler(func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		switch event.Key() {
		case tcell.KeyEnter:
			m.confirmed = true
		case tcell.KeyEscape:
			m.canceled = true
		}
	})
}

// Draw renders the confirm delete to-do modal as a centered dialog.
func (m *ConfirmDeleteToDoModal) Draw(screen tcell.Screen) {
	m.Box.DrawForSubclass(screen, m)
	x, y, width, height := m.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	formW := min(60, width-4)
	formH := 7
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
	drawText(screen, formX+2, formY+1, formW-4, "Delete to-do?", StyleTitle)

	// To-do name.
	name := m.item.Name
	maxW := formW - 6
	if maxW > 3 && len(name) > maxW {
		name = name[:maxW-1] + "…"
	}
	drawText(screen, formX+4, formY+3, formW-6, name, StyleNormal)

	// Hint.
	drawText(screen, formX+4, formY+5, formW-6, "[enter] confirm  [esc] cancel", StyleDimmed)
}
