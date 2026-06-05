// Package theme defines colors, icons, and styles for the Argus TUI.
package theme

import (
	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/model"
)

// Color constants for the 256-color palette theme.
var (
	ColorTitle      = tcell.Color87                    // cyan — titles, focused borders
	ColorStatusBG   = tcell.Color235                   // dark gray — status bar background
	ColorStatusFG   = tcell.Color245                   // medium gray — status bar text
	ColorSelected   = tcell.Color212                   // pink — selected/cursor row
	ColorNormal     = tcell.Color252                   // light gray — default text
	ColorDimmed     = tcell.Color240                   // dim gray — secondary text
	ColorPending    = tcell.Color245                   // gray — pending status
	ColorInProgress = tcell.Color214                   // orange — in-progress status
	ColorInReview   = tcell.Color81                    // blue — in-review status
	ColorComplete   = tcell.Color78                    // green — complete status
	ColorProject    = tcell.Color87                    // cyan — project names
	ColorElapsed    = tcell.Color243                   // gray — elapsed times
	ColorBorder     = tcell.Color238                   // dark gray — unfocused borders
	ColorError      = tcell.Color203                   // red — errors
	ColorKeyHint    = tcell.Color87                    // cyan — keybinding hints
	ColorKeyLabel   = tcell.Color240                   // dim — keybinding labels
	ColorHighlight  = tcell.Color236                   // slightly lighter dark gray — cursor/selection highlight
	ColorFilter     = tcell.Color201                   // magenta — active filter query
	ColorNeedsInput = tcell.NewRGBColor(250, 163, 120) // #faa378 light orange — agent blocked on user prompt

	// PR review indicator colors (add-pr-review-indicator). One per actionable
	// review state; non-actionable states render no cell so they need no color.
	ColorPRAwaiting = tcell.NewRGBColor(178, 148, 250) // #b294fa purple — PR open, awaiting review
	ColorPRChanges  = tcell.NewRGBColor(240, 96, 96)   // #f06060 red — reviewer requested changes
	ColorPRApproved = tcell.NewRGBColor(120, 220, 120) // #78dc78 green — PR approved
)

// Icon constants for status indicators (Nerd Font codepoints).
const (
	IconMoonStars   = rune(0x0F0594) // 󰖔 nf-md-weather_night — unvisited / needs attention
	IconMoonOutline = rune(0xF186)   //  nf-fa-moon_o — visited / idle
	IconNeedsInput  = rune(0xF059)   //  nf-fa-question_circle — idle AND blocked on a user prompt

	// PR review indicator glyphs (add-pr-review-indicator). All three live in
	// the git-pull-request family so they read as "this is about a PR", but use
	// distinct overlays so the three actionable states are tellable apart at a
	// glance. CODEPOINTS NOT RENDER-TESTED IN A TERMINAL YET — eyeball these in
	// a real Nerd Font terminal for distinctness before relying on them.
	IconPRAwaiting = rune(0xF407)  //  nf-oct-git_pull_request — open PR awaiting review
	IconPRChanges  = rune(0xF09D8) // 󰧘 nf-md-source_pull (changes requested overlay)
	IconPRApproved = rune(0xF0DDF) // 󰷟 nf-md-source_branch_check (approved overlay)
)

// Styles for common UI elements.
var (
	StyleDefault       = tcell.StyleDefault
	StyleTitle         = tcell.StyleDefault.Foreground(ColorTitle).Bold(true)
	StyleStatusBar     = tcell.StyleDefault.Background(ColorStatusBG).Foreground(ColorStatusFG)
	StyleSelected      = tcell.StyleDefault.Foreground(ColorSelected).Bold(true)
	StyleNormal        = tcell.StyleDefault.Foreground(ColorNormal)
	StyleDimmed        = tcell.StyleDefault.Foreground(ColorDimmed)
	StylePending       = tcell.StyleDefault.Foreground(ColorPending)
	StyleInProgress    = tcell.StyleDefault.Foreground(ColorInProgress)
	StyleInReview      = tcell.StyleDefault.Foreground(ColorInReview)
	StyleComplete      = tcell.StyleDefault.Foreground(ColorComplete)
	StyleProject       = tcell.StyleDefault.Foreground(ColorProject)
	StyleBorder        = tcell.StyleDefault.Foreground(ColorBorder)
	StyleFocusedBorder = tcell.StyleDefault.Foreground(ColorTitle)
	StyleError         = tcell.StyleDefault.Foreground(ColorError)
	StyleFilter        = tcell.StyleDefault.Foreground(ColorFilter).Bold(true)
	StyleNeedsInput    = tcell.StyleDefault.Foreground(ColorNeedsInput).Bold(true)

	// PR review indicator styles (add-pr-review-indicator).
	StylePRAwaiting = tcell.StyleDefault.Foreground(ColorPRAwaiting).Bold(true)
	StylePRChanges  = tcell.StyleDefault.Foreground(ColorPRChanges).Bold(true)
	StylePRApproved = tcell.StyleDefault.Foreground(ColorPRApproved).Bold(true)
)

// PRGlyph maps a PR review state to the glyph and style its reserved task-row
// cell should render. Only the three actionable states (awaiting-review,
// changes-requested, approved) produce a visible glyph; every other state
// (none, draft, merged-closed, unknown) returns a blank space with the default
// style so the reserved cell stays empty without shifting the name column.
func PRGlyph(s model.PRState) (rune, tcell.Style) {
	switch s {
	case model.PRAwaitingReview:
		return IconPRAwaiting, StylePRAwaiting
	case model.PRChangesRequested:
		return IconPRChanges, StylePRChanges
	case model.PRApproved:
		return IconPRApproved, StylePRApproved
	default:
		return ' ', tcell.StyleDefault
	}
}
