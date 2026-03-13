package ui

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// GitStatusRefreshMsg carries the result of a background git status check.
type GitStatusRefreshMsg struct {
	TaskID      string
	Status      string // git status --short output
	Diff        string // git diff --stat (unstaged + staged) output
	BranchDiff  string // git diff --stat against merge-base (committed changes)
	BranchFiles string // git diff --name-status against merge-base (for file list)
}

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
	return time.Since(g.lastRefresh) > 3*time.Second
}

// SetFocused sets whether this panel has focus (changes border color).
func (g *GitStatus) SetFocused(focused bool) {
	g.focused = focused
}

func (g GitStatus) View() string {
	innerW := max(g.width-4, 10) // padding inside border
	innerH := max(g.height-2, 1) // border top/bottom

	borderColor := "238"
	if g.focused {
		borderColor = "87"
	}
	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(borderColor)).
		Width(g.width - 2).
		Height(innerH)

	if g.taskID == "" {
		return border.Render(g.theme.Dimmed.Render(" No worktree"))
	}

	if !g.loaded {
		return border.Render(g.theme.Dimmed.Render(" Loading..."))
	}

	if g.statusText == "" && g.diffText == "" && g.branchDiffText == "" {
		return border.Render(g.theme.Dimmed.Render(" Clean — no changes"))
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

	// Truncate to fit
	contentLines := strings.Split(content, "\n")
	if len(contentLines) > innerH {
		contentLines = contentLines[:innerH]
	}

	return border.Render(strings.Join(contentLines, "\n"))
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

// FetchGitStatus runs git commands in the given worktree directory.
// Intended to be called from a tea.Cmd (off the main goroutine).
func FetchGitStatus(taskID, worktree string) GitStatusRefreshMsg {
	msg := GitStatusRefreshMsg{TaskID: taskID}

	if worktree == "" {
		return msg
	}

	// git status --short
	if out, err := runGit(worktree, "status", "--short"); err == nil {
		msg.Status = strings.TrimRight(out, "\n")
	}

	// git diff HEAD --stat (unstaged + staged changes)
	if out, err := runGit(worktree, "diff", "HEAD", "--stat"); err == nil {
		msg.Diff = strings.TrimRight(out, "\n")
	}

	// git diff against merge-base with default branch (committed branch changes)
	if base := findMergeBase(worktree); base != "" {
		if out, err := runGit(worktree, "diff", "--stat", base+"..HEAD"); err == nil {
			msg.BranchDiff = strings.TrimRight(out, "\n")
		}
		if out, err := runGit(worktree, "diff", "--name-status", base+"..HEAD"); err == nil {
			msg.BranchFiles = strings.TrimRight(out, "\n")
		}
	}

	return msg
}

// findMergeBase finds the merge-base between HEAD and the upstream or default branch.
func findMergeBase(worktree string) string {
	// Try upstream first
	if base, err := runGit(worktree, "merge-base", "HEAD", "HEAD@{upstream}"); err == nil {
		if b := strings.TrimSpace(base); b != "" {
			return b
		}
	}
	// Fallback: try common default branch names
	for _, branch := range []string{"master", "main"} {
		if base, err := runGit(worktree, "merge-base", "HEAD", branch); err == nil {
			if b := strings.TrimSpace(base); b != "" {
				return b
			}
		}
	}
	return ""
}

func runGit(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-pager"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(),
		"GIT_TERMINAL_PROMPT=0", // prevent credential prompts from blocking
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &bytes.Buffer{}
	err := cmd.Run()
	return out.String(), err
}
