package tui2

import (
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/gitutil"
)

// lineNumWidth is the number of characters reserved for line numbers.
const lineNumWidth = 4

// Diff colors. Backgrounds use fixed RGB for consistent diff tinting regardless
// of terminal theme. Foregrounds use palette indices so they adapt to the theme.
var (
	diffRemovedBG = tcell.NewRGBColor(61, 16, 18)  // #3d1012
	diffAddedBG   = tcell.NewRGBColor(13, 51, 23)  // #0d3317
	diffRemovedFG = tcell.PaletteColor(203)         // red
	diffAddedFG   = tcell.PaletteColor(78)          // green
	diffLineNumFG = tcell.PaletteColor(239)         // dim gray
	diffHunkFG    = tcell.PaletteColor(243)         // medium gray
	diffDividerFG = tcell.PaletteColor(236)         // dark gray
)

// renderedDiffLine is a pre-rendered diff line as styled cells, ready to paint.
type renderedDiffLine struct {
	cells []styledChar
}

// buildUnifiedDiffLines creates syntax-highlighted unified diff output from a
// parsed diff and filename. Each line includes line numbers, +/- prefix, and
// syntax-highlighted content with appropriate background colors.
func buildUnifiedDiffLines(pd gitutil.ParsedDiff, filename string) []renderedDiffLine {
	if len(pd.Hunks) == 0 {
		return nil
	}

	// Collect all line contents for batch highlighting.
	type lineRef struct {
		hunkIdx int
		lineIdx int
		isHunk  bool
		isSep   bool
	}
	var refs []lineRef
	var contents []string

	for hi, hunk := range pd.Hunks {
		if hi > 0 {
			refs = append(refs, lineRef{isSep: true})
			contents = append(contents, "───")
		}
		refs = append(refs, lineRef{isHunk: true})
		contents = append(contents, hunk.Header)
		for li := range hunk.Lines {
			refs = append(refs, lineRef{hunkIdx: hi, lineIdx: li})
			contents = append(contents, hunk.Lines[li].Content)
		}
	}

	highlighted := highlightLines(contents, filename)

	var result []renderedDiffLine
	for i, ref := range refs {
		if ref.isSep {
			result = append(result, renderedDiffLine{
				cells: styledString("───", tcell.StyleDefault.Foreground(diffDividerFG)),
			})
			continue
		}
		if ref.isHunk {
			result = append(result, renderedDiffLine{
				cells: styledString(contents[i], tcell.StyleDefault.Foreground(diffHunkFG).Italic(true)),
			})
			continue
		}

		dl := pd.Hunks[ref.hunkIdx].Lines[ref.lineIdx]
		hl := highlighted[i]

		// Build line number portion.
		numStyle := tcell.StyleDefault.Foreground(diffLineNumFG)
		var numCells []styledChar
		numCells = append(numCells, styledString(gitutil.FormatLineNum(dl.OldNum, lineNumWidth), numStyle)...)
		numCells = append(numCells, styledChar{ch: ' ', style: numStyle})
		numCells = append(numCells, styledString(gitutil.FormatLineNum(dl.NewNum, lineNumWidth), numStyle)...)
		numCells = append(numCells, styledChar{ch: ' ', style: tcell.StyleDefault})

		switch dl.Type {
		case gitutil.DiffRemoved:
			prefixStyle := tcell.StyleDefault.Foreground(diffRemovedFG).Background(diffRemovedBG)
			numCells = append(numCells, styledChar{ch: '-', style: prefixStyle})
			contentCells := applyDiffBG(hl.cells, diffRemovedBG)
			result = append(result, renderedDiffLine{cells: append(numCells, contentCells...)})
		case gitutil.DiffAdded:
			prefixStyle := tcell.StyleDefault.Foreground(diffAddedFG).Background(diffAddedBG)
			numCells = append(numCells, styledChar{ch: '+', style: prefixStyle})
			contentCells := applyDiffBG(hl.cells, diffAddedBG)
			result = append(result, renderedDiffLine{cells: append(numCells, contentCells...)})
		default:
			numCells = append(numCells, styledChar{ch: ' ', style: tcell.StyleDefault})
			result = append(result, renderedDiffLine{cells: append(numCells, hl.cells...)})
		}
	}

	return result
}

// buildSideBySideDiffLines creates syntax-highlighted side-by-side diff output.
func buildSideBySideDiffLines(pd gitutil.ParsedDiff, filename string, totalW int) []renderedDiffLine {
	rows := gitutil.BuildSideBySide(pd)
	if len(rows) == 0 {
		return nil
	}

	// Each side: lineNumWidth + 1(space after num) + 1(+/- prefix) + contentW + 1(trailing pad)
	// Middle divider: 1 char ("│")
	sideW := (totalW - 1) / 2
	contentW := sideW - lineNumWidth - 3 // 3 = space + prefix + pad
	if contentW < 5 {
		contentW = 5
	}

	// Collect all text for batch highlighting.
	leftTexts := make([]string, len(rows))
	rightTexts := make([]string, len(rows))
	for i, r := range rows {
		leftTexts[i] = r.LeftText
		rightTexts[i] = r.RightText
	}
	leftHL := highlightLines(leftTexts, filename)
	rightHL := highlightLines(rightTexts, filename)

	numStyle := tcell.StyleDefault.Foreground(diffLineNumFG)
	dividerStyle := tcell.StyleDefault.Foreground(diffDividerFG)
	hunkStyle := tcell.StyleDefault.Foreground(diffHunkFG).Italic(true)

	var result []renderedDiffLine
	for i, row := range rows {
		// Hunk header
		if row.LeftNum == 0 && row.RightNum == 0 && len(row.LeftText) > 0 && row.LeftText[0] == '@' {
			result = append(result, renderedDiffLine{
				cells: styledString(truncStr(row.LeftText, totalW), hunkStyle),
			})
			continue
		}
		// Separator
		if row.LeftNum == 0 && row.RightNum == 0 && row.LeftText == "───" {
			result = append(result, renderedDiffLine{
				cells: styledString(strings.Repeat("─", totalW), dividerStyle),
			})
			continue
		}

		var line []styledChar

		// Left side
		line = append(line, styledString(gitutil.FormatLineNum(row.LeftNum, lineNumWidth), numStyle)...)
		line = append(line, styledChar{ch: ' ', style: numStyle})
		line = append(line, buildSideContent(leftHL[i], row.LeftType, contentW)...)

		// Divider
		line = append(line, styledChar{ch: '│', style: dividerStyle})

		// Right side
		line = append(line, styledString(gitutil.FormatLineNum(row.RightNum, lineNumWidth), numStyle)...)
		line = append(line, styledChar{ch: ' ', style: numStyle})
		line = append(line, buildSideContent(rightHL[i], row.RightType, contentW)...)

		result = append(result, renderedDiffLine{cells: line})
	}

	return result
}

// buildSideContent renders one side of a side-by-side diff line.
func buildSideContent(hl highlightedLine, lineType gitutil.DiffLineType, contentW int) []styledChar {
	var prefix styledChar
	var bgColor tcell.Color
	hasBG := false

	switch lineType {
	case gitutil.DiffRemoved:
		prefix = styledChar{ch: '-', style: tcell.StyleDefault.Foreground(diffRemovedFG).Background(diffRemovedBG)}
		bgColor = diffRemovedBG
		hasBG = true
	case gitutil.DiffAdded:
		prefix = styledChar{ch: '+', style: tcell.StyleDefault.Foreground(diffAddedFG).Background(diffAddedBG)}
		bgColor = diffAddedBG
		hasBG = true
	default:
		prefix = styledChar{ch: ' ', style: tcell.StyleDefault}
	}

	cells := []styledChar{prefix}

	// Truncate content to fit
	content := hl.cells
	if len(content) > contentW {
		content = content[:contentW]
	}

	if hasBG {
		cells = append(cells, applyDiffBG(content, bgColor)...)
	} else {
		cells = append(cells, content...)
	}

	// Pad to fill the column
	padLen := contentW - len(content) + 1
	if padLen > 0 {
		padStyle := tcell.StyleDefault
		if hasBG {
			padStyle = padStyle.Background(bgColor)
		}
		for range padLen {
			cells = append(cells, styledChar{ch: ' ', style: padStyle})
		}
	}

	return cells
}

// applyDiffBG overlays a background color on syntax-highlighted cells.
// Cells that already have an explicit background keep it; cells with
// default background get the diff background.
func applyDiffBG(cells []styledChar, bg tcell.Color) []styledChar {
	result := make([]styledChar, len(cells))
	for i, c := range cells {
		result[i] = styledChar{
			ch:    c.ch,
			style: c.style.Background(bg),
		}
	}
	return result
}

// drawStyledLine paints a slice of styledChars to the screen at the given position.
func drawStyledLine(screen tcell.Screen, x, y, maxW int, cells []styledChar) {
	for i, c := range cells {
		if i >= maxW {
			break
		}
		screen.SetContent(x+i, y, c.ch, nil, c.style)
	}
}

// styledString converts a plain string to styled characters with a uniform style.
func styledString(s string, style tcell.Style) []styledChar {
	cells := make([]styledChar, 0, len(s))
	for _, r := range s {
		cells = append(cells, styledChar{ch: r, style: style})
	}
	return cells
}

// truncStr truncates a string to at most maxW runes.
func truncStr(s string, maxW int) string {
	runes := []rune(s)
	if len(runes) <= maxW {
		return s
	}
	return string(runes[:maxW])
}

