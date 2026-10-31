package widget

import (
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/gitutil"
)

// lineNumWidth is the number of characters reserved for line numbers.
const lineNumWidth = 4

// unifiedGutterW covers two line numbers, their separators, and the +/- prefix.
const unifiedGutterW = lineNumWidth*2 + 3

// minContentW floors wrapped content width so narrow panes don't emit one rune per row.
const minContentW = 5

// sidePad is the per-side prefix cell plus trailing pad cell around content.
const sidePad = 2

// wrapCells splits cells into rows of at most w; empty input yields one row to keep blank lines.
func wrapCells(cells []StyledChar, w int) [][]StyledChar {
	if w < 1 {
		w = 1
	}
	if len(cells) <= w {
		return [][]StyledChar{cells}
	}
	var rows [][]StyledChar
	for len(cells) > w {
		rows = append(rows, cells[:w:w])
		cells = cells[w:]
	}
	return append(rows, cells)
}

// blankCells returns n spaces in the given style.
func blankCells(n int, style tcell.Style) []StyledChar {
	cells := make([]StyledChar, n)
	for i := range cells {
		cells[i] = StyledChar{Ch: ' ', Style: style}
	}
	return cells
}

// Diff colors. Backgrounds use fixed RGB for consistent diff tinting regardless
// of terminal theme. Foregrounds use palette indices so they adapt to the theme.
var (
	diffRemovedBG = tcell.NewRGBColor(61, 16, 18) // #3d1012
	diffAddedBG   = tcell.NewRGBColor(13, 51, 23) // #0d3317
	diffRemovedFG = tcell.PaletteColor(203)       // red
	diffAddedFG   = tcell.PaletteColor(78)        // green
	diffLineNumFG = tcell.PaletteColor(239)       // dim gray
	diffHunkFG    = tcell.PaletteColor(243)       // medium gray
	diffDividerFG = tcell.PaletteColor(236)       // dark gray

	// Word-level highlight: brighter backgrounds for the specific changed spans.
	diffRemovedWordBG = tcell.NewRGBColor(110, 30, 35) // #6e1e23
	diffAddedWordBG   = tcell.NewRGBColor(30, 100, 50) // #1e6432
)

// RenderedDiffLine is a pre-rendered diff line as styled cells, ready to paint.
type RenderedDiffLine struct {
	Cells []StyledChar
}

// BuildUnifiedDiffLines creates syntax-highlighted unified diff output from a
// parsed diff and filename. Each line includes line numbers, +/- prefix, and
// syntax-highlighted content with appropriate background colors. Paired
// removed+added lines get word-level highlighting. Lines wider than totalW wrap.
func BuildUnifiedDiffLines(pd gitutil.ParsedDiff, filename string, totalW int) []RenderedDiffLine {
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

	highlighted := HighlightLines(contents, filename)

	// Pre-compute word diff spans for paired removed+added blocks.
	wordSpans := computeWordSpansForHunks(pd)

	contentW := max(totalW-unifiedGutterW, minContentW)

	var result []RenderedDiffLine
	for i, ref := range refs {
		if ref.isSep {
			result = append(result, RenderedDiffLine{
				Cells: styledString("───", tcell.StyleDefault.Foreground(diffDividerFG)),
			})
			continue
		}
		if ref.isHunk {
			hunkCells := styledString(contents[i], tcell.StyleDefault.Foreground(diffHunkFG).Italic(true))
			for _, row := range wrapCells(hunkCells, max(totalW, minContentW)) {
				result = append(result, RenderedDiffLine{Cells: row})
			}
			continue
		}

		dl := pd.Hunks[ref.hunkIdx].Lines[ref.lineIdx]
		hl := highlighted[i]

		// Build line number portion.
		numStyle := tcell.StyleDefault.Foreground(diffLineNumFG)
		var numCells []StyledChar
		numCells = append(numCells, styledString(gitutil.FormatLineNum(dl.OldNum, lineNumWidth), numStyle)...)
		numCells = append(numCells, StyledChar{Ch: ' ', Style: numStyle})
		numCells = append(numCells, styledString(gitutil.FormatLineNum(dl.NewNum, lineNumWidth), numStyle)...)
		numCells = append(numCells, StyledChar{Ch: ' ', Style: tcell.StyleDefault})

		switch dl.Type {
		case gitutil.DiffRemoved:
			prefixStyle := tcell.StyleDefault.Foreground(diffRemovedFG).Background(diffRemovedBG)
			numCells = append(numCells, StyledChar{Ch: '-', Style: prefixStyle})
			contentCells := applyDiffBG(hl.Cells, diffRemovedBG)
			if spans, ok := wordSpans[[2]int{ref.hunkIdx, ref.lineIdx}]; ok {
				contentCells = applyWordHighlight(contentCells, spans, diffRemovedWordBG)
			}
			result = appendWrappedUnified(result, numCells, contentCells, contentW, prefixStyle)
		case gitutil.DiffAdded:
			prefixStyle := tcell.StyleDefault.Foreground(diffAddedFG).Background(diffAddedBG)
			numCells = append(numCells, StyledChar{Ch: '+', Style: prefixStyle})
			contentCells := applyDiffBG(hl.Cells, diffAddedBG)
			if spans, ok := wordSpans[[2]int{ref.hunkIdx, ref.lineIdx}]; ok {
				contentCells = applyWordHighlight(contentCells, spans, diffAddedWordBG)
			}
			result = appendWrappedUnified(result, numCells, contentCells, contentW, prefixStyle)
		default:
			numCells = append(numCells, StyledChar{Ch: ' ', Style: tcell.StyleDefault})
			result = appendWrappedUnified(result, numCells, hl.Cells, contentW, tcell.StyleDefault)
		}
	}

	return result
}

// appendWrappedUnified wraps content to contentW; continuation rows keep the prefix-cell tint.
func appendWrappedUnified(result []RenderedDiffLine, gutter, content []StyledChar, contentW int, prefixStyle tcell.Style) []RenderedDiffLine {
	for i, row := range wrapCells(content, contentW) {
		if i == 0 {
			result = append(result, RenderedDiffLine{Cells: append(gutter, row...)})
			continue
		}
		cont := blankCells(unifiedGutterW-1, tcell.StyleDefault)
		cont = append(cont, StyledChar{Ch: ' ', Style: prefixStyle})
		result = append(result, RenderedDiffLine{Cells: append(cont, row...)})
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

// BuildSideBySideDiffLines creates syntax-highlighted side-by-side diff output.
// Content wider than a column wraps; the shorter side is padded to keep the divider aligned.
func BuildSideBySideDiffLines(pd gitutil.ParsedDiff, filename string, totalW int) []RenderedDiffLine {
	rows := gitutil.BuildSideBySide(pd)
	if len(rows) == 0 {
		return nil
	}

	// Each side: lineNumWidth + 1(space after num) + 1(+/- prefix) + contentW + 1(trailing pad)
	// Middle divider: 1 char ("│")
	sideW := (totalW - 1) / 2
	contentW := max(sideW-lineNumWidth-3, minContentW) // 3 = space + prefix + pad

	// Collect all text for batch highlighting.
	leftTexts := make([]string, len(rows))
	rightTexts := make([]string, len(rows))
	for i, r := range rows {
		leftTexts[i] = r.LeftText
		rightTexts[i] = r.RightText
	}
	leftHL := HighlightLines(leftTexts, filename)
	rightHL := HighlightLines(rightTexts, filename)

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

	var result []RenderedDiffLine
	for i, row := range rows {
		// Hunk header
		if row.LeftNum == 0 && row.RightNum == 0 && len(row.LeftText) > 0 && row.LeftText[0] == '@' {
			for _, r := range wrapCells(styledString(row.LeftText, hunkStyle), max(totalW, minContentW)) {
				result = append(result, RenderedDiffLine{Cells: r})
			}
			continue
		}
		// Separator
		if row.LeftNum == 0 && row.RightNum == 0 && row.LeftText == "───" {
			result = append(result, RenderedDiffLine{
				Cells: styledString(strings.Repeat("─", totalW), dividerStyle),
			})
			continue
		}

		ws := wordSpans[i]

		left := buildSideRows(leftHL[i], row.LeftType, contentW, ws.leftSpans)
		right := buildSideRows(rightHL[i], row.RightType, contentW, ws.rightSpans)
		for k := range max(len(left), len(right)) {
			var line []StyledChar
			line = append(line, sideGutter(row.LeftNum, k == 0, numStyle)...)
			line = append(line, sideRowOrBlank(left, k, contentW)...)
			line = append(line, StyledChar{Ch: '│', Style: dividerStyle})
			line = append(line, sideGutter(row.RightNum, k == 0, numStyle)...)
			line = append(line, sideRowOrBlank(right, k, contentW)...)
			result = append(result, RenderedDiffLine{Cells: line})
		}
	}

	return result
}

// sideGutter is the line-number column for one side; continuation rows blank it.
func sideGutter(num int, first bool, style tcell.Style) []StyledChar {
	if !first {
		return blankCells(lineNumWidth+1, tcell.StyleDefault)
	}
	cells := styledString(gitutil.FormatLineNum(num, lineNumWidth), style)
	return append(cells, StyledChar{Ch: ' ', Style: style})
}

// sideRowOrBlank returns wrapped row k, or blank filler when this side ran out
// of rows before the other.
func sideRowOrBlank(rows [][]StyledChar, k, contentW int) []StyledChar {
	if k < len(rows) {
		return rows[k]
	}
	return blankCells(contentW+sidePad, tcell.StyleDefault)
}

// buildSideRows wraps one side into prefix+contentW+pad rows; word spans apply before wrapping.
func buildSideRows(hl HighlightedLine, lineType gitutil.DiffLineType, contentW int, wordSpans []gitutil.DiffSpan) [][]StyledChar {
	var prefix StyledChar
	var bgColor tcell.Color
	wordBG := diffRemovedWordBG
	hasBG := false

	switch lineType {
	case gitutil.DiffRemoved:
		prefix = StyledChar{Ch: '-', Style: tcell.StyleDefault.Foreground(diffRemovedFG).Background(diffRemovedBG)}
		bgColor = diffRemovedBG
		hasBG = true
	case gitutil.DiffAdded:
		prefix = StyledChar{Ch: '+', Style: tcell.StyleDefault.Foreground(diffAddedFG).Background(diffAddedBG)}
		bgColor = diffAddedBG
		wordBG = diffAddedWordBG
		hasBG = true
	default:
		prefix = StyledChar{Ch: ' ', Style: tcell.StyleDefault}
	}

	content := hl.Cells
	if hasBG {
		content = applyDiffBG(content, bgColor)
	}
	if len(wordSpans) > 0 {
		content = applyWordHighlight(content, wordSpans, wordBG)
	}

	padStyle := tcell.StyleDefault
	if hasBG {
		padStyle = padStyle.Background(bgColor)
	}

	wrapped := wrapCells(content, contentW)
	rows := make([][]StyledChar, len(wrapped))
	for i, chunk := range wrapped {
		cells := make([]StyledChar, 0, contentW+sidePad)
		if i == 0 {
			cells = append(cells, prefix)
		} else {
			cells = append(cells, StyledChar{Ch: ' ', Style: padStyle})
		}
		cells = append(cells, chunk...)
		cells = append(cells, blankCells(contentW-len(chunk)+sidePad-1, padStyle)...)
		rows[i] = cells
	}
	return rows
}

// applyWordHighlight applies a brighter background to specific character spans
// within already-styled cells, for word-level diff highlighting.
func applyWordHighlight(cells []StyledChar, spans []gitutil.DiffSpan, bg tcell.Color) []StyledChar {
	result := make([]StyledChar, len(cells))
	copy(result, cells)
	for _, span := range spans {
		for j := span.Start; j < span.End && j < len(result); j++ {
			result[j] = StyledChar{
				Ch:    result[j].Ch,
				Style: result[j].Style.Background(bg),
			}
		}
	}
	return result
}

// applyDiffBG overlays a background color on syntax-highlighted cells.
// Cells that already have an explicit background keep it; cells with
// default background get the diff background.
func applyDiffBG(cells []StyledChar, bg tcell.Color) []StyledChar {
	result := make([]StyledChar, len(cells))
	for i, c := range cells {
		result[i] = StyledChar{
			Ch:    c.Ch,
			Style: c.Style.Background(bg),
		}
	}
	return result
}

// DrawStyledLine paints a slice of styledChars to the screen at the given position.
func DrawStyledLine(screen tcell.Screen, x, y, maxW int, cells []StyledChar) {
	for i, c := range cells {
		if i >= maxW {
			break
		}
		screen.SetContent(x+i, y, c.Ch, nil, c.Style)
	}
}

// styledString converts a plain string to styled characters with a uniform style.
func styledString(s string, style tcell.Style) []StyledChar {
	cells := make([]StyledChar, 0, len(s))
	for _, r := range s {
		cells = append(cells, StyledChar{Ch: r, Style: style})
	}
	return cells
}
