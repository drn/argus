package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
)

// ProjectList renders the project list view.
type ProjectList struct {
	projects   []projectEntry
	taskCounts map[string]statusCounts // project name → task status counts
	scroll     ScrollState
	theme      Theme
	width      int
	height     int
}

// statusCounts tracks task counts per status for a project.
type statusCounts struct {
	Pending    int
	InProgress int
	InReview   int
	Complete   int
}

func (sc statusCounts) Total() int {
	return sc.Pending + sc.InProgress + sc.InReview + sc.Complete
}

// projectEntry is a flattened project for display.
type projectEntry struct {
	Name    string
	Project config.Project
}

func NewProjectList(theme Theme) ProjectList {
	return ProjectList{theme: theme, taskCounts: make(map[string]statusCounts)}
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

// SetTasks computes per-project task status counts.
func (pl *ProjectList) SetTasks(tasks []*model.Task) {
	counts := make(map[string]statusCounts, len(pl.projects))
	for _, t := range tasks {
		sc := counts[t.Project]
		switch t.Status {
		case model.StatusPending:
			sc.Pending++
		case model.StatusInProgress:
			sc.InProgress++
		case model.StatusInReview:
			sc.InReview++
		case model.StatusComplete:
			sc.Complete++
		}
		counts[t.Project] = sc
	}
	pl.taskCounts = counts
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

// TaskCounts returns the task status counts for the named project.
func (pl *ProjectList) TaskCounts(name string) statusCounts {
	return pl.taskCounts[name]
}

func (pl *ProjectList) visibleRows() int {
	// Each project takes 3 lines (name, path, status summary)
	rows := pl.height / 3
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
		cur := "  "
		if selected {
			cur = pl.theme.Selected.Render(">")  + " "
		}

		// Task count badge
		sc := pl.taskCounts[entry.Name]
		total := sc.Total()
		var badge string
		if total > 0 {
			badge = pl.theme.Dimmed.Render(fmt.Sprintf(" %d tasks", total))
		}

		// Build first line: cursor + name + badge (right-aligned)
		left := fmt.Sprintf(" %s %s", cur, name)
		right := badge
		gap := pl.width - lipgloss.Width(left) - lipgloss.Width(right) - 1
		if gap < 1 {
			gap = 1
		}
		b.WriteString(left + strings.Repeat(" ", gap) + right + "\n")

		// Second line: path + branch
		detail := "     "
		if entry.Project.Path != "" {
			detail += pl.theme.Dimmed.Render(entry.Project.Path)
		}
		if entry.Project.Branch != "" {
			detail += pl.theme.Dimmed.Render(" (" + entry.Project.Branch + ")")
		}
		b.WriteString(detail + "\n")

		// Third line: mini status indicators
		if total > 0 {
			b.WriteString("     " + pl.renderMiniStatus(sc) + "\n")
		} else {
			b.WriteString("     " + pl.theme.Dimmed.Render("no tasks") + "\n")
		}
	}

	return b.String()
}

// renderMiniStatus renders compact colored status counts (e.g., "2 pending  1 active  3 done").
func (pl ProjectList) renderMiniStatus(sc statusCounts) string {
	var parts []string
	if sc.Pending > 0 {
		parts = append(parts, pl.theme.Pending.Render(fmt.Sprintf("○ %d", sc.Pending)))
	}
	if sc.InProgress > 0 {
		parts = append(parts, pl.theme.InProgress.Render(fmt.Sprintf("● %d", sc.InProgress)))
	}
	if sc.InReview > 0 {
		parts = append(parts, pl.theme.InReview.Render(fmt.Sprintf("● %d", sc.InReview)))
	}
	if sc.Complete > 0 {
		parts = append(parts, pl.theme.Complete.Render(fmt.Sprintf("✓ %d", sc.Complete)))
	}
	return strings.Join(parts, "  ")
}
