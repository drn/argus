// Package tui2 implements the tcell/tview UI runtime for Argus.
// This is the target runtime for native terminal passthrough in the agent
// view, replacing the Bubble Tea string-rendering approach.
package tui2

import "github.com/gdamore/tcell/v2"

// Color constants matching the Bubble Tea theme (256-color palette).
var (
	ColorTitle      = tcell.Color87  // cyan — titles, focused borders
	ColorStatusBG   = tcell.Color235 // dark gray — status bar background
	ColorStatusFG   = tcell.Color245 // medium gray — status bar text
	ColorSelected   = tcell.Color212 // pink — selected/cursor row
	ColorNormal     = tcell.Color252 // light gray — default text
	ColorDimmed     = tcell.Color240 // dim gray — secondary text
	ColorPending    = tcell.Color245 // gray — pending status
	ColorInProgress = tcell.Color214 // orange — in-progress status
	ColorInReview   = tcell.Color81  // blue — in-review status
	ColorComplete   = tcell.Color78  // green — complete status
	ColorProject    = tcell.Color87  // cyan — project names
	ColorElapsed    = tcell.Color243 // gray — elapsed times
	ColorBorder     = tcell.Color238 // dark gray — unfocused borders
	ColorError      = tcell.Color203 // red — errors
	ColorKeyHint    = tcell.Color87  // cyan — keybinding hints
	ColorKeyLabel   = tcell.Color240 // dim — keybinding labels
	ColorHighlight  = tcell.Color236 // slightly lighter dark gray — cursor/selection highlight
)

// Styles for common UI elements.
var (
	StyleDefault      = tcell.StyleDefault
	StyleTitle        = tcell.StyleDefault.Foreground(ColorTitle).Bold(true)
	StyleStatusBar    = tcell.StyleDefault.Background(ColorStatusBG).Foreground(ColorStatusFG)
	StyleSelected     = tcell.StyleDefault.Foreground(ColorSelected).Bold(true)
	StyleNormal       = tcell.StyleDefault.Foreground(ColorNormal)
	StyleDimmed       = tcell.StyleDefault.Foreground(ColorDimmed)
	StylePending      = tcell.StyleDefault.Foreground(ColorPending)
	StyleInProgress   = tcell.StyleDefault.Foreground(ColorInProgress)
	StyleInReview     = tcell.StyleDefault.Foreground(ColorInReview)
	StyleComplete     = tcell.StyleDefault.Foreground(ColorComplete)
	StyleProject      = tcell.StyleDefault.Foreground(ColorProject)
	StyleBorder       = tcell.StyleDefault.Foreground(ColorBorder)
	StyleFocusedBorder = tcell.StyleDefault.Foreground(ColorTitle)
	StyleError        = tcell.StyleDefault.Foreground(ColorError)
)
