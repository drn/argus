package tui2

import (
	"strings"

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
	borderStyle := StyleBorder
	if gp.focused {
		borderStyle = StyleFocusedBorder
	}
	drawBorder(screen, x-1, y-1, width+2, height+2, borderStyle)

	// Title in border
	title := " Git Status "
	for i, r := range title {
		if x+i < x+width {
			screen.SetContent(x+i, y-1, r, nil, borderStyle.Bold(true))
		}
	}

	if !gp.loaded {
		drawText(screen, x+1, y+1, width-2, "Loading...", StyleDimmed)
		return
	}

	row := y
	innerW := width - 1

	// STATUS section
	if len(gp.statusLines) > 0 {
		drawText(screen, x+1, row, innerW, "FILES", StyleTitle)
		row++
		for _, line := range gp.statusLines {
			if row >= y+height {
				break
			}
			style := gp.statusLineStyle(line)
			text := truncate(line, innerW)
			drawText(screen, x+1, row, innerW, text, style)
			row++
		}
		row++ // spacer
	}

	// DIFF section
	if len(gp.diffLines) > 0 && row < y+height {
		drawText(screen, x+1, row, innerW, "DIFF", StyleTitle)
		row++
		for _, line := range gp.diffLines {
			if row >= y+height {
				break
			}
			text := truncate(line, innerW)
			drawText(screen, x+1, row, innerW, text, StyleDimmed)
			row++
		}
		row++
	}

	// BRANCH section
	if len(gp.branchLines) > 0 && row < y+height {
		drawText(screen, x+1, row, innerW, "BRANCH", StyleTitle)
		row++
		for _, line := range gp.branchLines {
			if row >= y+height {
				break
			}
			text := truncate(line, innerW)
			drawText(screen, x+1, row, innerW, text, StyleDimmed)
			row++
		}
	}

	// Empty state
	if len(gp.statusLines) == 0 && len(gp.diffLines) == 0 && len(gp.branchLines) == 0 {
		drawText(screen, x+1, y+1, innerW, "Clean — no changes", StyleDimmed)
	}
}

func (gp *GitPanel) statusLineStyle(line string) tcell.Style {
	if len(line) < 2 {
		return StyleNormal
	}
	status := strings.TrimSpace(line[:2])
	switch {
	case status == "M" || status == "MM":
		return tcell.StyleDefault.Foreground(ColorInReview)
	case status == "A" || status == "??":
		return tcell.StyleDefault.Foreground(ColorComplete)
	case status == "D":
		return tcell.StyleDefault.Foreground(ColorError)
	default:
		return StyleNormal
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
