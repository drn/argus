package widget

import "github.com/gdamore/tcell/v2"

// DrawText writes a string at position, clipped to maxWidth.
func DrawText(screen tcell.Screen, x, y, maxWidth int, text string, style tcell.Style) {
	col := x
	for _, r := range text {
		if col-x >= maxWidth {
			break
		}
		screen.SetContent(col, y, r, nil, style)
		col++
	}
}

// FillArea fills a rectangle with a rune and style. Use it at the top of a
// widget's Draw to guarantee every cell in its bounding box is overwritten.
// That keeps redraws correct even when lazyScreen.skipClear suppresses
// tview's screen-wide Clear (see internal/tui/lazyscreen.go): widgets that
// only paint their occupied rows would otherwise leak stale cells from the
// previous frame's primitive.
func FillArea(screen tcell.Screen, x, y, w, h int, r rune, style tcell.Style) {
	if w <= 0 || h <= 0 {
		return
	}
	for row := y; row < y+h; row++ {
		for col := x; col < x+w; col++ {
			screen.SetContent(col, row, r, nil, style)
		}
	}
}
