package ui

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/drn/argus/internal/agent"
)

// ansiRe matches ANSI escape sequences (CSI, OSC, etc.)
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\][^\x1b]*(?:\x1b\\|\x07)|\x1b[()][0-9A-B]|\x1b\[[\?]?[0-9;]*[hlm]`)

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
	// Strip ANSI escape sequences for clean preview
	cleaned := ansiRe.ReplaceAll(raw, nil)

	// Remove carriage returns
	cleaned = bytes.ReplaceAll(cleaned, []byte("\r"), nil)

	// Split into lines and take the last `height` lines
	lines := strings.Split(string(cleaned), "\n")

	maxLines := max(p.height-4, 1)

	// Take last N lines
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}

	// Truncate long lines to fit width
	maxWidth := max(p.width-6, 10)
	for i, line := range lines {
		if len(line) > maxWidth {
			lines[i] = line[:maxWidth-1] + "…"
		}
	}

	return strings.Join(lines, "\n")
}
