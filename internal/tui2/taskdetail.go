package tui2

import (
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/model"
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

	// Draw border
	drawBorder(screen, x-1, y-1, width+2, height+2, StyleBorder)

	// Title in border
	title := " Details "
	for i, r := range title {
		if x+i < x+width {
			screen.SetContent(x+i, y-1, r, nil, StyleBorder.Bold(true))
		}
	}

	if td.task == nil {
		drawText(screen, x+1, y+1, width-2, "No task selected", StyleDimmed)
		return
	}

	t := td.task
	innerW := width - 2
	row := y

	// Task name (title)
	name := t.Name
	if len(name) > innerW-1 {
		name = name[:innerW-4] + "..."
	}
	drawText(screen, x+1, row, innerW, name, StyleTitle)
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
	row = td.drawField(screen, x, row, innerW, "Status", statusLabel, statusStyle)

	// Project
	if t.Project != "" {
		row = td.drawField(screen, x, row, innerW, "Project", t.Project, StyleNormal)
	}

	// Branch
	if t.Branch != "" {
		row = td.drawField(screen, x, row, innerW, "Branch", t.Branch, StyleNormal)
	}

	// Backend
	if t.Backend != "" {
		row = td.drawField(screen, x, row, innerW, "Backend", t.Backend, StyleNormal)
	}

	// PR URL
	if t.PRURL != "" {
		pr := t.PRURL
		maxLen := innerW - 5
		if maxLen > 3 && len(pr) > maxLen {
			pr = "..." + pr[len(pr)-maxLen+3:]
		}
		row = td.drawField(screen, x, row, innerW, "PR", pr, StyleNormal)
	}

	// Worktree
	if t.Worktree != "" {
		wt := t.Worktree
		maxLen := innerW - 11
		if maxLen > 3 && len(wt) > maxLen {
			wt = "..." + wt[len(wt)-maxLen+3:]
		}
		row = td.drawField(screen, x, row, innerW, "Worktree", wt, StyleNormal)
	}

	// Created date
	if !t.CreatedAt.IsZero() {
		row = td.drawField(screen, x, row, innerW, "Created", t.CreatedAt.Format(time.DateOnly), StyleNormal)
	}

	// Elapsed
	if elapsed := t.ElapsedString(); elapsed != "" {
		row = td.drawField(screen, x, row, innerW, "Elapsed", elapsed, tcell.StyleDefault.Foreground(ColorElapsed))
	}

	// Prompt
	if t.Prompt != "" && row < y+height-1 {
		row++
		drawText(screen, x+1, row, innerW, "PROMPT", StyleTitle)
		row++
		remaining := y + height - row
		promptLines := td.wrapText(t.Prompt, innerW-1)
		for i, line := range promptLines {
			if i >= remaining {
				break
			}
			drawText(screen, x+1, row, innerW, line, StyleNormal)
			row++
		}
	}
}

// drawField renders "  Label: Value" and returns the next row.
func (td *TaskDetailPanel) drawField(screen tcell.Screen, x, row, innerW int, label, value string, valStyle tcell.Style) int {
	labelStr := fmt.Sprintf("%s: ", label)
	drawText(screen, x+1, row, len(labelStr), labelStr, StyleDimmed)
	drawText(screen, x+1+len(labelStr), row, innerW-len(labelStr), value, valStyle)
	return row + 1
}

// statusStyle returns the style for a given status.
func (td *TaskDetailPanel) statusStyle(s model.Status) tcell.Style {
	switch s {
	case model.StatusPending:
		return StylePending
	case model.StatusInProgress:
		return StyleInProgress
	case model.StatusInReview:
		return StyleInReview
	case model.StatusComplete:
		return StyleComplete
	default:
		return StyleNormal
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
