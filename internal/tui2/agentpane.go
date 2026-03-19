package tui2

// agentpane.go — shared drawing utilities for the tui2 package.
// The AgentPane placeholder (Phase 2) has been replaced by TerminalPane (Phase 3).

import (
	"regexp"

	"github.com/gdamore/tcell/v2"
)

// ansiRe matches ANSI escape sequences (CSI, OSC, simple escapes).
var ansiRe = regexp.MustCompile(`\x1b(?:\[[0-9;]*[a-zA-Z]|\][^\x07]*\x07|\[[^\x1b]*|[()][0-9A-B]|[78DEHM])`)

// splitLines strips ANSI escape sequences, then splits the result into
// display lines, wrapping at maxWidth.
func splitLines(data []byte, maxWidth int) []string {
	if maxWidth <= 0 {
		maxWidth = 80
	}
	clean := ansiRe.ReplaceAll(data, nil)

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

// drawBorder draws a Unicode box border.
func drawBorder(screen tcell.Screen, x, y, w, h int, style tcell.Style) {
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

// innerRect holds the content area inside a bordered panel.
type innerRect struct {
	X, Y, W, H int
}

// drawBorderedPanel draws a rounded border at (x, y, w, h) with an optional
// title embedded in the top border, and returns the inner content rect.
// All bordered panels should use this to guarantee consistent chrome.
func drawBorderedPanel(screen tcell.Screen, x, y, w, h int, title string, style tcell.Style) innerRect {
	drawBorder(screen, x, y, w, h, style)
	if title != "" {
		for i, r := range title {
			if x+1+i < x+w-1 {
				screen.SetContent(x+1+i, y, r, nil, style.Bold(true))
			}
		}
	}
	return innerRect{X: x + 1, Y: y + 1, W: w - 2, H: h - 2}
}
