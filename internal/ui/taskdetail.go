package ui

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/drn/argus/internal/model"
)

// TaskDetail renders metadata for the selected task in a right-side panel.
type TaskDetail struct {
	theme  Theme
	width  int
	height int
}

func NewTaskDetail(theme Theme) TaskDetail {
	return TaskDetail{theme: theme}
}

func (d *TaskDetail) SetSize(w, h int) {
	d.width = w
	d.height = h
}

// View renders the task detail panel. If t is nil, shows an empty state.
func (d TaskDetail) View(t *model.Task, running bool) string {
	if t == nil {
		return borderedPanel(d.width, d.height, false,
			d.theme.Dimmed.Render(" No task selected"))
	}

	innerW := max(d.width-4, 10)
	innerH := max(d.height-2, 1)

	var b strings.Builder

	// Title
	name := t.Name
	if len(name) > innerW-2 {
		name = name[:innerW-5] + "..."
	}
	b.WriteString(d.theme.Title.Render(" "+name) + "\n\n")

	// Status with running/idle indicator
	statusStyle := d.statusStyle(t.Status)
	statusLabel := t.Status.DisplayName()
	if t.Status == model.StatusInProgress {
		if running {
			statusLabel += " (running)"
		} else {
			statusLabel += " (idle)"
		}
	}
	b.WriteString("  " + d.theme.Dimmed.Render("Status: ") + statusStyle.Render(statusLabel) + "\n")

	// Project
	if t.Project != "" {
		b.WriteString("  " + d.theme.Dimmed.Render("Project: ") + d.theme.Normal.Render(t.Project) + "\n")
	}

	// Branch
	if t.Branch != "" {
		b.WriteString("  " + d.theme.Dimmed.Render("Branch: ") + d.theme.Normal.Render(t.Branch) + "\n")
	}

	// Backend (only if non-empty, i.e. non-default)
	if t.Backend != "" {
		b.WriteString("  " + d.theme.Dimmed.Render("Backend: ") + d.theme.Normal.Render(t.Backend) + "\n")
	}

	// Worktree path (truncated)
	if t.Worktree != "" {
		wt := t.Worktree
		maxWtLen := innerW - 14 // "  Worktree: " + some margin
		if maxWtLen > 0 && len(wt) > maxWtLen {
			wt = "..." + wt[len(wt)-maxWtLen+3:]
		}
		b.WriteString("  " + d.theme.Dimmed.Render("Worktree: ") + d.theme.Normal.Render(wt) + "\n")
	}

	// Created date
	if !t.CreatedAt.IsZero() {
		b.WriteString("  " + d.theme.Dimmed.Render("Created: ") + d.theme.Normal.Render(t.CreatedAt.Format(time.DateOnly)) + "\n")
	}

	// Elapsed time
	if elapsed := t.ElapsedString(); elapsed != "" {
		b.WriteString("  " + d.theme.Dimmed.Render("Elapsed: ") + d.theme.Elapsed.Render(elapsed) + "\n")
	}

	// Prompt text (fills remaining space)
	if t.Prompt != "" {
		b.WriteString("\n" + d.theme.Section.Render("  PROMPT") + "\n")
		prompt := d.wrapText(t.Prompt, innerW-2)
		// Truncate to remaining height
		headerLines := strings.Count(b.String(), "\n")
		remaining := innerH - headerLines
		if remaining > 0 {
			promptLines := strings.Split(prompt, "\n")
			if len(promptLines) > remaining {
				promptLines = promptLines[:remaining]
			}
			for _, line := range promptLines {
				b.WriteString("  " + d.theme.Normal.Render(line) + "\n")
			}
		}
	}

	content := strings.TrimRight(b.String(), "\n")
	// Truncate to inner height
	lines := strings.Split(content, "\n")
	if len(lines) > innerH {
		lines = lines[:innerH]
		content = strings.Join(lines, "\n")
	}

	return borderedPanel(d.width, d.height, false, content)
}

// statusStyle returns the theme style for a given status.
func (d TaskDetail) statusStyle(s model.Status) lipgloss.Style {
	switch s {
	case model.StatusPending:
		return d.theme.Pending
	case model.StatusInProgress:
		return d.theme.InProgress
	case model.StatusInReview:
		return d.theme.InReview
	case model.StatusComplete:
		return d.theme.Complete
	default:
		return d.theme.Normal
	}
}

// wrapText wraps text to fit within maxWidth, breaking at word boundaries.
func (d TaskDetail) wrapText(text string, maxWidth int) string {
	if maxWidth <= 0 {
		return text
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
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
	return strings.Join(lines, "\n")
}

