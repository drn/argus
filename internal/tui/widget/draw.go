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
