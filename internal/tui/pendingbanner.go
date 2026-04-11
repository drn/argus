package tui

import (
	"github.com/gdamore/tcell/v2"
)

var pendingBannerLines = [...]string{
	`██       █████  ██    ██ ██   ██  ██████ ██   ██`,
	`██      ██   ██ ██    ██ ███  ██ ██      ██   ██`,
	`██      ███████ ██    ██ ██ █ ██ ██      ███████`,
	`██      ██   ██ ██    ██ ██  ███ ██      ██   ██`,
	`███████ ██   ██  ██████  ██   ██  ██████ ██   ██`,
}

// Per-line gradient colors for the pending banner.
var pendingBannerGradient = [...]tcell.Color{
	tcell.Color87,  // bright cyan
	tcell.Color81,  // light blue
	tcell.Color141, // lavender
	tcell.Color177, // light purple
	tcell.Color212, // pink
}

const pendingBannerTextWidth = 48

const pendingSubtitle = "P R E P A R I N G   W O R K T R E E ..."

// pendingBannerHeight returns the total height of the pending banner.
func pendingBannerHeight() int {
	// accent(1) + blank(1) + 5 logo lines + underline(1) + blank(1) + subtitle(1) + blank(1) + accent(1) = 12
	return 12
}

// drawPendingBanner draws the pending task banner centered at the given y offset.
// Returns the number of rows consumed.
func drawPendingBanner(screen tcell.Screen, x, y, width int) int {
	if width <= 0 {
		return 0
	}

	row := y

	// Top accent line.
	drawFadingAccent(screen, x, row, width, pendingBannerTextWidth)
	row++
	row++ // blank line

	// Main banner text with per-line gradient.
	for i, line := range pendingBannerLines {
		padLeft := max((width-pendingBannerTextWidth)/2, 0)
		style := tcell.StyleDefault.Foreground(pendingBannerGradient[i]).Bold(true)
		drawText(screen, x+padLeft, row, width-padLeft, line, style)
		row++
	}

	// Gradient underline beneath banner.
	drawGradientUnderline(screen, x, row, width, pendingBannerTextWidth, pendingBannerGradient[:])
	row++
	row++ // blank line

	// Subtitle.
	subPad := max((width-len(pendingSubtitle))/2, 0)
	drawText(screen, x+subPad, row, width-subPad, pendingSubtitle, tcell.StyleDefault.Foreground(ColorDimmed))
	row++
	row++ // blank line

	// Bottom accent line.
	drawFadingAccent(screen, x, row, width, pendingBannerTextWidth)
	row++

	return row - y
}
