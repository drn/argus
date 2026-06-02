package modal

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
)

// ErrorModal is a dismiss-only modal that surfaces a failed action prominently.
// It exists because the status bar (a single, easily-missed bottom row that
// truncates long text) is the wrong surface for a hard failure of an explicit
// user action — e.g. "create agent" failing leaves the user staring at a
// closed form with no visible reason. Any key dismisses it.
type ErrorModal struct {
	*tview.Box
	title  string
	body   string
	closed bool
}

// NewErrorModal creates an error modal with the given title and body. An empty
// title falls back to "Error".
func NewErrorModal(title, body string) *ErrorModal {
	return &ErrorModal{Box: tview.NewBox(), title: title, body: body}
}

// Closed reports whether the user has dismissed the modal.
func (m *ErrorModal) Closed() bool { return m.closed }

// Title returns the modal title (for tests).
func (m *ErrorModal) Title() string { return m.title }

// Body returns the modal body (for tests).
func (m *ErrorModal) Body() string { return m.body }

// InputHandler dismisses the modal on any key press.
func (m *ErrorModal) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return m.WrapInputHandler(func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		m.closed = true
	})
}

// wrapErrorBody word-wraps text to maxWidth columns. Long unsplittable tokens
// (e.g. file paths) are hard-broken so they never overflow the border.
func wrapErrorBody(text string, maxWidth int) []string {
	if maxWidth <= 0 {
		return nil
	}
	var lines []string
	for _, field := range strings.Fields(text) {
		// Hard-break tokens longer than the line width.
		for len(field) > maxWidth {
			lines = append(lines, field[:maxWidth])
			field = field[maxWidth:]
		}
		if len(lines) == 0 {
			lines = append(lines, field)
			continue
		}
		last := lines[len(lines)-1]
		if len(last)+1+len(field) > maxWidth {
			lines = append(lines, field)
		} else {
			lines[len(lines)-1] = last + " " + field
		}
	}
	return lines
}

// Draw renders the error modal as a centered dialog with a red border.
func (m *ErrorModal) Draw(screen tcell.Screen) {
	m.DrawForSubclass(screen, m)
	x, y, width, height := m.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	formW := min(64, width-4)
	if formW < 12 {
		formW = width
	}
	lines := wrapErrorBody(m.body, formW-6)
	// Cap body lines so a pathological message can't exceed the screen.
	maxBody := height - 6
	if maxBody < 1 {
		maxBody = 1
	}
	if len(lines) > maxBody {
		lines = lines[:maxBody]
	}
	formH := 6 + len(lines)
	if formH > height {
		formH = height
	}
	formX := x + (width-formW)/2
	formY := y + (height-formH)/2
	if formY < y {
		formY = y
	}

	// Clear the modal area so underlying content doesn't bleed through.
	for row := formY; row < formY+formH && row < y+height; row++ {
		for col := formX; col < formX+formW && col < x+width; col++ {
			screen.SetContent(col, row, ' ', nil, tcell.StyleDefault)
		}
	}

	widget.DrawBorder(screen, formX, formY, formW, formH, theme.StyleError)

	title := m.title
	if title == "" {
		title = "Error"
	}
	widget.DrawText(screen, formX+2, formY+1, formW-4, title, theme.StyleError)
	for i, line := range lines {
		widget.DrawText(screen, formX+3, formY+3+i, formW-6, line, theme.StyleNormal)
	}

	dismiss := "[ press any key to dismiss ]"
	dismissX := formX + (formW-len(dismiss))/2
	if dismissX < formX+2 {
		dismissX = formX + 2
	}
	widget.DrawText(screen, dismissX, formY+formH-2, formW-4, dismiss, theme.StyleDimmed)
}
