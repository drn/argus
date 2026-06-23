package modal

import (
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// CopyTarget identifies which task field a CopyChoiceModal selection refers to.
type CopyTarget int

const (
	// CopyNone means no choice has been made yet (the modal is still open or
	// was canceled).
	CopyNone CopyTarget = iota
	// CopyName copies the task name.
	CopyName
	// CopyPrompt copies the task prompt.
	CopyPrompt
)

// copyChoice is one selectable row in the modal.
type copyChoice struct {
	label  string
	target CopyTarget
}

// CopyChoiceModal is a small selection dialog that asks which field of a task
// to copy to the clipboard: the name or the prompt. The prompt choice is only
// offered when the task has a non-empty prompt (the name is always available).
// Up/Down (or j/k) move the cursor; Enter confirms; Esc / Ctrl+Q cancels. It
// carries no text input (so no PasteHandler is needed).
type CopyChoiceModal struct {
	*tview.Box
	title    string
	choices  []copyChoice
	cursor   int
	selected CopyTarget
	canceled bool
}

// NewCopyChoiceModal builds a copy-choice dialog. The Prompt choice is included
// only when promptAvailable is true.
func NewCopyChoiceModal(title string, promptAvailable bool) *CopyChoiceModal {
	choices := []copyChoice{{label: "Name", target: CopyName}}
	if promptAvailable {
		choices = append(choices, copyChoice{label: "Prompt", target: CopyPrompt})
	}
	return &CopyChoiceModal{Box: tview.NewBox(), title: title, choices: choices}
}

// Selected returns the chosen target (CopyNone until Enter is pressed).
func (m *CopyChoiceModal) Selected() CopyTarget { return m.selected }

// Canceled reports whether the dialog was dismissed without a choice.
func (m *CopyChoiceModal) Canceled() bool { return m.canceled }

// Title returns the dialog's title text (test/inspection seam).
func (m *CopyChoiceModal) Title() string { return m.title }

// InputHandler handles key events for the copy-choice dialog.
func (m *CopyChoiceModal) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return m.WrapInputHandler(func(event *tcell.EventKey, _ func(p tview.Primitive)) {
		switch event.Key() {
		case tcell.KeyEnter:
			if m.cursor >= 0 && m.cursor < len(m.choices) {
				m.selected = m.choices[m.cursor].target
			}
		case tcell.KeyEscape, tcell.KeyCtrlQ:
			m.canceled = true
		case tcell.KeyUp:
			m.moveCursor(-1)
		case tcell.KeyDown:
			m.moveCursor(1)
		case tcell.KeyRune:
			switch event.Rune() {
			case 'k':
				m.moveCursor(-1)
			case 'j':
				m.moveCursor(1)
			}
		}
	})
}

// moveCursor shifts the selection cursor, clamped to the choice list.
func (m *CopyChoiceModal) moveCursor(delta int) {
	m.cursor = max(0, min(m.cursor+delta, len(m.choices)-1))
}

// Draw renders the copy-choice dialog centered, covering its full panel rect.
func (m *CopyChoiceModal) Draw(screen tcell.Screen) {
	m.DrawForSubclass(screen, m)
	x, y, width, height := m.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	formW := min(48, width-4)
	if formW < 12 {
		formW = width
	}

	// Fixed chrome: top border, title, blank, [choices...], blank, footer,
	// bottom border = 6 + len(choices). On a terminal too short for the full
	// chrome, clamp the choice rows (floor at 0) so the footer never bleeds onto
	// a choice row — mirrors ConfirmModal's body clamp.
	maxChoices := max(height-6, 0)
	shown := m.choices
	if len(shown) > maxChoices {
		shown = shown[:maxChoices]
	}
	formH := min(6+len(shown), height)
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
	for i, c := range shown {
		style := theme.StyleNormal
		prefix := "  "
		if i == m.cursor {
			style = theme.StyleSelected
			prefix = "❯ "
		}
		widget.DrawText(screen, formX+2, formY+3+i, formW-4, prefix+c.label, style)
	}
	widget.DrawText(screen, formX+2, formY+formH-2, formW-4, "↑/↓ move    Enter = copy    Esc = cancel", theme.StyleDimmed)
}
