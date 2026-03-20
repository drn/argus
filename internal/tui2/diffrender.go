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

	// Word-level highlight: brighter backgrounds for the specific changed spans.
	diffRemovedWordBG = tcell.NewRGBColor(110, 30, 35) // #6e1e23
	diffAddedWordBG   = tcell.NewRGBColor(30, 100, 50) // #1e6432
)

// renderedDiffLine is a pre-rendered diff line as styled cells, ready to paint.
type renderedDiffLine struct {
	cells []styledChar
}

// buildUnifiedDiffLines creates syntax-highlighted unified diff output from a
// parsed diff and filename. Each line includes line numbers, +/- prefix, and
// syntax-highlighted content with appropriate background colors. Paired
// removed+added lines get word-level highlighting on the changed spans.
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

	// Pre-compute word diff spans for paired removed+added blocks.
	wordSpans := computeWordSpansForHunks(pd)

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
			if spans, ok := wordSpans[[2]int{ref.hunkIdx, ref.lineIdx}]; ok {
				contentCells = applyWordHighlight(contentCells, spans, diffRemovedWordBG)
			}
			result = append(result, renderedDiffLine{cells: append(numCells, contentCells...)})
		case gitutil.DiffAdded:
			prefixStyle := tcell.StyleDefault.Foreground(diffAddedFG).Background(diffAddedBG)
			numCells = append(numCells, styledChar{ch: '+', style: prefixStyle})
			contentCells := applyDiffBG(hl.cells, diffAddedBG)
			if spans, ok := wordSpans[[2]int{ref.hunkIdx, ref.lineIdx}]; ok {
				contentCells = applyWordHighlight(contentCells, spans, diffAddedWordBG)
			}
			result = append(result, renderedDiffLine{cells: append(numCells, contentCells...)})
		default:
			numCells = append(numCells, styledChar{ch: ' ', style: tcell.StyleDefault})
			result = append(result, renderedDiffLine{cells: append(numCells, hl.cells...)})
		}
	}

	return result
}

// computeWordSpansForHunks pairs consecutive removed+added blocks within each
// hunk and returns word-level diff spans keyed by (hunkIdx, lineIdx).
func computeWordSpansForHunks(pd gitutil.ParsedDiff) map[[2]int][]gitutil.DiffSpan {
	result := make(map[[2]int][]gitutil.DiffSpan)

	for hi, hunk := range pd.Hunks {
		lines := hunk.Lines
		i := 0
		for i < len(lines) {
			if lines[i].Type != gitutil.DiffRemoved {
				i++
				continue
			}

			// Collect consecutive removed lines
			removedStart := i
			for i < len(lines) && lines[i].Type == gitutil.DiffRemoved {
				i++
			}

			// Collect consecutive added lines
			addedStart := i
			for i < len(lines) && lines[i].Type == gitutil.DiffAdded {
				i++
			}

			// Pair removed and added lines
			nRemoved := addedStart - removedStart
			nAdded := i - addedStart
			pairs := min(nRemoved, nAdded)
			for k := 0; k < pairs; k++ {
				oldContent := lines[removedStart+k].Content
				newContent := lines[addedStart+k].Content
				oldSpans, newSpans := gitutil.WordDiff(oldContent, newContent)
				if len(oldSpans) > 0 {
					result[[2]int{hi, removedStart + k}] = oldSpans
				}
				if len(newSpans) > 0 {
					result[[2]int{hi, addedStart + k}] = newSpans
				}
			}
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

	// Pre-compute word diff spans for paired rows.
	type rowWordSpans struct {
		leftSpans  []gitutil.DiffSpan
		rightSpans []gitutil.DiffSpan
	}
	wordSpans := make(map[int]rowWordSpans)
	for i, row := range rows {
		if row.LeftType == gitutil.DiffRemoved && row.RightType == gitutil.DiffAdded {
			oldSpans, newSpans := gitutil.WordDiff(row.LeftText, row.RightText)
			if len(oldSpans) > 0 || len(newSpans) > 0 {
				wordSpans[i] = rowWordSpans{leftSpans: oldSpans, rightSpans: newSpans}
			}
		}
	}

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

		ws := wordSpans[i]

		var line []styledChar

		// Left side
		line = append(line, styledString(gitutil.FormatLineNum(row.LeftNum, lineNumWidth), numStyle)...)
		line = append(line, styledChar{ch: ' ', style: numStyle})
		line = append(line, buildSideContentWithWordHL(leftHL[i], row.LeftType, contentW, ws.leftSpans)...)

		// Divider
		line = append(line, styledChar{ch: '│', style: dividerStyle})

		// Right side
		line = append(line, styledString(gitutil.FormatLineNum(row.RightNum, lineNumWidth), numStyle)...)
		line = append(line, styledChar{ch: ' ', style: numStyle})
		line = append(line, buildSideContentWithWordHL(rightHL[i], row.RightType, contentW, ws.rightSpans)...)

		result = append(result, renderedDiffLine{cells: line})
	}

	return result
}

// buildSideContentWithWordHL renders one side of a side-by-side diff line with
// optional word-level highlighting for changed spans.
func buildSideContentWithWordHL(hl highlightedLine, lineType gitutil.DiffLineType, contentW int, wordSpans []gitutil.DiffSpan) []styledChar {
	cells := buildSideContent(hl, lineType, contentW)
	if len(wordSpans) == 0 {
		return cells
	}

	// The content starts after the prefix char (index 1).
	// Apply word highlight to the content portion.
	wordBG := diffRemovedWordBG
	if lineType == gitutil.DiffAdded {
		wordBG = diffAddedWordBG
	}
	for _, span := range wordSpans {
		for j := span.Start; j < span.End; j++ {
			idx := j + 1 // +1 for the prefix char
			if idx >= len(cells) {
				break
			}
			cells[idx] = styledChar{
				ch:    cells[idx].ch,
				style: cells[idx].style.Background(wordBG),
			}
		}
	}
	return cells
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

// applyWordHighlight applies a brighter background to specific character spans
// within already-styled cells, for word-level diff highlighting.
func applyWordHighlight(cells []styledChar, spans []gitutil.DiffSpan, bg tcell.Color) []styledChar {
	result := make([]styledChar, len(cells))
	copy(result, cells)
	for _, span := range spans {
		for j := span.Start; j < span.End && j < len(result); j++ {
			result[j] = styledChar{
				ch:    result[j].ch,
				style: result[j].style.Background(bg),
			}
		}
	}
	return result
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

