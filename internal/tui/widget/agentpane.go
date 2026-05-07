package widget

// agentpane.go — shared drawing utilities for the tui package.
// The AgentPane placeholder (Phase 2) has been replaced by TerminalPane (Phase 3).

import (
	"regexp"

	"github.com/gdamore/tcell/v2"
)

// AnsiRe matches ANSI escape sequences (CSI, OSC, simple escapes).
// CSI sequences: \x1b[ <params 0x20-0x3f>* <final 0x40-0x7e>
// OSC sequences are terminated by either BEL (\x07) or ST (\x1b\\).
// NOTE: For link extraction, osc8Re in internal/links must run BEFORE the
// general ANSI regex to preserve URLs embedded in OSC 8 hyperlink tags
// (AnsiRe / links.ansiRe strip them).
var AnsiRe = regexp.MustCompile(`\x1b(?:\[[\x20-\x3f]*[\x40-\x7e]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[()][0-9A-B]|[78DEHM])`)

// splitLines strips ANSI escape sequences, then splits the result into
// display lines, wrapping at maxWidth.
func splitLines(data []byte, maxWidth int) []string {
	if maxWidth <= 0 {
		maxWidth = 80
	}
	// All ANSI → empty here (display wrapping, not URL extraction).
	// stripANSI in internal/links uses a different strategy for link
	// extraction: SGR → empty (preserves mid-URL colors), non-SGR → space
	// (prevents text from different screen positions merging into URLs).
	clean := AnsiRe.ReplaceAll(data, nil)

	var lines []string
	var current []rune
	for _, b := range clean {
		switch b {
		case '\n':
			lines = append(lines, string(current))
			current = current[:0]
		case '\r', '\x1b':
			// skip leftover escape chars and carriage returns
		default:
			if b < 0x20 {
				continue
			}
			current = append(current, rune(b))
			if len(current) >= maxWidth {
				lines = append(lines, string(current))
				current = current[:0]
			}
		}
	}
	if len(current) > 0 {
		lines = append(lines, string(current))
	}
	return lines
}

// DrawBorder draws a Unicode box border.
func DrawBorder(screen tcell.Screen, x, y, w, h int, style tcell.Style) {
	if w < 2 || h < 2 {
		return
	}
	screen.SetContent(x, y, '╭', nil, style)
	screen.SetContent(x+w-1, y, '╮', nil, style)
	screen.SetContent(x, y+h-1, '╰', nil, style)
	screen.SetContent(x+w-1, y+h-1, '╯', nil, style)
	for col := x + 1; col < x+w-1; col++ {
		screen.SetContent(col, y, '─', nil, style)
		screen.SetContent(col, y+h-1, '─', nil, style)
	}
	for row := y + 1; row < y+h-1; row++ {
		screen.SetContent(x, row, '│', nil, style)
		screen.SetContent(x+w-1, row, '│', nil, style)
	}
}

// InnerRect holds the content area inside a bordered panel.
type InnerRect struct {
	X, Y, W, H int
}

// DrawBorderedPanel draws a rounded border at (x, y, w, h) with an optional
// title embedded in the top border, and returns the inner content rect. All
// bordered panels should use this to guarantee consistent chrome.
//
// The interior is blanked with (' ', tcell.StyleDefault) before the border
// is drawn. tview's screen.Clear() already does this screen-wide each
// frame, so the fill is defense-in-depth: if a future optimization ever
// bypasses Clear for partial redraws, widgets that only paint their
// occupied rows would otherwise leak stale cells from the previous frame.
// The fill style is hardcoded to tcell.StyleDefault because every current
// caller wants a transparent interior — if a future bordered panel ever
// lives on top of a tinted layer (e.g., a modal overlay with a coloured
// background), this helper will need a fillStyle parameter.
//
// When w or h is below the 2x2 minimum required for a border, the returned
// InnerRect is the zero value so callers can short-circuit on
// `inner.W <= 0 || inner.H <= 0`.
func DrawBorderedPanel(screen tcell.Screen, x, y, w, h int, title string, style tcell.Style) InnerRect {
	if w < 2 || h < 2 {
		return InnerRect{}
	}
	FillArea(screen, x+1, y+1, w-2, h-2, ' ', tcell.StyleDefault)
	DrawBorder(screen, x, y, w, h, style)
	if title != "" {
		for i, r := range title {
			if x+1+i < x+w-1 {
				screen.SetContent(x+1+i, y, r, nil, style.Bold(true))
			}
		}
	}
	return InnerRect{X: x + 1, Y: y + 1, W: w - 2, H: h - 2}
}
