package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/drn/argus/internal/model"
)

// TaskList renders the task list view.
type TaskList struct {
	tasks    []*model.Task
	cursor   int
	theme    Theme
	width    int
	height   int
	offset   int // scroll offset
	filter   string
	filtered []*model.Task
}

func NewTaskList(theme Theme) TaskList {
	return TaskList{theme: theme}
}

func (tl *TaskList) SetTasks(tasks []*model.Task) {
	tl.tasks = tasks
	tl.applyFilter()
	if tl.cursor >= len(tl.filtered) {
		tl.cursor = max(0, len(tl.filtered)-1)
	}
}

func (tl *TaskList) SetSize(w, h int) {
	tl.width = w
	tl.height = h
}

func (tl *TaskList) CursorUp() {
	if tl.cursor > 0 {
		tl.cursor--
		if tl.cursor < tl.offset {
			tl.offset = tl.cursor
		}
	}
}

func (tl *TaskList) CursorDown() {
	if tl.cursor < len(tl.filtered)-1 {
		tl.cursor++
		visible := tl.visibleRows()
		if tl.cursor >= tl.offset+visible {
			tl.offset = tl.cursor - visible + 1
		}
	}
}

func (tl *TaskList) Selected() *model.Task {
	if len(tl.filtered) == 0 {
		return nil
	}
	if tl.cursor >= 0 && tl.cursor < len(tl.filtered) {
		return tl.filtered[tl.cursor]
	}
	return nil
}

func (tl *TaskList) SetFilter(f string) {
	tl.filter = f
	tl.applyFilter()
	tl.cursor = 0
	tl.offset = 0
}

func (tl *TaskList) applyFilter() {
	if tl.filter == "" {
		tl.filtered = tl.tasks
		return
	}
	f := strings.ToLower(tl.filter)
	tl.filtered = nil
	for _, t := range tl.tasks {
		if strings.Contains(strings.ToLower(t.Name), f) ||
			strings.Contains(strings.ToLower(t.Project), f) {
			tl.filtered = append(tl.filtered, t)
		}
	}
}

func (tl *TaskList) visibleRows() int {
	// Each task takes 2 lines (name + details)
	rows := tl.height / 2
	if rows < 1 {
		rows = 1
	}
	return rows
}

func (tl TaskList) View() string {
	if len(tl.filtered) == 0 {
		return "\n" + tl.theme.Dimmed.Render("    No tasks yet. Press [n] to create one.")
	}

	var b strings.Builder
	visible := tl.visibleRows()
	end := tl.offset + visible
	if end > len(tl.filtered) {
		end = len(tl.filtered)
	}

	for i := tl.offset; i < end; i++ {
		t := tl.filtered[i]
		selected := i == tl.cursor

		statusStyle := tl.statusStyle(t.Status)
		badge := statusStyle.Render(t.Status.Badge())

		// Task name
		nameStyle := tl.theme.Normal
		if selected {
			nameStyle = tl.theme.Selected
		}
		if t.Status == model.StatusComplete {
			nameStyle = tl.theme.Dimmed
		}
		name := nameStyle.Render(t.Name)

		// Cursor indicator
		cursor := "  "
		if selected {
			cursor = tl.theme.Selected.Render(" >")
		}

		// Status label (right-aligned)
		statusLabel := statusStyle.Render(t.Status.Display())
		elapsed := ""
		if e := t.ElapsedString(); e != "" {
			elapsed = "  " + tl.theme.Elapsed.Render(e)
		}
		right := statusLabel + elapsed

		// Build first line with padding between name and status
		left := fmt.Sprintf("%s %s  %s", cursor, badge, name)
		gap := tl.width - lipgloss.Width(left) - lipgloss.Width(right) - 2
		if gap < 1 {
			gap = 1
		}
		b.WriteString(left + strings.Repeat(" ", gap) + right + "\n")

		// Second line: project + branch (subtle)
		detail := "      "
		if t.Project != "" {
			detail += tl.theme.ProjectName.Render(t.Project)
			if t.Branch != "" {
				detail += tl.theme.Dimmed.Render(" / " + t.Branch)
			}
		} else if t.Branch != "" {
			detail += tl.theme.Dimmed.Render(t.Branch)
		}
		b.WriteString(detail + "\n")
	}

	return b.String()
}

func (tl TaskList) statusStyle(s model.Status) lipgloss.Style {
	switch s {
	case model.StatusInProgress:
		return tl.theme.InProgress
	case model.StatusInReview:
		return tl.theme.InReview
	case model.StatusComplete:
		return tl.theme.Complete
	default:
		return tl.theme.Pending
	}
}
