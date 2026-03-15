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
	removedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Background(lipgloss.Color("#3d1012"))
	addedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("78")).Background(lipgloss.Color("#0d3317"))
	removedBgEsc := "\x1b[48;2;61;16;18m"  // #3d1012
	addedBgEsc := "\x1b[48;2;13;51;23m"    // #0d3317
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
		leftContent := formatSideContent(leftH, row.LeftText, row.LeftType, contentW, removedStyle, addedStyle, removedBgEsc, addedBgEsc)

		// Right side
		rightNumStr := lineNumStyle.Render(FormatLineNum(row.RightNum, lineNumWidth))
		rightContent := formatSideContent(rightH, row.RightText, row.RightType, contentW, removedStyle, addedStyle, removedBgEsc, addedBgEsc)

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
func formatSideContent(highlighted, raw string, lineType DiffLineType, width int, removedStyle, addedStyle lipgloss.Style, removedBgEsc, addedBgEsc string) string {
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
			// Syntax-highlighted: inject background after every reset so it persists across tokens
			content = injectBg(ansi.Truncate(highlighted, width-1, "\x1b[0m"), removedBgEsc)
		}
	case DiffAdded:
		prefix = addedStyle.Render("+")
		if highlighted == raw {
			content = addedStyle.Render(truncatePlain(raw, width-1))
		} else {
			content = injectBg(ansi.Truncate(highlighted, width-1, "\x1b[0m"), addedBgEsc)
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

	padStr := strings.Repeat(" ", pad)
	// Apply background color to padding for added/removed lines
	switch lineType {
	case DiffRemoved:
		padStr = removedStyle.Render(padStr)
	case DiffAdded:
		padStr = addedStyle.Render(padStr)
	}

	return prefix + content + padStr
}

// injectBg re-applies a background ANSI escape after every reset (\x1b[0m) in the
// string so the background color persists across syntax-highlighted tokens.
func injectBg(s string, bgEsc string) string {
	return bgEsc + strings.ReplaceAll(s, "\x1b[0m", "\x1b[0m"+bgEsc) + "\x1b[0m"
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

// RenderDiffHeader renders the header line for the diff panel with mode indicator.
func RenderDiffHeader(filename string, fileIdx, fileCount int, mode string, theme Theme) string {
	return theme.Section.Render("  DIFF") +
		theme.Dimmed.Render(" "+filename) +
		theme.Dimmed.Render(fmt.Sprintf("  [%d/%d]", fileIdx+1, fileCount)) +
		theme.Dimmed.Render("  "+mode)
}

// RenderUnifiedLines builds syntax-highlighted unified diff lines from a ParsedDiff.
// Each line includes the +/- prefix and line numbers. Returns pre-rendered ANSI strings.
func RenderUnifiedLines(pd ParsedDiff, filename string) []string {
	if len(pd.Hunks) == 0 {
		return nil
	}

	removedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Background(lipgloss.Color("#3d1012"))
	addedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("78")).Background(lipgloss.Color("#0d3317"))
	removedBgEsc := "\x1b[48;2;61;16;18m"  // #3d1012
	addedBgEsc := "\x1b[48;2;13;51;23m"    // #0d3317
	lineNumStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("239"))
	hunkHeaderStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Italic(true)
	dividerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("236"))

	// Collect all line contents for batch highlighting
	var allContents []string
	type lineRef struct {
		hunkIdx int
		lineIdx int
		isHunk  bool // true = hunk header or separator
	}
	var refs []lineRef

	for hi, hunk := range pd.Hunks {
		if hi > 0 {
			refs = append(refs, lineRef{isHunk: true})
			allContents = append(allContents, "───")
		}
		refs = append(refs, lineRef{isHunk: true})
		allContents = append(allContents, hunk.Header)
		for li := range hunk.Lines {
			refs = append(refs, lineRef{hunkIdx: hi, lineIdx: li})
			allContents = append(allContents, hunk.Lines[li].Content)
		}
	}

	highlighted := HighlightLines(allContents, filename)

	var result []string
	for i, ref := range refs {
		hl := highlighted[i]

		if ref.isHunk {
			if allContents[i] == "───" {
				result = append(result, dividerStyle.Render("───"))
			} else {
				result = append(result, hunkHeaderStyle.Render(allContents[i]))
			}
			continue
		}

		dl := pd.Hunks[ref.hunkIdx].Lines[ref.lineIdx]
		oldNum := FormatLineNum(dl.OldNum, lineNumWidth)
		newNum := FormatLineNum(dl.NewNum, lineNumWidth)
		nums := lineNumStyle.Render(oldNum) + " " + lineNumStyle.Render(newNum)

		switch dl.Type {
		case DiffRemoved:
			prefix := removedStyle.Render("-")
			var content string
			if hl == dl.Content {
				content = removedStyle.Render(dl.Content)
			} else {
				// Syntax-highlighted: inject background after every reset so it persists
				content = injectBg(hl, removedBgEsc)
			}
			result = append(result, nums+" "+prefix+content)
		case DiffAdded:
			prefix := addedStyle.Render("+")
			var content string
			if hl == dl.Content {
				content = addedStyle.Render(dl.Content)
			} else {
				content = injectBg(hl, addedBgEsc)
			}
			result = append(result, nums+" "+prefix+content)
		default:
			result = append(result, nums+"  "+hl)
		}
	}

	return result
}

// RenderUnified renders the unified diff view for the given visible window.
func RenderUnified(lines []string, dispW, visibleH, scrollOff int) string {
	if len(lines) == 0 {
		return "(no diff)"
	}

	end := scrollOff + visibleH
	if end > len(lines) {
		end = len(lines)
	}
	start := scrollOff
	if start > end {
		start = end
	}

	var b strings.Builder
	for i := start; i < end; i++ {
		b.WriteString(ansi.Truncate(lines[i], dispW, "\x1b[0m"))
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}
