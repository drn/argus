package tui

import (
	"strconv"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/drn/argus/internal/uxlog"
)

func (sv *SettingsView) toggleProjectFirst() {
	next := !sv.projectFirst
	if err := sv.database.SetConfigValue("ui.project_first", strconv.FormatBool(next)); err != nil {
		uxlog.Log("[settings] project-first persist failed: %v", err)
		if sv.remote {
			// Match sibling Appearance toggles: cosmetic until the next refresh.
			sv.projectFirst = next
			sv.rebuildRows()
		} else {
			sv.projectFirstError = "Could not save: " + err.Error()
		}
		return
	}
	sv.projectFirst = sv.database.Config().UI.ProjectFirst
	sv.projectFirstError = ""
	uxlog.Log("[settings] project-first set to %t (effective %t)", next, sv.projectFirst)
	sv.rebuildRows()
}

func (sv *SettingsView) renderProjectFirstDetail(screen tcell.Screen, x, y, w, h int) {
	lines := []string{"Project first", "", "Ask for a project before new tasks and coordinators",
		"when multiple projects exist. Recent projects appear first.",
		"Enter continues to the prompt; Esc returns to projects.",
		"Worker spawn keeps its existing flow.", "", "[enter/▶] toggle  [◀] rail"}
	if sv.projectFirstError != "" {
		lines = append(lines, sv.projectFirstError)
	}
	for i, line := range lines {
		if i >= h {
			break
		}
		style := theme.StyleDimmed
		if i == 0 {
			style = theme.StyleTitle
		}
		if line == sv.projectFirstError {
			style = tcell.StyleDefault.Foreground(theme.ColorError)
		}
		widget.DrawText(screen, x, y+i, w, line, style)
	}
}
