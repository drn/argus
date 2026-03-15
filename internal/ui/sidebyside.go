package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// lineNumWidth is the number of characters reserved for line numbers.
const lineNumWidth = 4

// RenderSideBySide renders a side-by-side diff view for the given width and
// visible line range. Returns a string ready for display inside a bordered panel.
func RenderSideBySide(rows []SideBySideLine, filename string, totalW, visibleH, scrollOff int, theme Theme) string {
	if len(rows) == 0 {
		return theme.Dimmed.Render("(no diff)")
	}

	// Each side: lineNumWidth + 1 (separator) + content + 1 (padding)
	// Middle divider: 1 char ("│")
	// Total: 2*(lineNumWidth + 2 + contentW) + 1 = totalW
	sideW := (totalW - 1) / 2
	contentW := sideW - lineNumWidth - 2
	if contentW < 5 {
		contentW = 5
	}

	// Syntax highlight all left and right text lines
	leftTexts := make([]string, len(rows))
	rightTexts := make([]string, len(rows))
	for i, r := range rows {
		leftTexts[i] = r.LeftText
		rightTexts[i] = r.RightText
	}
	leftHL := HighlightLines(leftTexts, filename)
	rightHL := HighlightLines(rightTexts, filename)

	// Styles for diff line backgrounds
	removedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	addedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("78"))
	lineNumStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("239"))
	dividerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("236"))
	hunkHeaderStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Italic(true)
	divider := dividerStyle.Render("│")

	// Window slice
	end := scrollOff + visibleH
	if end > len(rows) {
		end = len(rows)
	}
	start := scrollOff
	if start > end {
		start = end
	}

	var b strings.Builder
	for i := start; i < end; i++ {
		row := rows[i]
		leftH := leftHL[i]
		rightH := rightHL[i]

		// Hunk header rows (no line numbers)
		if row.LeftNum == 0 && row.RightNum == 0 && strings.HasPrefix(row.LeftText, "@@") {
			headerLine := hunkHeaderStyle.Render(ansi.Truncate(row.LeftText, totalW, "\x1b[0m"))
			b.WriteString(headerLine)
			if i < end-1 {
				b.WriteString("\n")
			}
			continue
		}

		// Separator rows
		if row.LeftNum == 0 && row.RightNum == 0 && row.LeftText == "───" {
			sep := dividerStyle.Render(strings.Repeat("─", totalW))
			b.WriteString(sep)
			if i < end-1 {
				b.WriteString("\n")
			}
			continue
		}

		// Left side
		leftNumStr := lineNumStyle.Render(FormatLineNum(row.LeftNum, lineNumWidth))
		leftContent := formatSideContent(leftH, row.LeftText, row.LeftType, contentW, removedStyle, addedStyle)

		// Right side
		rightNumStr := lineNumStyle.Render(FormatLineNum(row.RightNum, lineNumWidth))
		rightContent := formatSideContent(rightH, row.RightText, row.RightType, contentW, removedStyle, addedStyle)

		b.WriteString(leftNumStr)
		b.WriteString(" ")
		b.WriteString(leftContent)
		b.WriteString(divider)
		b.WriteString(rightNumStr)
		b.WriteString(" ")
		b.WriteString(rightContent)
		if i < end-1 {
			b.WriteString("\n")
		}
	}

	return b.String()
}

// formatSideContent renders one side of a diff line with appropriate coloring.
func formatSideContent(highlighted, raw string, lineType DiffLineType, width int, removedStyle, addedStyle lipgloss.Style) string {
	if raw == "" && lineType == DiffContext {
		// Blank padding
		return strings.Repeat(" ", width+1)
	}

	var prefix string
	var content string

	switch lineType {
	case DiffRemoved:
		prefix = removedStyle.Render("-")
		if highlighted == raw {
			// No highlighting — apply removal color to whole line
			content = removedStyle.Render(truncatePlain(raw, width-1))
		} else {
			content = ansi.Truncate(highlighted, width-1, "\x1b[0m")
		}
	case DiffAdded:
		prefix = addedStyle.Render("+")
		if highlighted == raw {
			content = addedStyle.Render(truncatePlain(raw, width-1))
		} else {
			content = ansi.Truncate(highlighted, width-1, "\x1b[0m")
		}
	default:
		prefix = " "
		if highlighted == raw {
			content = truncatePlain(raw, width-1)
		} else {
			content = ansi.Truncate(highlighted, width-1, "\x1b[0m")
		}
	}

	// Pad content to fill the column
	visW := ansi.StringWidth(prefix + content)
	pad := width + 1 - visW
	if pad < 0 {
		pad = 0
	}

	return prefix + content + strings.Repeat(" ", pad)
}

// truncatePlain truncates a plain (no ANSI) string to maxW visible characters.
func truncatePlain(s string, maxW int) string {
	if len(s) <= maxW {
		return s
	}
	// Simple rune-based truncation
	runes := []rune(s)
	if len(runes) <= maxW {
		return s
	}
	return string(runes[:maxW])
}

// RenderSideBySideHeader renders the header line for the diff panel.
func RenderSideBySideHeader(filename string, fileIdx, fileCount int, theme Theme) string {
	return theme.Section.Render("  DIFF") +
		theme.Dimmed.Render(" "+filename) +
		theme.Dimmed.Render(fmt.Sprintf("  [%d/%d]", fileIdx+1, fileCount))
}
