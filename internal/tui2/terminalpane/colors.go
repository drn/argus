// Package terminalpane provides a native terminal rendering widget that maps
// vt10x terminal emulator cells directly to tcell screen cells. This bypasses
// the ANSI-string intermediary used by the Bubble Tea runtime, giving true
// terminal passthrough for the agent view.
package terminalpane

import (
	"github.com/gdamore/tcell/v2"
	"github.com/hinshun/vt10x"
)

// vt10x attribute bit flags (unexported in the library).
const (
	vtAttrReverse   = 1 << 0
	vtAttrUnderline = 1 << 1
	vtAttrBold      = 1 << 2
	vtAttrItalic    = 1 << 4
)

// Cursor colors — high-contrast palette independent of the terminal theme.
var (
	CursorFG = tcell.PaletteColor(17)  // dark blue
	CursorBG = tcell.PaletteColor(153) // light blue
)

// vtColorToTcell maps a vt10x.Color to a tcell.Color.
func vtColorToTcell(c vt10x.Color) tcell.Color {
	if c == vt10x.DefaultFG || c == vt10x.DefaultBG {
		return tcell.ColorDefault
	}
	n := uint32(c)
	if n < 256 {
		return tcell.PaletteColor(int(n))
	}
	// True color (RGB encoded in upper bits)
	r := int32((n >> 16) & 0xFF)
	g := int32((n >> 8) & 0xFF)
	b := int32(n & 0xFF)
	return tcell.NewRGBColor(r, g, b)
}

// cellStyle converts a vt10x cell's attributes to a tcell.Style.
func cellStyle(cell vt10x.Glyph) tcell.Style {
	style := tcell.StyleDefault.
		Foreground(vtColorToTcell(cell.FG)).
		Background(vtColorToTcell(cell.BG))

	if cell.Mode&vtAttrBold != 0 {
		style = style.Bold(true)
	}
	if cell.Mode&vtAttrItalic != 0 {
		style = style.Italic(true)
	}
	if cell.Mode&vtAttrUnderline != 0 {
		style = style.Underline(true)
	}
	if cell.Mode&vtAttrReverse != 0 {
		style = style.Reverse(true)
	}
	return style
}
