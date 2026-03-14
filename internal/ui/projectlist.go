package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/drn/argus/internal/config"
)

// ProjectList renders the project list view.
type ProjectList struct {
	projects []projectEntry
	scroll   ScrollState
	theme    Theme
	width    int
	height   int
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
	pl.scroll.ClampCursor(len(pl.projects))
}

func sortProjects(entries []projectEntry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})
}

func (pl *ProjectList) SetSize(w, h int) {
	pl.width = w
	pl.height = h
}

func (pl *ProjectList) CursorUp() {
	pl.scroll.CursorUp()
}

func (pl *ProjectList) CursorDown() {
	pl.scroll.CursorDown(len(pl.projects), pl.visibleRows())
}

func (pl *ProjectList) Selected() *projectEntry {
	if len(pl.projects) == 0 {
		return nil
	}
	c := pl.scroll.Cursor()
	if c >= 0 && c < len(pl.projects) {
		return &pl.projects[c]
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
	offset := pl.scroll.Offset()
	cursor := pl.scroll.Cursor()
	end := offset + visible
	if end > len(pl.projects) {
		end = len(pl.projects)
	}

	for i := offset; i < end; i++ {
		entry := pl.projects[i]
		selected := i == cursor

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
