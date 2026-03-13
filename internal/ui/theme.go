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
}

func DefaultTheme() Theme {
	return Theme{
		Title: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("99")).
			Padding(0, 1),
		StatusBar: lipgloss.NewStyle().
			Background(lipgloss.Color("236")).
			Foreground(lipgloss.Color("252")).
			Padding(0, 1),
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
			Foreground(lipgloss.Color("117")),
		Complete: lipgloss.NewStyle().
			Foreground(lipgloss.Color("78")),
		ProjectName: lipgloss.NewStyle().
			Foreground(lipgloss.Color("243")),
		Elapsed: lipgloss.NewStyle().
			Foreground(lipgloss.Color("243")),
		Help: lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")),
		Border: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("238")),
		Badge: lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")),
	}
}
