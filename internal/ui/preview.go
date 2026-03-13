package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
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

	// Use a virtual terminal to interpret PTY output (cursor movements,
	// screen clears, etc.) into a proper screen buffer.
	cols := max(p.width-4, 20)
	rows := max(p.height-4, 5)
	vt := vt10x.New(vt10x.WithSize(cols, rows))
	vt.Write(raw)

	// Read the screen content from the virtual terminal
	vt.Lock()
	defer vt.Unlock()

	var lines []string
	for y := 0; y < rows; y++ {
		var line strings.Builder
		for x := 0; x < cols; x++ {
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

	return strings.Join(lines, "\n")
}
