package ui

import (
	"strings"
	"testing"
)

func TestRenderBanner_WideWidth(t *testing.T) {
	result := renderBanner(100)
	if !strings.Contains(result, "██") {
		t.Error("wide banner should contain block art characters")
	}
	if !strings.Contains(result, "O R C H E S T R A T O R") {
		t.Error("wide banner should contain spaced subtitle")
	}
	if !strings.Contains(result, "⬡") {
		t.Error("wide banner should contain hexagon accent")
	}
	if !strings.Contains(result, "─") {
		t.Error("wide banner should contain gradient underline")
	}
}

func TestRenderBanner_NarrowWidth(t *testing.T) {
	result := renderBanner(30)
	if !strings.Contains(result, "ARGUS") {
		t.Error("narrow banner should contain compact 'ARGUS'")
	}
	if !strings.Contains(result, "CODE ORCHESTRATOR") {
		t.Error("narrow banner should contain compact subtitle")
	}
}

func TestRenderBanner_ExactBoundary(t *testing.T) {
	narrow := renderBanner(bannerWidth + 3)
	if !strings.Contains(narrow, "CODE ORCHESTRATOR") {
		t.Error("width bannerWidth+3 should use compact format")
	}

	wide := renderBanner(bannerWidth + 4)
	if !strings.Contains(wide, "O R C H E S T R A T O R") {
		t.Error("width bannerWidth+4 should use full banner")
	}
}

func TestRenderBanner_ZeroWidth(t *testing.T) {
	result := renderBanner(0)
	if result == "" {
		t.Error("banner with zero width should still produce output")
	}
}

func TestFadeDashes(t *testing.T) {
	tests := []struct {
		length  int
		reverse bool
	}{
		{0, false},
		{1, false},
		{10, false},
		{10, true},
		{30, false},
	}
	for _, tt := range tests {
		result := fadeDashes(tt.length, tt.reverse)
		if len(result) != tt.length {
			t.Errorf("fadeDashes(%d, %v) length = %d, want %d", tt.length, tt.reverse, len(result), tt.length)
		}
	}
}

func TestRenderGradientUnderline(t *testing.T) {
	result := renderGradientUnderline(80, 41)
	if !strings.Contains(result, "─") {
		t.Error("gradient underline should contain dash characters")
	}
	if result == "" {
		t.Error("gradient underline should not be empty")
	}
}

func TestRenderGradientUnderline_ZeroLen(t *testing.T) {
	result := renderGradientUnderline(80, 0)
	if result != "" {
		t.Error("gradient underline with zero length should be empty")
	}
}

func TestRenderFadingAccent(t *testing.T) {
	result := renderFadingAccent(80, accentCyan, accentPink)
	if !strings.Contains(result, "⬡") {
		t.Error("fading accent should contain hexagon")
	}
	// Should contain true-color escape sequences from gradient
	if !strings.Contains(result, "\x1b[38;2;") {
		t.Error("fading accent should contain true-color gradient escapes")
	}
}

func TestLerpRGB(t *testing.T) {
	a := rgb{0, 0, 0}
	b := rgb{100, 200, 50}
	mid := lerpRGB(a, b, 0.5)
	if mid.r != 50 || mid.g != 100 || mid.b != 25 {
		t.Errorf("lerpRGB midpoint = %v, want {50 100 25}", mid)
	}
	start := lerpRGB(a, b, 0.0)
	if start != a {
		t.Errorf("lerpRGB at t=0 = %v, want %v", start, a)
	}
	end := lerpRGB(a, b, 1.0)
	if end != b {
		t.Errorf("lerpRGB at t=1 = %v, want %v", end, b)
	}
}

func TestRenderGradientDashes(t *testing.T) {
	result := renderGradientDashes("-- -", rgb{0, 0, 0}, rgb{255, 255, 255})
	if !strings.Contains(result, "\x1b[38;2;") {
		t.Error("should contain true-color escapes for dash characters")
	}
	// Spaces should not have color escapes
	if strings.Contains(result, "\x1b[38;2;") && !strings.Contains(result, "-") {
		t.Error("should still contain dash characters")
	}
	// Empty pattern
	if renderGradientDashes("", rgb{0, 0, 0}, rgb{255, 255, 255}) != "" {
		t.Error("empty pattern should return empty string")
	}
}
