package ui

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/hinshun/vt10x"
)

// vt10x attribute bit flags (unexported in the library)
const (
	vtAttrReverse   = 1 << 0
	vtAttrUnderline = 1 << 1
	vtAttrBold      = 1 << 2
	// attrGfx      = 1 << 3
	vtAttrItalic = 1 << 4
	// attrBlink    = 1 << 5
)

// replayVT10X replays raw terminal output through a virtual terminal and
// returns the rendered lines with ANSI colors. cursorVisible controls whether
// the cursor position is rendered with reverse video.
func replayVT10X(raw []byte, vtCols, vtRows int, cursorVisible bool) []string {
	vt := vt10x.New(vt10x.WithSize(vtCols, vtRows))
	vt.Write(raw)

	vt.Lock()
	defer vt.Unlock()

	cur := vt.Cursor()
	// Always show cursor regardless of CursorVisible() — TUI agents like
	// Claude Code hide the hardware cursor (\x1b[?25l) but we still want
	// to show the cursor position in the agent view.
	showCursor := cursorVisible

	// Find the row to highlight as the active input line. Claude (Ink-based)
	// parks the cursor at an empty trailing row; findInputRow scans upward to
	// the last row with visible content so the highlight lands on the real
	// input row rather than the empty parking spot.
	inputRow := -1
	if showCursor {
		inputRow = findInputRow(vt, cur.Y, vtCols)
	}

	var lines []string
	for y := 0; y < vtRows; y++ {
		cursorX := -1
		// Only render the cursor block on the input row. When the cursor is
		// parked on a different (empty) row — as Claude (Ink) does — rendering
		// the cursor there produces a stray white block below the real input.
		if showCursor && y == cur.Y && cur.Y == inputRow {
			cursorX = cur.X
		}
		line := renderLine(vt, y, vtCols, cursorX, y == inputRow)
		lines = append(lines, line)
	}

	// Trim trailing empty lines. The inputRow has visible content so it is
	// never trimmed. The cursor parking row (cur.Y when cur.Y != inputRow) is
	// now empty and trimmed away — no stray blank row below the input.
	for len(lines) > 0 && stripANSI(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	// Trim leading empty lines (e.g. Codex positions its TUI content in the
	// lower portion of the terminal, leaving the top rows blank).
	for len(lines) > 0 && stripANSI(lines[0]) == "" {
		lines = lines[1:]
	}

	return lines
}

// estimateVTRows estimates the number of virtual terminal rows needed to
// capture all output, given the raw bytes and display dimensions.
func estimateVTRows(raw []byte, vtCols, dispH int) int {
	vtRows := dispH
	if n := bytes.Count(raw, []byte{'\n'}); n > vtRows {
		vtRows = n + dispH
	}
	if vtCols > 0 {
		wrappedEstimate := len(raw)/vtCols + dispH
		if wrappedEstimate > vtRows {
			vtRows = wrappedEstimate
		}
	}
	return vtRows
}

const (
	// activeInputBG is a subtle row highlight for the live input line in the
	// agent terminal. It makes the editable row read as an input field rather
	// than passive terminal output.
	activeInputBG vt10x.Color = 17
	cursorFG      vt10x.Color = 17
	cursorBG      vt10x.Color = 153
)

// promptMarker is the Unicode character Claude Code (Ink-based) renders at
// the start of its input prompt row. Scanning for it lets findInputRow land
// exactly on the input row rather than a tip/hint row below it.
const promptMarker = '❯'

// findInputRow returns the row to highlight as the active input line.
// For agents like Claude Code (Ink-based), the cursor is parked at an empty
// trailing row while the actual input content is above it. We scan upward
// from curY looking first for a row that starts with the prompt marker (❯),
// which precisely identifies the Claude input row. If no prompt marker is
// found, we fall back to the last non-empty row so Codex (cursor on input)
// and other agents still work correctly.
func findInputRow(vt vt10x.Terminal, curY, cols int) int {
	lastNonEmpty := -1
	for y := curY; y >= 0; y-- {
		for x := 0; x < cols; x++ {
			cell := vt.Cell(x, y)
			ch := cell.Char
			if ch == promptMarker {
				return y
			}
			if (ch != 0 && ch != ' ') || cell.BG != vt10x.DefaultBG || cell.FG != vt10x.DefaultFG {
				if lastNonEmpty == -1 {
					lastNonEmpty = y
				}
			}
		}
	}
	if lastNonEmpty != -1 {
		return lastNonEmpty
	}
	return curY // fallback: entire screen is empty
}

// renderLine builds a single line from the vt10x screen with ANSI colors.
// cursorX is the column to render a cursor at (-1 for no cursor on this line).
// isInputRow controls the blue background highlight for the active input line;
// it is determined by the caller (usually via findInputRow) and is independent
// of cursorX so that the cursor parking row and the input row can differ.
func renderLine(vt vt10x.Terminal, y, cols int, cursorX int, isInputRow bool) string {
	var b strings.Builder
	var curFG, curBG vt10x.Color
	var curMode int16
	active := false
	activeLine := isInputRow

	lastCol := -1
	for x := cols - 1; x >= 0; x-- {
		cell := vt.Cell(x, y)
		ch := cell.Char
		if ch == 0 {
			ch = ' '
		}
		if ch != ' ' || cell.FG != vt10x.DefaultFG || cell.BG != vt10x.DefaultBG || cell.Mode != 0 {
			lastCol = x
			break
		}
	}
	if cursorX > lastCol {
		lastCol = cursorX
	}
	if activeLine && cols > 0 {
		lastCol = cols - 1
	}

	for x := 0; x <= lastCol; x++ {
		cell := vt.Cell(x, y)
		ch := cell.Char
		if ch == 0 {
			ch = ' '
		}

		if x == cursorX {
			// Use an explicit high-contrast cursor so it stays legible on the
			// highlighted input row and against the panel background.
			b.WriteString(buildSGR(cursorFG, cursorBG, 0))
			b.WriteRune(ch)
			b.WriteString("\x1b[0m")
			active = false
		} else {
			if cell.FG != curFG || cell.BG != curBG || cell.Mode != curMode || !active {
				b.WriteString(buildSGRWithActiveLine(cell.FG, cell.BG, cell.Mode, activeLine))
				curFG = cell.FG
				curBG = cell.BG
				curMode = cell.Mode
				active = true
			}
			b.WriteRune(ch)
		}
	}

	if active {
		b.WriteString("\x1b[0m")
	}

	return b.String()
}

// buildSGR builds an ANSI SGR escape sequence for the given attributes.
func buildSGR(fg, bg vt10x.Color, mode int16) string {
	return buildSGRWithActiveLine(fg, bg, mode, false)
}

// buildSGRWithActiveLine builds an ANSI SGR escape sequence for the given
// attributes. When activeLine is true, default-background cells inherit the
// input-row highlight so the editable line is visually separated from
// surrounding text.
func buildSGRWithActiveLine(fg, bg vt10x.Color, mode int16, activeLine bool) string {
	var params []string

	params = append(params, "0")

	if mode&vtAttrBold != 0 {
		params = append(params, "1")
	}
	if mode&vtAttrItalic != 0 {
		params = append(params, "3")
	}
	if mode&vtAttrUnderline != 0 {
		params = append(params, "4")
	}
	if mode&vtAttrReverse != 0 {
		params = append(params, "7")
	}

	if fg != vt10x.DefaultFG {
		params = append(params, sgrColor(fg, 30))
	}
	// Only tint blank cells (no FG, no BG, no mode set). Cells with any
	// explicit styling — including Claude Code's Ink cursor indicator — keep
	// their original colors so they remain legible on the highlighted row.
	if activeLine && fg == vt10x.DefaultFG && bg == vt10x.DefaultBG && mode == 0 {
		bg = activeInputBG
	}
	if bg != vt10x.DefaultBG {
		params = append(params, sgrColor(bg, 40))
	}

	return "\x1b[" + strings.Join(params, ";") + "m"
}

// sgrColor returns the SGR parameter string for a color.
// base is 30 for foreground, 40 for background.
func sgrColor(c vt10x.Color, base int) string {
	n := uint32(c)
	switch {
	case n < 8:
		return fmt.Sprintf("%d", base+int(n))
	case n < 16:
		return fmt.Sprintf("%d", base+60+int(n)-8)
	case n < 256:
		prefix := 38
		if base == 40 {
			prefix = 48
		}
		return fmt.Sprintf("%d;5;%d", prefix, n)
	default:
		prefix := 38
		if base == 40 {
			prefix = 48
		}
		r, g, b := (n>>16)&0xFF, (n>>8)&0xFF, n&0xFF
		return fmt.Sprintf("%d;2;%d;%d;%d", prefix, r, g, b)
	}
}

// stripANSI removes ANSI escape sequences and trims whitespace for emptiness check.
func stripANSI(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && !((s[j] >= 'A' && s[j] <= 'Z') || (s[j] >= 'a' && s[j] <= 'z')) {
				j++
			}
			if j < len(s) {
				j++
			}
			i = j
		} else {
			b.WriteByte(s[i])
			i++
		}
	}
	return strings.TrimSpace(b.String())
}
