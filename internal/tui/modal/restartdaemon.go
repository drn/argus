package modal

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
)

// RestartDaemonModal prompts the user to restart the daemon when its binary
// is older than the TUI's. Two buttons: Restart and Skip. Tab/← → switches
// the selection; Enter activates the focused button; Esc skips.
type RestartDaemonModal struct {
	*tview.Box
	selected int // 0 = Restart, 1 = Skip
	chose    int // -1 unset; 0 restart; 1 skip
}

// NewRestartDaemonModal creates the modal with Restart selected by default.
func NewRestartDaemonModal() *RestartDaemonModal {
	return &RestartDaemonModal{
		Box:   tview.NewBox(),
		chose: -1,
	}
}

// ChoseRestart returns true if the user picked Restart.
func (m *RestartDaemonModal) ChoseRestart() bool { return m.chose == 0 }

// ChoseSkip returns true if the user picked Skip (or pressed Esc).
func (m *RestartDaemonModal) ChoseSkip() bool { return m.chose == 1 }

// Done returns true once the user has made a choice.
func (m *RestartDaemonModal) Done() bool { return m.chose != -1 }

// Selected returns the currently focused button index (for tests).
func (m *RestartDaemonModal) Selected() int { return m.selected }

// InputHandler handles key events for the restart modal.
func (m *RestartDaemonModal) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return m.WrapInputHandler(func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		switch event.Key() {
		case tcell.KeyTab, tcell.KeyRight, tcell.KeyBacktab, tcell.KeyLeft:
			m.selected = 1 - m.selected
		case tcell.KeyEnter:
			m.chose = m.selected
		case tcell.KeyEscape:
			m.chose = 1 // skip
		case tcell.KeyRune:
			switch event.Rune() {
			case 'r', 'R':
				m.chose = 0
			case 's', 'S':
				m.chose = 1
			case 'h':
				m.selected = 0
			case 'l':
				m.selected = 1
			}
		}
	})
}

// Draw renders the restart confirmation modal as a centered dialog.
func (m *RestartDaemonModal) Draw(screen tcell.Screen) {
	m.DrawForSubclass(screen, m)
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

	widget.DrawBorder(screen, formX, formY, formW, formH, theme.StyleFocusedBorder)
	widget.DrawText(screen, formX+2, formY+1, formW-4, "Daemon out of date", theme.StyleTitle)
	widget.DrawText(screen, formX+4, formY+3, formW-6, "The argus binary has been updated since the", theme.StyleNormal)
	widget.DrawText(screen, formX+4, formY+4, formW-6, "daemon started. Restart it to load the new code?", theme.StyleNormal)

	// Two buttons centered on the bottom row of the body.
	btnRestart := "[ Restart ]"
	btnSkip := "[ Skip ]"
	totalW := len(btnRestart) + 2 + len(btnSkip)
	startX := formX + (formW-totalW)/2
	btnRow := formY + formH - 2

	restartStyle := theme.StyleNormal
	skipStyle := theme.StyleNormal
	if m.selected == 0 {
		restartStyle = theme.StyleSelected
	} else {
		skipStyle = theme.StyleSelected
	}
	widget.DrawText(screen, startX, btnRow, len(btnRestart), btnRestart, restartStyle)
	widget.DrawText(screen, startX+len(btnRestart)+2, btnRow, len(btnSkip), btnSkip, skipStyle)
}
