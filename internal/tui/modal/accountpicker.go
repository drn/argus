package modal

import (
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// AccountChoice is one target account offered by the AccountPickerModal.
// Detail is shown after the name (e.g. its usage).
type AccountChoice struct {
	Name   string
	Detail string
}

// AccountPickerModal asks which account to move a task to. An optional warning
// line (e.g. "the running agent will be interrupted") sits above the choices.
// Up/Down (or j/k) move the cursor; Enter confirms; Esc / Ctrl+Q cancels. It
// carries no text input (so no PasteHandler is needed).
type AccountPickerModal struct {
	*tview.Box
	title    string
	warning  string
	choices  []AccountChoice
	cursor   int
	selected string
	canceled bool
}

// NewAccountPickerModal builds an account picker over choices.
func NewAccountPickerModal(title, warning string, choices []AccountChoice) *AccountPickerModal {
	return &AccountPickerModal{Box: tview.NewBox(), title: title, warning: warning, choices: choices}
}

// Selected returns the chosen account name ("" until Enter is pressed).
func (m *AccountPickerModal) Selected() string { return m.selected }

// Canceled reports whether the dialog was dismissed without a choice.
func (m *AccountPickerModal) Canceled() bool { return m.canceled }

// Title returns the dialog's title text (test/inspection seam).
func (m *AccountPickerModal) Title() string { return m.title }

// InputHandler handles key events for the picker.
func (m *AccountPickerModal) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return m.WrapInputHandler(func(event *tcell.EventKey, _ func(p tview.Primitive)) {
		switch event.Key() {
		case tcell.KeyEnter:
			if m.cursor >= 0 && m.cursor < len(m.choices) {
				m.selected = m.choices[m.cursor].Name
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

func (m *AccountPickerModal) moveCursor(delta int) {
	m.cursor = max(0, min(m.cursor+delta, len(m.choices)-1))
}

// Draw renders the picker centered, covering its full panel rect.
func (m *AccountPickerModal) Draw(screen tcell.Screen) {
	m.DrawForSubclass(screen, m)
	x, y, width, height := m.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}
	formW := min(64, width-4)
	if formW < 12 {
		formW = width
	}
	// Chrome: top border, title, blank, [warning, blank,] choices, blank,
	// footer, bottom border. Clamp the choices on a short terminal so the
	// footer never overlaps them.
	chrome := 6
	if m.warning != "" {
		chrome += 2
	}
	shown := m.choices
	if maxChoices := max(height-chrome, 0); len(shown) > maxChoices {
		shown = shown[:maxChoices]
	}
	formH := min(chrome+len(shown), height)
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
	row := formY + 3
	if m.warning != "" {
		widget.DrawText(screen, formX+2, row, formW-4, m.warning, theme.StyleError)
		row += 2
	}
	for i, c := range shown {
		style := theme.StyleNormal
		prefix := "  "
		if i == m.cursor {
			style = theme.StyleSelected
			prefix = "❯ "
		}
		label := c.Name
		if c.Detail != "" {
			label += "  " + c.Detail
		}
		widget.DrawText(screen, formX+2, row+i, formW-4, prefix+label, style)
	}
	widget.DrawText(screen, formX+2, formY+formH-2, formW-4, "↑/↓ move    Enter = move    Esc = cancel", theme.StyleDimmed)
}
