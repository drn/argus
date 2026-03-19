package tui2

import (
	"github.com/gdamore/tcell/v2"
)

var bannerLines = [...]string{
	` █████  ██████   ██████  ██    ██ ███████`,
	`██   ██ ██   ██ ██       ██    ██ ██     `,
	`███████ ██████  ██   ███ ██    ██ ███████`,
	`██   ██ ██   ██ ██    ██ ██    ██      ██`,
	`██   ██ ██   ██  ██████   ██████  ███████`,
}

// Per-line gradient colors for the banner.
var bannerGradient = [...]tcell.Color{
	tcell.Color87,  // bright cyan
	tcell.Color81,  // light blue
	tcell.Color141, // lavender
	tcell.Color177, // light purple
	tcell.Color212, // pink
}

const bannerTextWidth = 41

const subtitle = "C O D E   O R C H E S T R A T O R"

// bannerHeight returns the total height of the banner (logo + spacing + subtitle + spacing + accent).
func bannerHeight() int {
	// accent(1) + blank(1) + 5 logo lines + underline(1) + blank(1) + subtitle(1) + blank(1) + accent(1) = 12
	return 12
}

// drawBanner draws the ASCII banner centered at the given y offset.
// Returns the number of rows consumed.
func drawBanner(screen tcell.Screen, x, y, width int) int {
	if width <= 0 {
		return 0
	}

	row := y

	// Top accent line.
	drawFadingAccent(screen, x, row, width)
	row++
	row++ // blank line

	// Main banner text with per-line gradient.
	for i, line := range bannerLines {
		padLeft := (width - bannerTextWidth) / 2
		if padLeft < 0 {
			padLeft = 0
		}
		style := tcell.StyleDefault.Foreground(bannerGradient[i]).Bold(true)
		drawText(screen, x+padLeft, row, width-padLeft, line, style)
		row++
	}

	// Gradient underline beneath banner.
	drawGradientUnderline(screen, x, row, width)
	row++
	row++ // blank line

	// Subtitle.
	subPad := (width - len(subtitle)) / 2
	if subPad < 0 {
		subPad = 0
	}
	drawText(screen, x+subPad, row, width-subPad, subtitle, tcell.StyleDefault.Foreground(ColorDimmed))
	row++
	row++ // blank line

	// Bottom accent line.
	drawFadingAccent(screen, x, row, width)
	row++

	return row - y
}

// drawFadingAccent draws two fading dash lines from center with a hexagon.
func drawFadingAccent(screen tcell.Screen, x, y, width int) {
	sideLen := max((width-bannerTextWidth)/2-2, 3)

	leftPattern := fadeDashes(sideLen, false)
	rightPattern := fadeDashes(sideLen, true)

	// Compute where to start so it's centered.
	totalLen := len(leftPattern) + 3 + len(rightPattern) // " ⬡ "
	padLeft := (width - totalLen) / 2
	if padLeft < 0 {
		padLeft = 0
	}

	col := x + padLeft

	// Left dashes: dim → cyan gradient.
	drawGradientChars(screen, col, y, leftPattern, rgbVal{98, 98, 98}, rgbVal{95, 255, 255})
	col += len(leftPattern)

	// Space + hexagon + space.
	screen.SetContent(col, y, ' ', nil, tcell.StyleDefault)
	col++
	screen.SetContent(col, y, '⬡', nil, tcell.StyleDefault.Foreground(tcell.Color87))
	col++
	screen.SetContent(col, y, ' ', nil, tcell.StyleDefault)
	col++

	// Right dashes: pink → dim gradient.
	drawGradientChars(screen, col, y, rightPattern, rgbVal{255, 135, 215}, rgbVal{98, 98, 98})
}

// drawGradientUnderline draws a centered underline with the banner gradient colors.
func drawGradientUnderline(screen tcell.Screen, x, y, width int) {
	lineLen := bannerTextWidth
	segLen := max(lineLen/len(bannerGradient), 1)
	padLeft := (width - lineLen) / 2
	if padLeft < 0 {
		padLeft = 0
	}

	col := x + padLeft
	for i, c := range bannerGradient {
		n := segLen
		if i == len(bannerGradient)-1 {
			n = lineLen - segLen*(len(bannerGradient)-1)
		}
		style := tcell.StyleDefault.Foreground(c)
		for range n {
			screen.SetContent(col, y, '─', nil, style)
			col++
		}
	}
}

// rgbVal holds RGB components for gradient interpolation.
type rgbVal struct{ r, g, b uint8 }

func lerpRGB(a, b rgbVal, t float64) rgbVal {
	return rgbVal{
		r: uint8(float64(a.r) + t*(float64(b.r)-float64(a.r))),
		g: uint8(float64(a.g) + t*(float64(b.g)-float64(a.g))),
		b: uint8(float64(a.b) + t*(float64(b.b)-float64(a.b))),
	}
}

// drawGradientChars draws a string with per-character RGB gradient.
func drawGradientChars(screen tcell.Screen, x, y int, pattern string, from, to rgbVal) {
	n := len(pattern)
	if n == 0 {
		return
	}
	for i := 0; i < n; i++ {
		ch := rune(pattern[i])
		if ch == ' ' {
			screen.SetContent(x+i, y, ' ', nil, tcell.StyleDefault)
			continue
		}
		t := float64(i) / float64(max(n-1, 1))
		c := lerpRGB(from, to, t)
		style := tcell.StyleDefault.Foreground(tcell.NewRGBColor(int32(c.r), int32(c.g), int32(c.b)))
		screen.SetContent(x+i, y, ch, nil, style)
	}
}

// fadeDashes generates a string of dashes that fade from sparse to dense.
// If reverse is true, the dense end is on the left.
func fadeDashes(length int, reverse bool) string {
	if length <= 0 {
		return ""
	}
	buf := make([]byte, length)
	for i := range buf {
		pos := i
		if reverse {
			pos = length - 1 - i
		}
		third := length / 3
		if third == 0 {
			third = 1
		}
		if pos < third {
			if pos%3 == 2 {
				buf[i] = '-'
			} else {
				buf[i] = ' '
			}
		} else if pos < 2*third {
			if pos%2 == 1 {
				buf[i] = '-'
			} else {
				buf[i] = ' '
			}
		} else {
			buf[i] = '-'
		}
	}
	return string(buf)
}

