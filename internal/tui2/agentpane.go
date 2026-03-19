package tui2

import (
	"os"
	"path/filepath"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/drn/argus/internal/app/agentview"
	"github.com/drn/argus/internal/tui2/terminalpane"
	"github.com/drn/argus/internal/uxlog"
)

// AgentPane is the center panel of the agent view. It wraps a terminalpane.Pane
// for native terminal rendering and adds border, placeholder, and diff mode.
type AgentPane struct {
	*tview.Box
	terminal *terminalpane.Pane
	session  agentview.TerminalAdapter
	taskID   string
	focused  bool

	// Diff mode state
	diffMode    bool
	diffContent []diffLine // parsed diff lines for rendering
	diffSplit   bool       // true = side-by-side, false = unified
	diffScroll  int        // scroll offset within diff
	diffFile    string     // file being diffed
}

// diffLine is a single line in the diff view with its type.
type diffLine struct {
	text    string
	lineType diffLineType
}

type diffLineType int

const (
	diffContext diffLineType = iota
	diffAdded
	diffRemoved
	diffHeader
)

// NewAgentPane creates the agent terminal pane with native rendering.
func NewAgentPane() *AgentPane {
	return &AgentPane{
		Box:      tview.NewBox(),
		terminal: terminalpane.New(),
	}
}

// SetSession attaches a session for display.
func (ap *AgentPane) SetSession(sess agentview.TerminalAdapter) {
	ap.session = sess
	if sess != nil {
		ap.terminal.SetSession(sess)
	} else {
		ap.terminal.SetSession(nil)
	}
}

// SetTaskID sets the current task ID.
func (ap *AgentPane) SetTaskID(id string) {
	ap.taskID = id
	// Try to load session log for finished tasks
	if id != "" && ap.session == nil {
		ap.loadSessionLog(id)
	}
}

// loadSessionLog loads the session log file for replay of finished sessions.
func (ap *AgentPane) loadSessionLog(taskID string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	logPath := filepath.Join(home, ".argus", "sessions", taskID+".log")
	data, err := os.ReadFile(logPath)
	if err != nil || len(data) == 0 {
		return
	}
	uxlog.Log("[tui2] loaded session log for %s (%d bytes)", taskID, len(data))
	ap.terminal.SetReplayData(data)
}

// SetFocused sets the focus state for border rendering.
func (ap *AgentPane) SetFocused(f bool) {
	ap.focused = f
}

// ScrollUp scrolls the terminal view up.
func (ap *AgentPane) ScrollUp(n int) {
	if ap.diffMode {
		ap.diffScroll -= n
		if ap.diffScroll < 0 {
			ap.diffScroll = 0
		}
	} else {
		ap.terminal.ScrollUp(n)
	}
}

// ScrollDown scrolls toward the tail.
func (ap *AgentPane) ScrollDown(n int) {
	if ap.diffMode {
		ap.diffScroll += n
	} else {
		ap.terminal.ScrollDown(n)
	}
}

// ScrollToBottom resets scroll to follow tail.
func (ap *AgentPane) ScrollToBottom() {
	ap.terminal.ScrollToBottom()
}

// ScrollOffset returns the current terminal scroll offset.
func (ap *AgentPane) ScrollOffset() int {
	return ap.terminal.ScrollOffset()
}

// EnterDiffMode activates diff display in the center panel.
func (ap *AgentPane) EnterDiffMode(diff, fileName string) {
	ap.diffMode = true
	ap.diffScroll = 0
	ap.diffFile = fileName
	ap.diffContent = parseDiffLines(diff)
}

// ExitDiffMode returns to terminal display.
func (ap *AgentPane) ExitDiffMode() {
	ap.diffMode = false
	ap.diffContent = nil
	ap.diffScroll = 0
	ap.diffFile = ""
}

// InDiffMode returns true if viewing a diff.
func (ap *AgentPane) InDiffMode() bool {
	return ap.diffMode
}

// ToggleDiffSplit switches between side-by-side and unified diff views.
func (ap *AgentPane) ToggleDiffSplit() {
	ap.diffSplit = !ap.diffSplit
	ap.diffScroll = 0
}

// Draw renders the agent pane.
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

	if ap.diffMode {
		ap.renderDiff(screen, x, y, width, height)
		return
	}

	if ap.session == nil && !ap.terminal.HasContent() {
		// No session and no replay — show placeholder
		msg := "No active session"
		if ap.taskID != "" {
			msg = "Session not running - press Enter to start"
		}
		midY := y + height/2
		midX := x + (width-len(msg))/2
		if midX < x {
			midX = x
		}
		drawText(screen, midX, midY, width, msg, StyleDimmed)
		return
	}

	if ap.session != nil && !ap.terminal.HasContent() {
		msg := "Waiting for output..."
		drawText(screen, x+(width-len(msg))/2, y+height/2, width, msg, StyleDimmed)
		return
	}

	// Native terminal rendering — direct vt10x → tcell cell mapping
	ap.terminal.Render(screen, x, y, width, height)
}

// renderDiff renders diff content in the center panel.
func (ap *AgentPane) renderDiff(screen tcell.Screen, x, y, w, h int) {
	if len(ap.diffContent) == 0 {
		msg := "No diff available"
		drawText(screen, x+(w-len(msg))/2, y+h/2, w, msg, StyleDimmed)
		return
	}

	// Header
	headerText := " " + ap.diffFile + " "
	headerStyle := tcell.StyleDefault.Foreground(ColorTitle).Bold(true)
	for i, r := range headerText {
		if i >= w {
			break
		}
		screen.SetContent(x+i, y, r, nil, headerStyle)
	}

	// Clamp scroll
	visibleH := h - 1 // minus header
	maxScroll := len(ap.diffContent) - visibleH
	if maxScroll < 0 {
		maxScroll = 0
	}
	if ap.diffScroll > maxScroll {
		ap.diffScroll = maxScroll
	}

	// Render diff lines
	for i := range visibleH {
		lineIdx := ap.diffScroll + i
		if lineIdx >= len(ap.diffContent) {
			break
		}
		line := ap.diffContent[lineIdx]
		style := diffLineStyle(line.lineType)
		drawText(screen, x, y+1+i, w, line.text, style)
	}
}

// diffLineStyle returns the tcell style for a diff line type.
func diffLineStyle(t diffLineType) tcell.Style {
	switch t {
	case diffAdded:
		return tcell.StyleDefault.Foreground(tcell.PaletteColor(78)) // green
	case diffRemoved:
		return tcell.StyleDefault.Foreground(tcell.PaletteColor(203)) // red
	case diffHeader:
		return tcell.StyleDefault.Foreground(tcell.PaletteColor(87)).Bold(true) // cyan
	default:
		return tcell.StyleDefault.Foreground(tcell.PaletteColor(245)) // gray
	}
}

// parseDiffLines parses raw unified diff text into typed lines.
func parseDiffLines(diff string) []diffLine {
	if diff == "" {
		return nil
	}
	var lines []diffLine
	start := 0
	for i := 0; i <= len(diff); i++ {
		if i == len(diff) || diff[i] == '\n' {
			line := diff[start:i]
			start = i + 1
			var lt diffLineType
			if len(line) > 0 {
				switch line[0] {
				case '+':
					lt = diffAdded
				case '-':
					lt = diffRemoved
				case '@':
					lt = diffHeader
				default:
					lt = diffContext
				}
			}
			lines = append(lines, diffLine{text: line, lineType: lt})
		}
	}
	return lines
}

// drawBorder draws a Unicode box border. Used by AgentPane and SidePanel.
func drawBorder(screen tcell.Screen, x, y, w, h int, style tcell.Style) {
	if w < 2 || h < 2 {
		return
	}
	screen.SetContent(x, y, '\u256d', nil, style)
	screen.SetContent(x+w-1, y, '\u256e', nil, style)
	screen.SetContent(x, y+h-1, '\u2570', nil, style)
	screen.SetContent(x+w-1, y+h-1, '\u256f', nil, style)
	for col := x + 1; col < x+w-1; col++ {
		screen.SetContent(col, y, '\u2500', nil, style)
		screen.SetContent(col, y+h-1, '\u2500', nil, style)
	}
	for row := y + 1; row < y+h-1; row++ {
		screen.SetContent(x, row, '\u2502', nil, style)
		screen.SetContent(x+w-1, row, '\u2502', nil, style)
	}
}
