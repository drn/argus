package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// PanelConfig describes a single panel's target width ratio and minimum width.
type PanelConfig struct {
	Pct int // target width percentage (all panels should sum to 100)
	Min int // minimum width in columns
}

// PanelLayout handles horizontal width splitting for a multi-panel layout.
// Callers provide panel configs (percentage + minimum width) and get back
// computed widths. This is shared between the agent view and task list view.
type PanelLayout struct {
	configs []PanelConfig
	width   int
	height  int
}

// NewPanelLayout creates a layout with the given panel configurations.
func NewPanelLayout(configs []PanelConfig) PanelLayout {
	return PanelLayout{configs: configs}
}

// SetSize updates the available dimensions.
func (pl *PanelLayout) SetSize(w, h int) {
	pl.width = w
	pl.height = h
}

// Width returns the current total width.
func (pl PanelLayout) Width() int { return pl.width }

// Height returns the current total height.
func (pl PanelLayout) Height() int { return pl.height }

// SplitWidths computes panel widths from percentages and minimums.
// When the terminal is too narrow, panels are compressed right-to-left
// (rightmost panel shrinks first) to preserve left/center content.
func (pl PanelLayout) SplitWidths() []int {
	n := len(pl.configs)
	if n == 0 || pl.width <= 0 {
		return make([]int, n)
	}

	widths := make([]int, n)

	// Compute target widths from percentages
	assigned := 0
	for i, c := range pl.configs {
		w := pl.width * c.Pct / 100
		if w < c.Min {
			w = c.Min
		}
		widths[i] = w
		assigned += w
	}

	// If total exceeds available width, compress right-to-left.
	// Each panel can shrink down to half its minimum (hard floor).
	if excess := assigned - pl.width; excess > 0 {
		for i := n - 1; i >= 0 && excess > 0; i-- {
			floor := pl.configs[i].Min / 2
			if floor < 5 {
				floor = 5
			}
			shrink := widths[i] - floor
			if shrink > excess {
				shrink = excess
			}
			if shrink > 0 {
				widths[i] -= shrink
				excess -= shrink
			}
		}
	}

	// Give any remaining slack to the last panel that was assigned by percentage.
	// This avoids 1-pixel gaps from integer division.
	total := 0
	for _, w := range widths {
		total += w
	}
	if diff := pl.width - total; diff != 0 && n > 0 {
		// Find the largest panel to absorb the remainder
		largest := 0
		for i := 1; i < n; i++ {
			if widths[i] > widths[largest] {
				largest = i
			}
		}
		widths[largest] += diff
	}

	return widths
}

// Render takes pre-rendered panel strings, pads them to a uniform height,
// and joins them horizontally. Callers are responsible for borders and content.
func (pl PanelLayout) Render(panels []string) string {
	for i := range panels {
		panels[i] = padHeight(panels[i], pl.height)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, panels...)
}

// padHeight ensures a rendered string fills exactly h lines.
// This is a package-level function also used elsewhere.
func padHeight(s string, h int) string {
	if h <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) >= h {
		return strings.Join(lines[:h], "\n")
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}
