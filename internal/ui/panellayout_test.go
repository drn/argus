package ui

import (
	"strings"
	"testing"
)

func TestPanelLayout_SplitWidths_AgentRatios(t *testing.T) {
	pl := NewPanelLayout([]PanelConfig{
		{Pct: 20, Min: 20},
		{Pct: 60, Min: 60},
		{Pct: 20, Min: 20},
	})
	pl.SetSize(120, 40)
	widths := pl.SplitWidths()

	if len(widths) != 3 {
		t.Fatalf("got %d widths, want 3", len(widths))
	}
	if widths[0] < 20 {
		t.Errorf("left = %d, want >= 20", widths[0])
	}
	if widths[1] < 60 {
		t.Errorf("center = %d, want >= 60", widths[1])
	}
	if widths[2] < 20 {
		t.Errorf("right = %d, want >= 20", widths[2])
	}
	total := widths[0] + widths[1] + widths[2]
	if total != 120 {
		t.Errorf("total = %d, want 120", total)
	}
}

func TestPanelLayout_SplitWidths_TaskRatios(t *testing.T) {
	// Task list view now uses the same 20/60/20 ratios as the agent view.
	pl := NewPanelLayout([]PanelConfig{
		{Pct: 20, Min: 20},
		{Pct: 60, Min: 60},
		{Pct: 20, Min: 20},
	})
	pl.SetSize(120, 40)
	widths := pl.SplitWidths()

	if widths[0] < 20 {
		t.Errorf("left = %d, want >= 20", widths[0])
	}
	if widths[1] < 60 {
		t.Errorf("center = %d, want >= 60", widths[1])
	}
	if widths[2] < 20 {
		t.Errorf("right = %d, want >= 20", widths[2])
	}
	total := widths[0] + widths[1] + widths[2]
	if total != 120 {
		t.Errorf("total = %d, want 120", total)
	}
}

func TestPanelLayout_SplitWidths_Narrow(t *testing.T) {
	pl := NewPanelLayout([]PanelConfig{
		{Pct: 20, Min: 20},
		{Pct: 60, Min: 60},
		{Pct: 20, Min: 20},
	})
	// Width is less than sum of minimums (100)
	pl.SetSize(60, 40)
	widths := pl.SplitWidths()

	total := widths[0] + widths[1] + widths[2]
	if total != 60 {
		t.Errorf("total = %d, want 60", total)
	}
	// All panels should be positive
	for i, w := range widths {
		if w <= 0 {
			t.Errorf("panel %d width = %d, want > 0", i, w)
		}
	}
}

func TestPanelLayout_SplitWidths_ZeroWidth(t *testing.T) {
	pl := NewPanelLayout([]PanelConfig{
		{Pct: 30, Min: 25},
		{Pct: 40, Min: 30},
		{Pct: 30, Min: 20},
	})
	pl.SetSize(0, 40)
	widths := pl.SplitWidths()

	for i, w := range widths {
		if w != 0 {
			t.Errorf("panel %d width = %d, want 0 for zero-width layout", i, w)
		}
	}
}

func TestPanelLayout_SplitWidths_SumAlwaysEqualsWidth(t *testing.T) {
	pl := NewPanelLayout([]PanelConfig{
		{Pct: 20, Min: 20},
		{Pct: 60, Min: 60},
		{Pct: 20, Min: 20},
	})
	for _, w := range []int{50, 80, 100, 120, 150, 200, 250} {
		pl.SetSize(w, 40)
		widths := pl.SplitWidths()
		total := 0
		for _, pw := range widths {
			total += pw
		}
		if total != w {
			t.Errorf("width=%d: total = %d, want %d (widths=%v)", w, total, w, widths)
		}
	}
}

func TestPanelLayout_Render(t *testing.T) {
	pl := NewPanelLayout([]PanelConfig{
		{Pct: 50, Min: 10},
		{Pct: 50, Min: 10},
	})
	pl.SetSize(20, 3)

	result := pl.Render([]string{"left", "right"})
	lines := strings.Split(result, "\n")
	if len(lines) != 3 {
		t.Errorf("got %d lines, want 3", len(lines))
	}
	if !strings.Contains(result, "left") {
		t.Error("expected 'left' in rendered output")
	}
	if !strings.Contains(result, "right") {
		t.Error("expected 'right' in rendered output")
	}
}

func TestPanelLayout_Render_PadsHeight(t *testing.T) {
	pl := NewPanelLayout([]PanelConfig{
		{Pct: 100, Min: 10},
	})
	pl.SetSize(20, 5)

	// Single line of content should be padded to 5 lines
	result := pl.Render([]string{"hello"})
	lines := strings.Split(result, "\n")
	if len(lines) != 5 {
		t.Errorf("got %d lines, want 5", len(lines))
	}
}

func TestPanelLayout_Empty(t *testing.T) {
	pl := NewPanelLayout(nil)
	pl.SetSize(100, 40)
	widths := pl.SplitWidths()
	if len(widths) != 0 {
		t.Errorf("got %d widths for nil configs, want 0", len(widths))
	}
}
