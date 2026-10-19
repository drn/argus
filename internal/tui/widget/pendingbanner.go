package widget

import (
	"github.com/drn/argus/internal/tui/theme"
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

// Launch-stage subtitles for panes that derive the stage from task/session
// state rather than an in-process callback (Hera panes). Same letter-spaced
// style as pendingSubtitle.
const (
	LaunchSubtitlePreparing = pendingSubtitle
	LaunchSubtitleStarting  = "S T A R T I N G   A G E N T ..."
)

// PendingBannerHeight returns the total height of the pending banner.
func PendingBannerHeight() int {
	// accent(1) + blank(1) + 5 logo lines + underline(1) + blank(1) + subtitle(1) + blank(1) + accent(1) = 12
	return 12
}

// LaunchBannerMinWidth returns the narrowest pane the full wordmark fits in
// without being right-clipped.
func LaunchBannerMinWidth() int { return pendingBannerTextWidth }

// DrawPendingBanner draws the pending task banner centered at the given y offset.
// Returns the number of rows consumed.
func DrawPendingBanner(screen tcell.Screen, x, y, width int) int {
	return DrawLaunchBanner(screen, x, y, width, pendingSubtitle)
}

// DrawLaunchBanner is DrawPendingBanner with a caller-chosen status line.
// It paints only inside [x, x+width) and the PendingBannerHeight() rows
// starting at y; callers must guarantee that many rows fit.
func DrawLaunchBanner(screen tcell.Screen, x, y, width int, subtitle string) int {
	if width <= 0 {
		return 0
	}

	row := y

	// Top accent line.
	DrawFadingAccent(screen, x, row, width, pendingBannerTextWidth)
	row++
	row++ // blank line

	// Main banner text with per-line gradient.
	for i, line := range pendingBannerLines {
		padLeft := max((width-pendingBannerTextWidth)/2, 0)
		style := tcell.StyleDefault.Foreground(pendingBannerGradient[i]).Bold(true)
		DrawText(screen, x+padLeft, row, width-padLeft, line, style)
		row++
	}

	// Gradient underline beneath banner.
	DrawGradientUnderline(screen, x, row, width, pendingBannerTextWidth, pendingBannerGradient[:])
	row++
	row++ // blank line

	// Subtitle.
	subPad := max((width-len(subtitle))/2, 0)
	DrawText(screen, x+subPad, row, width-subPad, subtitle, tcell.StyleDefault.Foreground(theme.ColorDimmed))
	row++
	row++ // blank line

	// Bottom accent line.
	DrawFadingAccent(screen, x, row, width, pendingBannerTextWidth)
	row++

	return row - y
}
