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
		empty := tl.theme.Dimmed.Render("  No tasks. Press 'n' to create one.")
		return empty
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

		// Status styling
		var statusStyle lipgloss.Style
		switch t.Status {
		case model.StatusPending:
			statusStyle = tl.theme.Pending
		case model.StatusInProgress:
			statusStyle = tl.theme.InProgress
		case model.StatusInReview:
			statusStyle = tl.theme.InReview
		case model.StatusComplete:
			statusStyle = tl.theme.Complete
		}

		badge := statusStyle.Render(t.Status.Badge())
		statusText := statusStyle.Render(t.Status.Display())

		// Task name
		nameStyle := tl.theme.Normal
		if selected {
			nameStyle = tl.theme.Selected
		}
		if t.Status == model.StatusComplete {
			nameStyle = tl.theme.Dimmed
		}

		name := nameStyle.Render(t.Name)

		// First line: badge + name + status
		line1 := fmt.Sprintf("  %s  %-*s %s",
			badge,
			tl.width-lipgloss.Width(statusText)-10,
			name,
			statusText,
		)

		// Second line: project + branch + elapsed
		project := tl.theme.ProjectName.Render(t.Project)
		branch := tl.theme.Dimmed.Render(t.Branch)
		elapsed := tl.theme.Elapsed.Render(t.ElapsedString())
		line2 := fmt.Sprintf("       %s  %s  %s", project, branch, elapsed)

		if selected {
			// Highlight indicator
			line1 = " ▸" + line1[2:]
		}

		b.WriteString(line1 + "\n")
		b.WriteString(line2 + "\n")
	}

	return b.String()
}
