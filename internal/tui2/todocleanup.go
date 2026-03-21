package tui2

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// ConfirmCleanupToDosModal shows a confirmation dialog before deleting
// completed to-do vault files. Pressing Enter confirms, Esc cancels.
type ConfirmCleanupToDosModal struct {
	*tview.Box
	count     int
	confirmed bool
	canceled  bool
}

// NewConfirmCleanupToDosModal creates a confirm dialog for cleaning up N completed to-dos.
func NewConfirmCleanupToDosModal(count int) *ConfirmCleanupToDosModal {
	return &ConfirmCleanupToDosModal{
		Box:   tview.NewBox(),
		count: count,
	}
}

func (m *ConfirmCleanupToDosModal) Confirmed() bool { return m.confirmed }
func (m *ConfirmCleanupToDosModal) Canceled() bool  { return m.canceled }
func (m *ConfirmCleanupToDosModal) Count() int       { return m.count }

// InputHandler handles key events for the confirm dialog.
func (m *ConfirmCleanupToDosModal) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return m.WrapInputHandler(func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		switch event.Key() {
		case tcell.KeyEnter:
			m.confirmed = true
		case tcell.KeyEscape:
			m.canceled = true
		}
	})
}

// Draw renders the confirm cleanup modal as a centered dialog.
func (m *ConfirmCleanupToDosModal) Draw(screen tcell.Screen) {
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
	drawText(screen, formX+2, formY+1, formW-4, "Clean up completed to-dos?", StyleTitle)

	noun := "note"
	if m.count != 1 {
		noun = "notes"
	}
	detail := fmt.Sprintf("Delete %d completed %s from vault", m.count, noun)
	drawText(screen, formX+4, formY+3, formW-6, detail, StyleNormal)

	drawText(screen, formX+4, formY+5, formW-6, "[enter] confirm  [esc] cancel", StyleDimmed)
}
