package ui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/darrencheng/argus/internal/model"
)

// StatusBar renders the bottom status bar.
type StatusBar struct {
	theme  Theme
	width  int
	tasks  []*model.Task
}

func NewStatusBar(theme Theme) StatusBar {
	return StatusBar{theme: theme}
}

func (sb *StatusBar) SetWidth(w int) {
	sb.width = w
}

func (sb *StatusBar) SetTasks(tasks []*model.Task) {
	sb.tasks = tasks
}

func (sb StatusBar) View() string {
	active := 0
	pending := 0
	complete := 0
	for _, t := range sb.tasks {
		switch t.Status {
		case model.StatusInProgress:
			active++
		case model.StatusPending:
			pending++
		case model.StatusComplete:
			complete++
		}
	}

	left := fmt.Sprintf(" %d active  %d pending  %d done", active, pending, complete)
	right := " [n]ew [↵]attach [s]tatus [d]el [?]help [q]uit "

	gap := sb.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 0 {
		gap = 0
	}

	bar := sb.theme.StatusBar.
		Width(sb.width).
		Render(left + fmt.Sprintf("%*s", gap, "") + right)

	return bar
}
