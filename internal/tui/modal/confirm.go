package modal

import (
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// ConfirmModal is a generic confirmation dialog: a title, a one- or two-line
// message, and a y/N decision. Enter or `y` confirms; Esc, Ctrl+Q, or `n`
// cancels. It carries no text input (so no PasteHandler is needed) and reusable
// by any caller that needs a destructive-action gate. The Hera view uses it for
// archive-of-live and delete confirmation.
type ConfirmModal struct {
	*tview.Box
	title     string
	message   string
	confirmed bool
	canceled  bool
}

// NewConfirmModal builds a confirm dialog with the given title and message.
func NewConfirmModal(title, message string) *ConfirmModal {
	return &ConfirmModal{Box: tview.NewBox(), title: title, message: message}
}

func (m *ConfirmModal) Confirmed() bool { return m.confirmed }
func (m *ConfirmModal) Canceled() bool  { return m.canceled }

// InputHandler handles key events for the confirm dialog.
func (m *ConfirmModal) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return m.WrapInputHandler(func(event *tcell.EventKey, _ func(p tview.Primitive)) {
		switch event.Key() {
		case tcell.KeyEnter:
			m.confirmed = true
		case tcell.KeyEscape, tcell.KeyCtrlQ:
			m.canceled = true
		case tcell.KeyRune:
			switch event.Rune() {
			case 'y', 'Y':
				m.confirmed = true
			case 'n', 'N':
				m.canceled = true
			}
		}
	})
}

// Draw renders the confirm dialog centered, covering its full panel rect.
func (m *ConfirmModal) Draw(screen tcell.Screen) {
	m.DrawForSubclass(screen, m)
	x, y, width, height := m.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	formW := min(64, width-4)
	if formW < 12 {
		formW = width
	}

	// Word-wrap the message so it never overflows the border (the title and
	// footer stay single-line; only the body grows).
	lines := wrapErrorBody(m.message, formW-4)
	// Cap body lines so a pathological message can't exceed the screen.
	maxBody := max(height-6, 1)
	if len(lines) > maxBody {
		lines = lines[:maxBody]
	}
	formH := min(6+len(lines), height)
	formX := x + (width-formW)/2
	formY := max(y+(height-formH)/2, y)

	// Clear the modal area so no stale cells survive (no Sync — full-rect cover).
	for row := formY; row < formY+formH && row < y+height; row++ {
		for col := formX; col < formX+formW && col < x+width; col++ {
			screen.SetContent(col, row, ' ', nil, tcell.StyleDefault)
		}
	}

	widget.DrawBorder(screen, formX, formY, formW, formH, theme.StyleFocusedBorder)
	widget.DrawText(screen, formX+2, formY+1, formW-4, m.title, theme.StyleTitle)
	for i, line := range lines {
		widget.DrawText(screen, formX+2, formY+3+i, formW-4, line, theme.StyleNormal)
	}
	widget.DrawText(screen, formX+2, formY+formH-2, formW-4, "Enter / y = confirm    Esc / n = cancel", theme.StyleDimmed)
}
