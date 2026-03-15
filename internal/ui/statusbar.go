package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/drn/argus/internal/model"
)

// StatusBar renders the bottom status bar.
type StatusBar struct {
	theme      Theme
	width      int
	tasks      []*model.Task
	running    map[string]bool
	errMsg      string
	settingsTab bool
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

func (sb *StatusBar) SetRunning(ids []string) {
	sb.running = toStringSet(ids)
}

func (sb *StatusBar) SetSettingsTab(active bool) {
	sb.settingsTab = active
}

func (sb *StatusBar) SetError(msg string) {
	sb.errMsg = msg
}

func (sb *StatusBar) ClearError() {
	sb.errMsg = ""
}

func (sb StatusBar) View() string {
	var left string
	if sb.errMsg != "" {
		left = " " + sb.theme.Error.Render("! "+sb.errMsg)
	} else {
		active := 0
		pending := 0
		complete := 0
		for _, t := range sb.tasks {
			switch t.Status {
			case model.StatusInProgress:
				if sb.running[t.ID] {
					active++
				}
			case model.StatusPending:
				pending++
			case model.StatusComplete:
				complete++
			}
		}
		left = fmt.Sprintf(" %d active  %d pending  %d done", active, pending, complete)
	}

	// Keybinding hints with highlighted keys
	var keys []struct{ key, label string }
	if sb.settingsTab {
		keys = []struct{ key, label string }{
			{"n", "new project"},
			{"d", "del"},
			{"1", "tasks"},
			{"?", "help"},
			{"q", "quit"},
		}
	} else {
		keys = []struct{ key, label string }{
			{"n", "new"},
			{"RET", "attach"},
			{"s", "status"},
			{"d", "del"},
			{"2", "settings"},
			{"?", "help"},
			{"q", "quit"},
		}
	}
	var parts []string
	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("87"))
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	for _, k := range keys {
		parts = append(parts, keyStyle.Render(k.key)+labelStyle.Render(" "+k.label))
	}
	right := strings.Join(parts, "  ") + " "

	gap := sb.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 0 {
		gap = 0
	}

	bar := sb.theme.StatusBar.
		Width(sb.width).
		Render(left + fmt.Sprintf("%*s", gap, "") + right)

	return bar
}
