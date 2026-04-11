package gitpanel

import (
	"strings"

	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// GitPanel displays git status in a bordered side panel.
type GitPanel struct {
	*tview.Box
	statusLines []string
	diffLines   []string
	branchLines []string
	loaded      bool
	focused     bool
}

// NewGitPanel creates a git status panel.
func NewGitPanel() *GitPanel {
	return &GitPanel{
		Box: tview.NewBox(),
	}
}

// SetFocused updates focus state.
func (gp *GitPanel) SetFocused(f bool) {
	gp.focused = f
}

// SetStatus updates the git status content.
func (gp *GitPanel) SetStatus(status, diff, branchDiff string) {
	gp.loaded = true
	gp.statusLines = splitNonEmpty(status)
	gp.diffLines = splitNonEmpty(diff)
	gp.branchLines = splitNonEmpty(branchDiff)
}

// Clear resets the panel content.
func (gp *GitPanel) Clear() {
	gp.loaded = false
	gp.statusLines = nil
	gp.diffLines = nil
	gp.branchLines = nil
}

// Draw renders the git status panel.
func (gp *GitPanel) Draw(screen tcell.Screen) {
	gp.Box.DrawForSubclass(screen, gp)
	x, y, width, height := gp.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	// Draw border
	borderStyle := theme.StyleBorder
	if gp.focused {
		borderStyle = theme.StyleFocusedBorder
	}

	inner := widget.DrawBorderedPanel(screen, x, y, width, height, " Git Status ", borderStyle)
	if inner.W <= 0 || inner.H <= 0 {
		return
	}

	if !gp.loaded {
		widget.DrawText(screen, inner.X, inner.Y, inner.W, "Loading...", theme.StyleDimmed)
		return
	}

	row := inner.Y
	maxRow := inner.Y + inner.H

	// STATUS section
	if len(gp.statusLines) > 0 {
		widget.DrawText(screen, inner.X, row, inner.W, "Files", theme.StyleTitle)
		row++
		for _, line := range gp.statusLines {
			if row >= maxRow {
				break
			}
			style := gp.statusLineStyle(line)
			text := truncate(line, inner.W)
			widget.DrawText(screen, inner.X, row, inner.W, text, style)
			row++
		}
		row++ // spacer
	}

	// DIFF section
	if len(gp.diffLines) > 0 && row < maxRow {
		widget.DrawText(screen, inner.X, row, inner.W, "Diff", theme.StyleTitle)
		row++
		for _, line := range gp.diffLines {
			if row >= maxRow {
				break
			}
			text := truncate(line, inner.W)
			widget.DrawText(screen, inner.X, row, inner.W, text, theme.StyleDimmed)
			row++
		}
		row++
	}

	// BRANCH section
	if len(gp.branchLines) > 0 && row < maxRow {
		widget.DrawText(screen, inner.X, row, inner.W, "BRANCH", theme.StyleTitle)
		row++
		for _, line := range gp.branchLines {
			if row >= maxRow {
				break
			}
			text := truncate(line, inner.W)
			widget.DrawText(screen, inner.X, row, inner.W, text, theme.StyleDimmed)
			row++
		}
	}

	// Empty state
	if len(gp.statusLines) == 0 && len(gp.diffLines) == 0 && len(gp.branchLines) == 0 {
		widget.DrawText(screen, inner.X, inner.Y, inner.W, "Clean — no changes", theme.StyleDimmed)
	}
}

func (gp *GitPanel) statusLineStyle(line string) tcell.Style {
	if len(line) < 2 {
		return theme.StyleNormal
	}
	status := strings.TrimSpace(line[:2])
	switch {
	case status == "M" || status == "MM":
		return tcell.StyleDefault.Foreground(theme.ColorInReview)
	case status == "A" || status == "??":
		return tcell.StyleDefault.Foreground(theme.ColorComplete)
	case status == "D":
		return tcell.StyleDefault.Foreground(theme.ColorError)
	default:
		return theme.StyleNormal
	}
}

func splitNonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	var result []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			result = append(result, l)
		}
	}
	return result
}

func truncate(s string, maxW int) string {
	runes := []rune(s)
	if len(runes) <= maxW {
		return s
	}
	if maxW <= 3 {
		return string(runes[:maxW])
	}
	return string(runes[:maxW-1]) + "…"
}
