package tui

import (
	"github.com/gdamore/tcell/v2"
)

var todoBannerLines = [...]string{
	`███████  ███████  ██   ██`,
	`     ██  ██       ███  ██`,
	`  ███    █████    ██ █ ██`,
	`██       ██       ██  ███`,
	`███████  ███████  ██   ██`,
}

// Per-line gradient colors for the todo banner.
var todoBannerGradient = [...]tcell.Color{
	tcell.Color87,  // bright cyan
	tcell.Color81,  // light blue
	tcell.Color141, // lavender
	tcell.Color177, // light purple
	tcell.Color212, // pink
}

// todoBannerTextWidth is the rune count of each banner line, used for
// centering math. Matches the bannerTextWidth convention in banner.go.
const todoBannerTextWidth = 25

const todoSubtitle = "N O T H I N G   T O   D O"

// todoBannerHeight returns the total height of the todo banner.
func todoBannerHeight() int {
	// accent(1) + blank(1) + 5 logo lines + underline(1) + blank(1) + subtitle(1) + blank(1) + accent(1) = 12
	return 12
}

// drawTodoBanner draws the To Dos tab empty-state banner centered at the
// given y offset. Returns the number of rows consumed.
func drawTodoBanner(screen tcell.Screen, x, y, width int) int {
	if width <= 0 {
		return 0
	}

	row := y

	// Top accent line.
	drawFadingAccent(screen, x, row, width, todoBannerTextWidth)
	row++
	row++ // blank line

	// Main banner text with per-line gradient.
	for i, line := range todoBannerLines {
		padLeft := (width - todoBannerTextWidth) / 2
		if padLeft < 0 {
			padLeft = 0
		}
		style := tcell.StyleDefault.Foreground(todoBannerGradient[i]).Bold(true)
		drawText(screen, x+padLeft, row, width-padLeft, line, style)
		row++
	}

	// Gradient underline beneath banner.
	drawGradientUnderline(screen, x, row, width, todoBannerTextWidth, todoBannerGradient[:])
	row++
	row++ // blank line

	// Subtitle.
	subPad := (width - len(todoSubtitle)) / 2
	if subPad < 0 {
		subPad = 0
	}
	drawText(screen, x+subPad, row, width-subPad, todoSubtitle, tcell.StyleDefault.Foreground(ColorDimmed))
	row++
	row++ // blank line

	// Bottom accent line.
	drawFadingAccent(screen, x, row, width, todoBannerTextWidth)
	row++

	return row - y
}
