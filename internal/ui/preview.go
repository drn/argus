package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/drn/argus/internal/agent"
	"github.com/hinshun/vt10x"
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
	content := p.formatOutput(raw)
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

func (p Preview) formatOutput(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}

	// The PTY output was generated for the full terminal width, so we
	// interpret it with a wide virtual terminal, then crop to fit.
	vtCols := 200
	vtRows := 500
	vt := vt10x.New(vt10x.WithSize(vtCols, vtRows))
	vt.Write(raw)

	// Read the screen content from the virtual terminal
	vt.Lock()
	defer vt.Unlock()

	var lines []string
	for y := 0; y < vtRows; y++ {
		var line strings.Builder
		for x := 0; x < vtCols; x++ {
			cell := vt.Cell(x, y)
			if cell.Char == 0 {
				line.WriteByte(' ')
			} else {
				line.WriteRune(cell.Char)
			}
		}
		lines = append(lines, strings.TrimRight(line.String(), " "))
	}

	// Trim trailing empty lines
	for len(lines) > 0 && lines[len(lines)-1] == "" {
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
		lines[i] = ansi.Truncate(line, dispW, "")
	}

	return strings.Join(lines, "\n")
}
