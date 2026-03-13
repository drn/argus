package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/drn/argus/internal/config"
)

// ProjectList renders the project list view.
type ProjectList struct {
	projects []projectEntry
	cursor   int
	theme    Theme
	width    int
	height   int
	offset   int
}

// projectEntry is a flattened project for display.
type projectEntry struct {
	Name    string
	Project config.Project
}

func NewProjectList(theme Theme) ProjectList {
	return ProjectList{theme: theme}
}

func (pl *ProjectList) SetProjects(projects map[string]config.Project) {
	pl.projects = nil
	for name, proj := range projects {
		pl.projects = append(pl.projects, projectEntry{Name: name, Project: proj})
	}
	// Sort alphabetically for stable ordering
	sortProjects(pl.projects)
	if pl.cursor >= len(pl.projects) {
		pl.cursor = max(0, len(pl.projects)-1)
	}
}

func sortProjects(entries []projectEntry) {
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j].Name < entries[j-1].Name; j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
}

func (pl *ProjectList) SetSize(w, h int) {
	pl.width = w
	pl.height = h
}

func (pl *ProjectList) CursorUp() {
	if pl.cursor > 0 {
		pl.cursor--
		if pl.cursor < pl.offset {
			pl.offset = pl.cursor
		}
	}
}

func (pl *ProjectList) CursorDown() {
	if pl.cursor < len(pl.projects)-1 {
		pl.cursor++
		visible := pl.visibleRows()
		if pl.cursor >= pl.offset+visible {
			pl.offset = pl.cursor - visible + 1
		}
	}
}

func (pl *ProjectList) Selected() *projectEntry {
	if len(pl.projects) == 0 {
		return nil
	}
	if pl.cursor >= 0 && pl.cursor < len(pl.projects) {
		return &pl.projects[pl.cursor]
	}
	return nil
}

func (pl *ProjectList) visibleRows() int {
	// Each project takes 2 lines (name + path)
	rows := pl.height / 2
	if rows < 1 {
		rows = 1
	}
	return rows
}

func (pl ProjectList) View() string {
	if len(pl.projects) == 0 {
		return "\n" + pl.theme.Dimmed.Render("    No projects configured. Press [n] to add one.")
	}

	var b strings.Builder
	visible := pl.visibleRows()
	end := pl.offset + visible
	if end > len(pl.projects) {
		end = len(pl.projects)
	}

	for i := pl.offset; i < end; i++ {
		entry := pl.projects[i]
		selected := i == pl.cursor

		// Project name
		nameStyle := pl.theme.Normal
		if selected {
			nameStyle = pl.theme.Selected
		}
		name := nameStyle.Render(entry.Name)

		// Cursor indicator
		cursor := "  "
		if selected {
			cursor = pl.theme.Selected.Render(" >")
		}

		// Backend label (right-aligned)
		backend := entry.Project.Backend
		if backend == "" {
			backend = "default"
		}
		right := pl.theme.Dimmed.Render(backend)

		// Build first line
		left := fmt.Sprintf("%s  %s", cursor, name)
		gap := pl.width - lipgloss.Width(left) - lipgloss.Width(right) - 2
		if gap < 1 {
			gap = 1
		}
		b.WriteString(left + strings.Repeat(" ", gap) + right + "\n")

		// Second line: path + branch
		detail := "      "
		if entry.Project.Path != "" {
			detail += pl.theme.Dimmed.Render(entry.Project.Path)
		}
		if entry.Project.Branch != "" {
			detail += pl.theme.Dimmed.Render(" (" + entry.Project.Branch + ")")
		}
		b.WriteString(detail + "\n")
	}

	return b.String()
}
