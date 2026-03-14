package ui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestDefaultTheme_NonZeroStyles(t *testing.T) {
	theme := DefaultTheme()

	// Verify each style can render without panicking and produces output
	styles := map[string]lipgloss.Style{
		"Title":       theme.Title,
		"StatusBar":   theme.StatusBar,
		"Selected":    theme.Selected,
		"Normal":      theme.Normal,
		"Dimmed":      theme.Dimmed,
		"Pending":     theme.Pending,
		"InProgress":  theme.InProgress,
		"InReview":    theme.InReview,
		"Complete":    theme.Complete,
		"ProjectName": theme.ProjectName,
		"Elapsed":     theme.Elapsed,
		"Help":        theme.Help,
		"Border":      theme.Border,
		"Badge":       theme.Badge,
		"Section":     theme.Section,
		"Divider":     theme.Divider,
		"Error":       theme.Error,
	}

	for name, style := range styles {
		result := style.Render("test")
		if result == "" {
			t.Errorf("DefaultTheme().%s rendered empty string", name)
		}
	}
}
