package terminal

import "github.com/gdamore/tcell/v2"

// DesaturateStyle drains all color from a cell style, mapping both foreground
// and background to luminance-matched grays. ColorDefault passes through
// unchanged so the terminal's own default foreground/background is preserved
// rather than forced to a hard gray.
//
// Used by the task-list preview panel (always grayscale) and by TerminalPane
// when unfocused — both contexts need a pre-attentive signal that the surface
// is inactive and not receiving keystrokes.
func DesaturateStyle(style tcell.Style) tcell.Style {
	fg, bg, _ := style.Decompose()
	style = style.Foreground(GrayscaleColor(fg)).Background(GrayscaleColor(bg))
	// Decompose() returns only fg/bg/attrs — the underline color is a separate
	// channel. Gray it too (when set) so a colored underline (SGR 58) doesn't
	// leak through the otherwise-grayscale surface.
	if ulc := style.GetUnderlineColor(); ulc.Valid() {
		style = style.Underline(GrayscaleColor(ulc))
	}
	return style
}

// GrayscaleColor maps a color to its Rec. 601 luminance gray. Invalid/default
// colors (ColorDefault) pass through unchanged so the terminal's own default
// foreground/background is preserved rather than forced to a hard gray.
func GrayscaleColor(c tcell.Color) tcell.Color {
	if !c.Valid() {
		return c
	}
	r, g, b := c.RGB()
	if r < 0 {
		return c
	}
	l := (299*r + 587*g + 114*b) / 1000
	return tcell.NewRGBColor(l, l, l)
}
