package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var bannerLines = [...]string{
	` █████  ██████   ██████  ██    ██ ███████`,
	`██   ██ ██   ██ ██       ██    ██ ██     `,
	`███████ ██████  ██   ███ ██    ██ ███████`,
	`██   ██ ██   ██ ██    ██ ██    ██      ██`,
	`██   ██ ██   ██  ██████   ██████  ███████`,
}

var bannerGradient = [...]lipgloss.Color{
	lipgloss.Color("87"),  // bright cyan
	lipgloss.Color("81"),  // light blue
	lipgloss.Color("141"), // lavender
	lipgloss.Color("177"), // light purple
	lipgloss.Color("212"), // pink
}

const bannerWidth = 41

// Accent colors matching gradient endpoints.
var (
	accentCyan = lipgloss.Color("87")  // cyan (top of gradient)
	accentPink = lipgloss.Color("212") // pink (bottom of gradient)
	dimColor   = lipgloss.Color("241") // muted gray
)

func renderBanner(width int) string {
	if width < bannerWidth+4 {
		// Terminal too narrow for banner — use compact title
		title := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("87")).
			Render("ARGUS")
		sub := lipgloss.NewStyle().
			Foreground(dimColor).
			Render("CODE ORCHESTRATOR")
		block := lipgloss.JoinVertical(lipgloss.Center, title, sub)
		return lipgloss.PlaceHorizontal(width, lipgloss.Center, block)
	}

	var b strings.Builder

	// Top accent: fading lines from center with hexagon
	b.WriteString(renderFadingAccent(width, accentCyan, accentPink))
	b.WriteByte('\n')
	b.WriteByte('\n')

	// Main banner text with per-line gradient
	for i, line := range bannerLines {
		styled := lipgloss.NewStyle().
			Bold(true).
			Foreground(bannerGradient[i]).
			Render(line)
		centered := lipgloss.PlaceHorizontal(width, lipgloss.Center, styled)
		b.WriteString(centered)
		b.WriteByte('\n')
	}

	// Gradient underline beneath banner
	underlineLen := bannerWidth
	b.WriteString(renderGradientUnderline(width, underlineLen))
	b.WriteByte('\n')
	b.WriteByte('\n')

	// Subtitle — clean and airy
	sub := lipgloss.NewStyle().
		Foreground(dimColor).
		Render("C O D E   O R C H E S T R A T O R")
	b.WriteString(lipgloss.PlaceHorizontal(width, lipgloss.Center, sub))
	b.WriteByte('\n')
	b.WriteByte('\n')

	// Bottom accent: fading lines with hexagon
	b.WriteString(renderFadingAccent(width, accentCyan, accentPink))

	return b.String()
}

// RGB color for gradient interpolation.
type rgb struct{ r, g, b uint8 }

var (
	rgbCyan = rgb{95, 255, 255}  // color 87
	rgbPink = rgb{255, 135, 215} // color 212
	rgbDim  = rgb{98, 98, 98}    // color 241
)

// lerpRGB linearly interpolates between two colors. t in [0,1].
func lerpRGB(a, b rgb, t float64) rgb {
	return rgb{
		r: uint8(float64(a.r) + t*(float64(b.r)-float64(a.r))),
		g: uint8(float64(a.g) + t*(float64(b.g)-float64(a.g))),
		b: uint8(float64(a.b) + t*(float64(b.b)-float64(a.b))),
	}
}

// renderGradientDashes renders a dash pattern with per-character color gradient.
func renderGradientDashes(pattern string, from, to rgb) string {
	n := len(pattern)
	if n == 0 {
		return ""
	}
	var b strings.Builder
	for i := 0; i < n; i++ {
		ch := pattern[i]
		if ch == ' ' {
			b.WriteByte(' ')
			continue
		}
		t := float64(i) / float64(max(n-1, 1))
		c := lerpRGB(from, to, t)
		fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm%c\x1b[0m", c.r, c.g, c.b, ch)
	}
	return b.String()
}

// renderFadingAccent draws two fading dashes from center with a hexagon accent.
// Each side has a per-character color gradient: dim at edges, bright near center.
func renderFadingAccent(width int, left, _ lipgloss.Color) string {
	sideLen := max((width-bannerWidth)/2-2, 3)

	// Build fading dashes: sparse near edges, dense near center
	leftPattern := fadeDashes(sideLen, false)
	rightPattern := fadeDashes(sideLen, true)

	// Left: dim gray → cyan (edge → center)
	leftStyled := renderGradientDashes(leftPattern, rgbDim, rgbCyan)
	// Right: pink → dim gray (center → edge)
	rightStyled := renderGradientDashes(rightPattern, rgbPink, rgbDim)

	hex := lipgloss.NewStyle().Foreground(left).Render("⬡")

	line := leftStyled + " " + hex + " " + rightStyled
	return lipgloss.PlaceHorizontal(width, lipgloss.Center, line)
}

// fadeDashes generates a string of dashes that fade from sparse to dense.
// If reverse is true, the dense end is on the left (fading out to the right).
func fadeDashes(length int, reverse bool) string {
	if length <= 0 {
		return ""
	}
	buf := make([]byte, length)
	for i := range buf {
		// density increases toward the center
		pos := i
		if reverse {
			pos = length - 1 - i
		}
		// first third: sparse (every 3rd char), middle: every 2nd, last: solid
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

// renderGradientUnderline renders a centered underline using the gradient colors.
func renderGradientUnderline(width, lineLen int) string {
	if lineLen <= 0 {
		return ""
	}
	// Split the underline into segments matching the gradient
	segLen := max(lineLen/len(bannerGradient), 1)
	var parts []string
	for i, c := range bannerGradient {
		n := segLen
		if i == len(bannerGradient)-1 {
			n = lineLen - segLen*(len(bannerGradient)-1) // remainder
		}
		if n > 0 {
			parts = append(parts, lipgloss.NewStyle().Foreground(c).Render(strings.Repeat("─", n)))
		}
	}
	line := strings.Join(parts, "")
	return lipgloss.PlaceHorizontal(width, lipgloss.Center, line)
}
