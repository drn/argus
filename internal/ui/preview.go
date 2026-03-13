package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/drn/argus/internal/agent"
	"github.com/hinshun/vt10x"
)

// vt10x attribute bit flags (unexported in the library)
const (
	vtAttrReverse   = 1 << 0
	vtAttrUnderline = 1 << 1
	vtAttrBold      = 1 << 2
	// attrGfx      = 1 << 3
	vtAttrItalic = 1 << 4
	// attrBlink    = 1 << 5
)

// Preview renders the agent output for the selected task.
type Preview struct {
	theme  Theme
	runner *agent.Runner
	width  int
	height int
}

func NewPreview(theme Theme, runner *agent.Runner) Preview {
	return Preview{theme: theme, runner: runner}
}

func (p *Preview) SetSize(w, h int) {
	p.width = w
	p.height = h
}

func (p Preview) View(taskID string) string {
	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("238")).
		Width(p.width - 2).
		Height(p.height - 2)

	if taskID == "" {
		return border.Render(p.emptyView("No task selected"))
	}

	sess := p.runner.Get(taskID)
	if sess == nil {
		return border.Render(p.emptyView("No active agent\n\n" +
			p.theme.Help.Render("Press ENTER to start")))
	}

	raw := sess.RecentOutput()
	ptyCols, ptyRows := sess.PTYSize()
	content := p.formatOutput(raw, ptyCols, ptyRows)
	return border.Render(content)
}

func (p Preview) emptyView(msg string) string {
	style := p.theme.Dimmed.
		Width(p.width - 4).
		Height(p.height - 4).
		AlignHorizontal(lipgloss.Center).
		AlignVertical(lipgloss.Center)
	return style.Render(msg)
}

func (p Preview) formatOutput(raw []byte, ptyCols, ptyRows int) string {
	if len(raw) == 0 {
		return ""
	}

	// Use the actual PTY dimensions so the vt10x interpretation matches
	// what the child process was rendering for. Fall back to wide defaults
	// if dimensions are unknown.
	vtCols := ptyCols
	vtRows := ptyRows
	if vtCols < 40 {
		vtCols = 200
	}
	if vtRows < 10 {
		vtRows = 500
	}
	vt := vt10x.New(vt10x.WithSize(vtCols, vtRows))
	vt.Write(raw)

	// Read the screen content from the virtual terminal with color info
	vt.Lock()
	defer vt.Unlock()

	var lines []string
	for y := 0; y < vtRows; y++ {
		line := renderLine(vt, y, vtCols, -1)
		lines = append(lines, line)
	}

	// Trim trailing empty lines (check stripped version for emptiness)
	for len(lines) > 0 && stripANSI(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	if len(lines) == 0 {
		return ""
	}

	// Available display area inside the border
	dispW := max(p.width-4, 10)
	dispH := max(p.height-4, 3)

	// Take the tail (most recent output) and truncate lines to fit
	if len(lines) > dispH {
		lines = lines[len(lines)-dispH:]
	}
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, dispW, "\x1b[0m")
	}

	return strings.Join(lines, "\n")
}

// renderLine builds a single line from the vt10x screen with ANSI colors.
// cursorX is the column to render a cursor at (-1 for no cursor on this line).
func renderLine(vt vt10x.Terminal, y, cols int, cursorX int) string {
	var b strings.Builder
	var curFG, curBG vt10x.Color
	var curMode int16
	active := false // whether we have an active SGR state

	// Find the last non-empty column to trim trailing spaces
	// (extend to cursor position if cursor is on this line)
	lastCol := -1
	for x := cols - 1; x >= 0; x-- {
		cell := vt.Cell(x, y)
		ch := cell.Char
		if ch == 0 {
			ch = ' '
		}
		if ch != ' ' || cell.FG != vt10x.DefaultFG || cell.BG != vt10x.DefaultBG || cell.Mode != 0 {
			lastCol = x
			break
		}
	}
	if cursorX > lastCol {
		lastCol = cursorX
	}

	for x := 0; x <= lastCol; x++ {
		cell := vt.Cell(x, y)
		ch := cell.Char
		if ch == 0 {
			ch = ' '
		}

		if x == cursorX {
			// Render cursor cell with reverse video
			b.WriteString("\x1b[7m")
			b.WriteRune(ch)
			b.WriteString("\x1b[27m")
			// Force SGR re-emit on next cell
			active = false
		} else {
			// Emit SGR sequence if attributes changed
			if cell.FG != curFG || cell.BG != curBG || cell.Mode != curMode || !active {
				b.WriteString(buildSGR(cell.FG, cell.BG, cell.Mode))
				curFG = cell.FG
				curBG = cell.BG
				curMode = cell.Mode
				active = true
			}
			b.WriteRune(ch)
		}
	}

	// Reset at end of line if we emitted any SGR
	if active {
		b.WriteString("\x1b[0m")
	}

	return b.String()
}

// buildSGR builds an ANSI SGR escape sequence for the given attributes.
func buildSGR(fg, bg vt10x.Color, mode int16) string {
	var params []string

	// Reset first, then apply attributes
	params = append(params, "0")

	if mode&vtAttrBold != 0 {
		params = append(params, "1")
	}
	if mode&vtAttrItalic != 0 {
		params = append(params, "3")
	}
	if mode&vtAttrUnderline != 0 {
		params = append(params, "4")
	}
	if mode&vtAttrReverse != 0 {
		params = append(params, "7")
	}

	if fg != vt10x.DefaultFG {
		params = append(params, fgColor(fg))
	}
	if bg != vt10x.DefaultBG {
		params = append(params, bgColor(bg))
	}

	return "\x1b[" + strings.Join(params, ";") + "m"
}

// fgColor returns the SGR parameter string for a foreground color.
func fgColor(c vt10x.Color) string {
	n := uint32(c)
	switch {
	case n < 8:
		return fmt.Sprintf("%d", 30+n)
	case n < 16:
		return fmt.Sprintf("%d", 90+n-8)
	case n < 256:
		return fmt.Sprintf("38;5;%d", n)
	default:
		// vt10x stores RGB as r<<16 | g<<8 | b
		r, g, b := (n>>16)&0xFF, (n>>8)&0xFF, n&0xFF
		return fmt.Sprintf("38;2;%d;%d;%d", r, g, b)
	}
}

// bgColor returns the SGR parameter string for a background color.
func bgColor(c vt10x.Color) string {
	n := uint32(c)
	switch {
	case n < 8:
		return fmt.Sprintf("%d", 40+n)
	case n < 16:
		return fmt.Sprintf("%d", 100+n-8)
	case n < 256:
		return fmt.Sprintf("48;5;%d", n)
	default:
		// vt10x stores RGB as r<<16 | g<<8 | b
		r, g, b := (n>>16)&0xFF, (n>>8)&0xFF, n&0xFF
		return fmt.Sprintf("48;2;%d;%d;%d", r, g, b)
	}
}

// stripANSI removes ANSI escape sequences and trims whitespace for emptiness check.
func stripANSI(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			// Skip CSI sequence
			j := i + 2
			for j < len(s) && !((s[j] >= 'A' && s[j] <= 'Z') || (s[j] >= 'a' && s[j] <= 'z')) {
				j++
			}
			if j < len(s) {
				j++ // skip final byte
			}
			i = j
		} else {
			b.WriteByte(s[i])
			i++
		}
	}
	return strings.TrimSpace(b.String())
}
