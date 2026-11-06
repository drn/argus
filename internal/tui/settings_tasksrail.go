package tui

import (
	"strconv"

	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/drn/argus/internal/uxlog"
	"github.com/gdamore/tcell/v2"
)

func (sv *SettingsView) toggleTasksRail() {
	next := !sv.tasksRail
	if err := sv.database.SetConfigValue("ui.tasks_rail", strconv.FormatBool(next)); err != nil {
		sv.tasksRailError = "Could not save: " + err.Error()
		uxlog.Log("[tasks-rail] preference persist failed: %v", err)
		return
	}
	sv.tasksRail = sv.database.Config().UI.TasksRail
	sv.tasksRailError = ""
	sv.rebuildRows()
	uxlog.Log("[tasks-rail] preference enabled=%v", sv.tasksRail)
	if sv.OnTasksRailToggle != nil {
		sv.OnTasksRailToggle(sv.tasksRail)
	}
}

func (sv *SettingsView) renderTasksRailDetail(screen tcell.Screen, x, y, w, h int) {
	lines := []string{"Persistent task rail", "", "Keep project tasks visible beside the selected agent.", "Browse without starting sessions; Enter opens the agent.", "Ctrl+Q returns to the rail. Applies immediately.", "", "[enter/▶] toggle  [◀] rail"}
	if sv.tasksRailError != "" {
		lines = append(lines, sv.tasksRailError)
	}
	for i, line := range lines {
		if i >= h {
			break
		}
		style := theme.StyleDimmed
		if i == 0 {
			style = theme.StyleTitle
		}
		widget.DrawText(screen, x, y+i, w, line, style)
	}
}
