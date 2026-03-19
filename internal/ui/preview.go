package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/drn/argus/internal/agent"
)

// Preview renders the agent output for the selected task.
type Preview struct {
	theme  Theme
	runner agent.SessionProvider
	width  int
	height int
}

func NewPreview(theme Theme, runner agent.SessionProvider) Preview {
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

	vtCols := ptyCols
	vtRows := ptyRows
	if vtCols < 40 {
		vtCols = 200
	}
	if vtRows < 10 {
		vtRows = 500
	}

	lines := ReplayVT10X(raw, vtCols, vtRows, false)
	if len(lines) == 0 {
		return ""
	}

	dispW := max(p.width-4, 10)
	dispH := max(p.height-4, 3)

	if len(lines) > dispH {
		lines = lines[len(lines)-dispH:]
	}
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, dispW, "\x1b[0m")
	}

	return strings.Join(lines, "\n")
}
