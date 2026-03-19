package ui

import (
	"strings"
	"time"
)

// gitRefreshInterval is how long between automatic git status refreshes.
const gitRefreshInterval = 3 * time.Second

// GitStatus renders worktree git status and diff stat above the preview pane.
type GitStatus struct {
	theme       Theme
	width       int
	height      int
	taskID         string
	statusText     string
	diffText       string
	branchDiffText string
	loaded         bool
	lastRefresh time.Time
	focused     bool
}

func NewGitStatus(theme Theme) GitStatus {
	return GitStatus{theme: theme}
}

func (g *GitStatus) SetSize(w, h int) {
	g.width = w
	g.height = h
}

// Update caches the git status result if it matches the current task.
func (g *GitStatus) Update(msg GitStatusRefreshMsg) {
	if msg.TaskID == g.taskID {
		g.statusText = msg.Status
		g.diffText = msg.Diff
		g.branchDiffText = msg.BranchDiff
		g.loaded = true
		g.lastRefresh = time.Now()
	}
}

// SetTask updates the tracked task; clears cached data on change.
func (g *GitStatus) SetTask(taskID string) {
	if taskID != g.taskID {
		g.taskID = taskID
		g.statusText = ""
		g.diffText = ""
		g.branchDiffText = ""
		g.loaded = false
		g.lastRefresh = time.Time{}
	}
}

// NeedsRefresh returns true if we should kick off a new git status check.
func (g *GitStatus) NeedsRefresh() bool {
	if g.taskID == "" {
		return false
	}
	return time.Since(g.lastRefresh) > gitRefreshInterval
}

// SetFocused sets whether this panel has focus (changes border color).
func (g *GitStatus) SetFocused(focused bool) {
	g.focused = focused
}

func (g GitStatus) View() string {
	innerW := max(g.width-4, 10)
	innerH := max(g.height-2, 1)

	renderPanel := func(content string) string {
		return borderedPanel(g.width, g.height, g.focused, content)
	}

	if g.taskID == "" {
		return renderPanel(g.theme.Dimmed.Render(" No worktree"))
	}

	if !g.loaded {
		return renderPanel(g.theme.Dimmed.Render(" Loading..."))
	}

	if g.statusText == "" && g.diffText == "" && g.branchDiffText == "" {
		return renderPanel(g.theme.Dimmed.Render(" Clean — no changes"))
	}

	var sections []string

	if g.statusText != "" {
		header := g.theme.Section.Render("  FILES")
		lines := g.truncateLines(g.statusText, innerW, innerH-2)
		sections = append(sections, header+"\n"+g.colorizeStatus(lines))
	}

	if g.diffText != "" {
		header := g.theme.Section.Render("  DIFF")
		lines := g.truncateLines(g.diffText, innerW, innerH-2)
		sections = append(sections, header+"\n"+g.colorizeDiff(lines))
	}

	if g.branchDiffText != "" {
		header := g.theme.Section.Render("  BRANCH")
		lines := g.truncateLines(g.branchDiffText, innerW, innerH-2)
		sections = append(sections, header+"\n"+g.colorizeDiff(lines))
	}

	content := strings.Join(sections, "\n")

	contentLines := strings.Split(content, "\n")
	if len(contentLines) > innerH {
		contentLines = contentLines[:innerH]
	}

	return renderPanel(strings.Join(contentLines, "\n"))
}

func (g GitStatus) truncateLines(text string, maxWidth, maxLines int) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	for i, line := range lines {
		if len(line) > maxWidth {
			lines[i] = line[:maxWidth-1] + "…"
		}
	}
	return strings.Join(lines, "\n")
}

func (g GitStatus) colorizeStatus(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "M "), strings.HasPrefix(trimmed, "MM"):
			lines[i] = g.theme.InReview.Render("  " + trimmed)
		case strings.HasPrefix(trimmed, "A "), strings.HasPrefix(trimmed, "??"):
			lines[i] = g.theme.Complete.Render("  " + trimmed)
		case strings.HasPrefix(trimmed, "D "):
			lines[i] = g.theme.Error.Render("  " + trimmed)
		default:
			lines[i] = g.theme.Normal.Render("  " + trimmed)
		}
	}
	return strings.Join(lines, "\n")
}

func (g GitStatus) colorizeDiff(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = g.theme.Dimmed.Render("  " + line)
	}
	return strings.Join(lines, "\n")
}

