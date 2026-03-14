package ui

import "github.com/charmbracelet/lipgloss"

// Theme defines the color scheme for the UI.
type Theme struct {
	Title       lipgloss.Style
	StatusBar   lipgloss.Style
	Selected    lipgloss.Style
	Normal      lipgloss.Style
	Dimmed      lipgloss.Style
	Pending     lipgloss.Style
	InProgress  lipgloss.Style
	InReview    lipgloss.Style
	Complete    lipgloss.Style
	ProjectName lipgloss.Style
	Elapsed     lipgloss.Style
	Help        lipgloss.Style
	Border      lipgloss.Style
	Badge       lipgloss.Style
	Section     lipgloss.Style
	Divider     lipgloss.Style
	Error       lipgloss.Style
}

// clampModalWidth computes a modal width from the total terminal width,
// clamping to a reasonable range (50–80, constrained to width-4).
func clampModalWidth(totalWidth int) int {
	w := totalWidth * 2 / 5
	if w < 50 {
		w = 50
	}
	if w > 80 {
		w = 80
	}
	if w > totalWidth-4 {
		w = totalWidth - 4
	}
	return w
}

// toStringSet converts a string slice to a lookup map.
func toStringSet(ids []string) map[string]bool {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// borderedPanel creates a rounded-border panel with focus-aware border color.
func borderedPanel(w, h int, focused bool, content string) string {
	borderColor := "238"
	if focused {
		borderColor = "87"
	}
	innerH := max(h-2, 1)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(borderColor)).
		Width(w - 2).
		Height(innerH).
		Render(content)
}

func DefaultTheme() Theme {
	return Theme{
		Title: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("87")),
		StatusBar: lipgloss.NewStyle().
			Background(lipgloss.Color("235")).
			Foreground(lipgloss.Color("245")),
		Selected: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("212")),
		Normal: lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")),
		Dimmed: lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")),
		Pending: lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")),
		InProgress: lipgloss.NewStyle().
			Foreground(lipgloss.Color("214")),
		InReview: lipgloss.NewStyle().
			Foreground(lipgloss.Color("81")),
		Complete: lipgloss.NewStyle().
			Foreground(lipgloss.Color("78")),
		ProjectName: lipgloss.NewStyle().
			Foreground(lipgloss.Color("87")),
		Elapsed: lipgloss.NewStyle().
			Foreground(lipgloss.Color("243")),
		Help: lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")),
		Border: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("238")),
		Badge: lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")),
		Section: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("245")),
		Divider: lipgloss.NewStyle().
			Foreground(lipgloss.Color("236")),
		Error: lipgloss.NewStyle().
			Foreground(lipgloss.Color("203")),
	}
}
