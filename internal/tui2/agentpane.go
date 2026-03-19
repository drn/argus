package tui2

import (
	"regexp"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/drn/argus/internal/app/agentview"
)

// agentOutputTailBytes is the number of bytes to read from the ring buffer
// for the placeholder text display. Matches the PR URL scan tail size.
const agentOutputTailBytes = 32 * 1024

// ansiRe matches ANSI escape sequences (CSI, OSC, simple escapes).
var ansiRe = regexp.MustCompile(`\x1b(?:\[[0-9;]*[a-zA-Z]|\][^\x07]*\x07|\[[^\x1b]*|[()][0-9A-B]|[78DEHM])`)

// AgentPane is a placeholder terminal pane for Phase 2. In Phase 3 this will
// be replaced with a native PTY rendering surface. For now it displays the
// most recent PTY output as text so the agent view is visually functional.
type AgentPane struct {
	*tview.Box
	session agentview.TerminalAdapter
	taskID  string
	focused bool
}

// NewAgentPane creates a placeholder agent terminal pane.
func NewAgentPane() *AgentPane {
	return &AgentPane{
		Box: tview.NewBox(),
	}
}

// SetSession attaches a session for display.
func (ap *AgentPane) SetSession(sess agentview.TerminalAdapter) {
	ap.session = sess
}

// SetTaskID sets the current task ID for session lookup.
func (ap *AgentPane) SetTaskID(id string) {
	ap.taskID = id
}

// SetFocused sets the focus state for border rendering.
func (ap *AgentPane) SetFocused(f bool) {
	ap.focused = f
}

// Draw renders the agent pane. In Phase 2 this shows a placeholder message.
// Phase 3 will replace this with direct PTY output rendering.
func (ap *AgentPane) Draw(screen tcell.Screen) {
	ap.Box.DrawForSubclass(screen, ap)
	x, y, width, height := ap.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	// Draw border
	borderStyle := StyleBorder
	if ap.focused {
		borderStyle = StyleFocusedBorder
	}
	drawBorder(screen, x-1, y-1, width+2, height+2, borderStyle)

	if ap.session == nil {
		// No session — show placeholder
		msg := "No active session"
		if ap.taskID != "" {
			msg = "Session not running — press Enter to start"
		}
		midY := y + height/2
		midX := x + (width-len(msg))/2
		if midX < x {
			midX = x
		}
		drawText(screen, midX, midY, width, msg, StyleDimmed)
		return
	}

	// Phase 2: show tail of PTY output as plain text.
	// Phase 3 will replace this with native terminal rendering.
	tail := ap.session.RecentOutputTail(agentOutputTailBytes)
	if len(tail) == 0 {
		msg := "Waiting for output..."
		drawText(screen, x+(width-len(msg))/2, y+height/2, width, msg, StyleDimmed)
		return
	}

	// Split into lines and show the last `height` lines
	lines := splitLines(tail, width)
	startLine := 0
	if len(lines) > height {
		startLine = len(lines) - height
	}
	for i := startLine; i < len(lines) && (i-startLine) < height; i++ {
		drawText(screen, x, y+(i-startLine), width, lines[i], StyleNormal)
	}
}

// splitLines strips ANSI escape sequences, then splits the result into
// display lines, wrapping at maxWidth.
func splitLines(data []byte, maxWidth int) []string {
	if maxWidth <= 0 {
		maxWidth = 80
	}
	// Strip ANSI escapes so control sequences don't render as garbage glyphs.
	clean := ansiRe.ReplaceAll(data, nil)

	var lines []string
	var current []rune
	for _, b := range clean {
		switch b {
		case '\n':
			lines = append(lines, string(current))
			current = current[:0]
		case '\r', '\x1b':
			// skip leftover escape chars and carriage returns
		default:
			if b < 0x20 {
				continue // skip other control characters
			}
			current = append(current, rune(b))
			if len(current) >= maxWidth {
				lines = append(lines, string(current))
				current = current[:0]
			}
		}
	}
	if len(current) > 0 {
		lines = append(lines, string(current))
	}
	return lines
}

// drawBorder draws a Unicode box border.
func drawBorder(screen tcell.Screen, x, y, w, h int, style tcell.Style) {
	if w < 2 || h < 2 {
		return
	}
	// Corners
	screen.SetContent(x, y, '╭', nil, style)
	screen.SetContent(x+w-1, y, '╮', nil, style)
	screen.SetContent(x, y+h-1, '╰', nil, style)
	screen.SetContent(x+w-1, y+h-1, '╯', nil, style)
	// Top and bottom
	for col := x + 1; col < x+w-1; col++ {
		screen.SetContent(col, y, '─', nil, style)
		screen.SetContent(col, y+h-1, '─', nil, style)
	}
	// Sides
	for row := y + 1; row < y+h-1; row++ {
		screen.SetContent(x, row, '│', nil, style)
		screen.SetContent(x+w-1, row, '│', nil, style)
	}
}
