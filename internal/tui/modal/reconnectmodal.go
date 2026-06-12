package modal

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
)

// ReconnectModal is the app-level "reconnecting…" screen shown when a plugin
// view's WebSocket drops (e.g. the plugin daemon restarted). It is purely
// informational — it has NO InputHandler, because while reconnecting the App's
// modePluginView key routing owns the keyboard (Esc / double-Ctrl+Q exit; every
// other key is dropped, there being no live plugin to forward to). The message
// is mutable so the App can flip it from "Reconnecting…" to a "still trying…"
// hint after a prolonged outage. Touched only on the tview goroutine.
type ReconnectModal struct {
	*tview.Box
	title   string
	message string
}

// NewReconnectModal creates a reconnect overlay for the given plugin title with
// an initial message.
func NewReconnectModal(title, message string) *ReconnectModal {
	return &ReconnectModal{Box: tview.NewBox(), title: title, message: message}
}

// SetMessage updates the body line (e.g. attempt count or the prolonged-outage
// hint). Call on the tview goroutine.
func (m *ReconnectModal) SetMessage(msg string) { m.message = msg }

// Message returns the current body line (for tests).
func (m *ReconnectModal) Message() string { return m.message }

// Title returns the plugin title (for tests).
func (m *ReconnectModal) Title() string { return m.title }

// Draw renders a centered dialog with an in-progress accent border. Mirrors
// ErrorModal's centered-dialog layout so the reconnect screen looks at home
// among argus's other modals.
func (m *ReconnectModal) Draw(screen tcell.Screen) {
	m.DrawForSubclass(screen, m)
	x, y, width, height := m.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	formW := min(60, width-4)
	if formW < 12 {
		formW = width
	}
	body := wrapErrorBody(m.message, formW-6)
	maxBody := height - 6
	if maxBody < 1 {
		maxBody = 1
	}
	if len(body) > maxBody {
		body = body[:maxBody]
	}
	formH := 6 + len(body)
	if formH > height {
		formH = height
	}
	formX := x + (width-formW)/2
	formY := y + (height-formH)/2
	if formY < y {
		formY = y
	}

	// Clear the dialog area so the frozen plugin frame doesn't bleed through.
	for row := formY; row < formY+formH && row < y+height; row++ {
		for col := formX; col < formX+formW && col < x+width; col++ {
			screen.SetContent(col, row, ' ', nil, tcell.StyleDefault)
		}
	}

	widget.DrawBorder(screen, formX, formY, formW, formH, theme.StyleInProgress)

	title := m.title
	if title == "" {
		title = "Plugin"
	}
	widget.DrawText(screen, formX+2, formY+1, formW-4, title, theme.StyleTitle)
	for i, line := range body {
		widget.DrawText(screen, formX+3, formY+3+i, formW-6, line, theme.StyleNormal)
	}

	hint := "[ Esc or Ctrl+Q twice to exit ]"
	hintX := formX + (formW-len(hint))/2
	if hintX < formX+2 {
		hintX = formX + 2
	}
	widget.DrawText(screen, hintX, formY+formH-2, formW-4, hint, theme.StyleDimmed)
}
