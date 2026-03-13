package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var bannerLines = [...]string{
	` █████  ██████   ██████  ██    ██ ███████`,
	`██   ██ ██   ██ ██       ██    ██ ██     `,
	`███████ ██████  ██   ███ ██    ██ ███████`,
	`██   ██ ██   ██ ██    ██ ██    ██      ██`,
	`██   ██ ██   ██  ██████   ██████  ███████`,
}

var bannerGradient = [...]lipgloss.Color{
	lipgloss.Color("87"),  // bright cyan
	lipgloss.Color("81"),  // light blue
	lipgloss.Color("141"), // lavender
	lipgloss.Color("177"), // light purple
	lipgloss.Color("212"), // pink
}

const bannerWidth = 41

func renderBanner(width int) string {
	if width < bannerWidth+4 {
		// Terminal too narrow for banner — use compact title
		title := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("87")).
			Render("ARGUS")
		sub := lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Render("CODE ORCHESTRATOR")
		block := lipgloss.JoinVertical(lipgloss.Center, title, sub)
		return lipgloss.PlaceHorizontal(width, lipgloss.Center, block)
	}

	var b strings.Builder
	for i, line := range bannerLines {
		styled := lipgloss.NewStyle().Foreground(bannerGradient[i]).Render(line)
		centered := lipgloss.PlaceHorizontal(width, lipgloss.Center, styled)
		b.WriteString(centered)
		if i < len(bannerLines)-1 {
			b.WriteByte('\n')
		}
	}

	sub := lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")).
		Render("C O D E   O R C H E S T R A T O R")
	b.WriteByte('\n')
	b.WriteString(lipgloss.PlaceHorizontal(width, lipgloss.Center, sub))

	return b.String()
}
