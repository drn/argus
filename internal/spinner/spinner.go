package spinner

import "time"

// Style identifies a named spinner animation.
type Style string

const (
	StyleProgress Style = "progress" // Nerd Font progress (ee06-ee0b), 6 frames
	StyleDots     Style = "dots"     // Braille dots, 10 frames
	StyleBraille  Style = "braille"  // Braille pattern, 8 frames
	StyleClassic  Style = "classic"  // ASCII |/-\, 4 frames
)

// Spinner holds the frames for an animation style.
type Spinner struct {
	Style        Style
	Label        string        // human-readable name for settings UI
	Frames       []rune
	TickInterval time.Duration // time per frame
}

// Frame returns the rune for the given animation tick (mod len(Frames)).
func (s *Spinner) Frame(tick int) rune {
	return s.Frames[tick%len(s.Frames)]
}

// FrameCount returns the number of frames in this spinner.
func (s *Spinner) FrameCount() int {
	return len(s.Frames)
}

// All is the ordered list of available spinner styles.
var All = []Spinner{
	{StyleProgress, "Progress", []rune{'\uEE06', '\uEE07', '\uEE08', '\uEE09', '\uEE0A', '\uEE0B'}, 150 * time.Millisecond},
	{StyleDots, "Dots", []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}, 100 * time.Millisecond},
	{StyleBraille, "Braille", []rune{'⣷', '⣯', '⣟', '⡿', '⢿', '⣻', '⣽', '⣾'}, 100 * time.Millisecond},
	{StyleClassic, "Classic", []rune{'|', '/', '-', '\\'}, 150 * time.Millisecond},
}

// Get returns the Spinner for the given style, falling back to Progress.
func Get(style Style) *Spinner {
	for i := range All {
		if All[i].Style == style {
			return &All[i]
		}
	}
	return &All[0]
}

// Styles returns the ordered list of style keys.
func Styles() []Style {
	out := make([]Style, len(All))
	for i := range All {
		out[i] = All[i].Style
	}
	return out
}

// Next returns the next style after the given one (wraps around).
func Next(current Style) Style {
	styles := Styles()
	for i, s := range styles {
		if s == current {
			return styles[(i+1)%len(styles)]
		}
	}
	return styles[0]
}

// Prev returns the previous style before the given one (wraps around).
func Prev(current Style) Style {
	styles := Styles()
	for i, s := range styles {
		if s == current {
			return styles[(i-1+len(styles))%len(styles)]
		}
	}
	return styles[0]
}
