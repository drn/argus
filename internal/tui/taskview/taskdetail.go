package taskview

import (
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
)

// TaskDetailPanel displays metadata for the selected task in the right panel.
type TaskDetailPanel struct {
	*tview.Box
	task    *model.Task
	running bool
}

// NewTaskDetailPanel creates a task detail panel.
func NewTaskDetailPanel() *TaskDetailPanel {
	return &TaskDetailPanel{
		Box: tview.NewBox(),
	}
}

// SetTask updates the displayed task.
func (td *TaskDetailPanel) SetTask(t *model.Task, running bool) {
	td.task = t
	td.running = running
}

// Draw renders the task detail panel.
func (td *TaskDetailPanel) Draw(screen tcell.Screen) {
	td.Box.DrawForSubclass(screen, td)
	x, y, width, height := td.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	inner := widget.DrawBorderedPanel(screen, x, y, width, height, " Details ", theme.StyleBorder)
	if inner.W <= 0 || inner.H <= 0 {
		return
	}

	if td.task == nil {
		widget.DrawText(screen, inner.X, inner.Y, inner.W, "No task selected", theme.StyleDimmed)
		return
	}

	t := td.task
	row := inner.Y

	// Task name (title)
	name := t.Name
	if len(name) > inner.W-1 {
		name = name[:inner.W-4] + "..."
	}
	widget.DrawText(screen, inner.X, row, inner.W, name, theme.StyleTitle)
	row += 2

	// Status
	statusLabel := t.Status.DisplayName()
	if t.Status == model.StatusInProgress {
		if td.running {
			statusLabel += " (running)"
		} else {
			statusLabel += " (idle)"
		}
	}
	statusStyle := td.statusStyle(t.Status)
	row = td.drawField(screen, inner.X, row, inner.W, "Status", statusLabel, statusStyle)

	// Project
	if t.Project != "" {
		row = td.drawField(screen, inner.X, row, inner.W, "Project", t.Project, theme.StyleNormal)
	}

	// Branch
	if t.Branch != "" {
		row = td.drawField(screen, inner.X, row, inner.W, "Branch", t.Branch, theme.StyleNormal)
	}

	// Backend
	if t.Backend != "" {
		row = td.drawField(screen, inner.X, row, inner.W, "Backend", t.Backend, theme.StyleNormal)
	}

	// Sandbox
	if t.Sandboxed {
		row = td.drawField(screen, inner.X, row, inner.W, "Sandbox", "Yes", theme.StyleComplete)
	} else {
		row = td.drawField(screen, inner.X, row, inner.W, "Sandbox", "No", theme.StyleDimmed)
	}

	// PR URL
	if t.PRURL != "" {
		pr := t.PRURL
		maxLen := inner.W - 5
		if maxLen > 3 && len(pr) > maxLen {
			pr = "..." + pr[len(pr)-maxLen+3:]
		}
		row = td.drawField(screen, inner.X, row, inner.W, "PR", pr, theme.StyleNormal)
	}

	// Worktree
	if t.Worktree != "" {
		wt := t.Worktree
		maxLen := inner.W - 11
		if maxLen > 3 && len(wt) > maxLen {
			wt = "..." + wt[len(wt)-maxLen+3:]
		}
		row = td.drawField(screen, inner.X, row, inner.W, "Worktree", wt, theme.StyleNormal)
	}

	// Created date
	if !t.CreatedAt.IsZero() {
		row = td.drawField(screen, inner.X, row, inner.W, "Created", t.CreatedAt.Format(time.DateOnly), theme.StyleNormal)
	}

	// Elapsed
	if elapsed := t.ElapsedString(); elapsed != "" {
		row = td.drawField(screen, inner.X, row, inner.W, "Elapsed", elapsed, tcell.StyleDefault.Foreground(theme.ColorElapsed))
	}

	// Prompt
	maxRow := inner.Y + inner.H
	if t.Prompt != "" && row < maxRow-1 {
		row++
		widget.DrawText(screen, inner.X, row, inner.W, "PROMPT", theme.StyleTitle)
		row++
		remaining := maxRow - row
		promptLines := td.wrapText(t.Prompt, inner.W-1)
		for i, line := range promptLines {
			if i >= remaining {
				break
			}
			widget.DrawText(screen, inner.X, row, inner.W, line, theme.StyleNormal)
			row++
		}
	}
}

// drawField renders "Label: Value" and returns the next row.
func (td *TaskDetailPanel) drawField(screen tcell.Screen, x, row, w int, label, value string, valStyle tcell.Style) int {
	labelStr := fmt.Sprintf("%s: ", label)
	widget.DrawText(screen, x, row, len(labelStr), labelStr, theme.StyleDimmed)
	widget.DrawText(screen, x+len(labelStr), row, w-len(labelStr), value, valStyle)
	return row + 1
}

// statusStyle returns the style for a given status.
func (td *TaskDetailPanel) statusStyle(s model.Status) tcell.Style {
	switch s {
	case model.StatusPending:
		return theme.StylePending
	case model.StatusInProgress:
		return theme.StyleInProgress
	case model.StatusInReview:
		return theme.StyleInReview
	case model.StatusComplete:
		return theme.StyleComplete
	default:
		return theme.StyleNormal
	}
}

// wrapText wraps text to fit within maxWidth at word boundaries.
func (td *TaskDetailPanel) wrapText(text string, maxWidth int) []string {
	if maxWidth <= 0 {
		return nil
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	line := words[0]
	for _, w := range words[1:] {
		if len(line)+1+len(w) > maxWidth {
			lines = append(lines, line)
			line = w
		} else {
			line += " " + w
		}
	}
	lines = append(lines, line)
	return lines
}
