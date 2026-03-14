package ui

import (
	"strings"
	"testing"

	"github.com/hinshun/vt10x"
)

func TestRenderLine_NoCursor(t *testing.T) {
	vt := vt10x.New(vt10x.WithSize(20, 5))
	vt.Write([]byte("hello"))
	vt.Lock()
	defer vt.Unlock()

	line := renderLine(vt, 0, 20, -1)
	stripped := stripANSI(line)
	if stripped != "hello" {
		t.Errorf("renderLine without cursor = %q, want %q", stripped, "hello")
	}
	// Should NOT contain reverse video escape
	if strings.Contains(line, "\x1b[7m") {
		t.Error("renderLine without cursor should not contain reverse video")
	}
}

func TestRenderLine_WithCursor(t *testing.T) {
	vt := vt10x.New(vt10x.WithSize(20, 5))
	vt.Write([]byte("hello"))
	vt.Lock()
	defer vt.Unlock()

	// Cursor at position 2 (on the 'l')
	line := renderLine(vt, 0, 20, 2)
	// Should contain reverse video escape for the cursor
	if !strings.Contains(line, "\x1b[7m") {
		t.Error("renderLine with cursor should contain reverse video escape")
	}
}

func TestRenderLine_CursorBeyondText(t *testing.T) {
	vt := vt10x.New(vt10x.WithSize(20, 5))
	vt.Write([]byte("hi"))
	vt.Lock()
	defer vt.Unlock()

	// Cursor at position 5, beyond "hi" (at position 2 would be after text)
	line := renderLine(vt, 0, 20, 5)
	// Should still render cursor (extends lastCol)
	if !strings.Contains(line, "\x1b[7m") {
		t.Error("renderLine with cursor beyond text should still render cursor")
	}
}

func TestStripANSI_NoEscapes(t *testing.T) {
	result := stripANSI("hello world")
	if result != "hello world" {
		t.Errorf("stripANSI('hello world') = %q, want 'hello world'", result)
	}
}

func TestStripANSI_CSISequences(t *testing.T) {
	result := stripANSI("\x1b[31mhello\x1b[0m")
	if result != "hello" {
		t.Errorf("stripANSI with CSI = %q, want 'hello'", result)
	}
}

func TestStripANSI_MixedContent(t *testing.T) {
	result := stripANSI("\x1b[1;32mgreen bold\x1b[0m normal \x1b[4munderline\x1b[0m")
	if result != "green bold normal underline" {
		t.Errorf("stripANSI mixed = %q, want 'green bold normal underline'", result)
	}
}

func TestStripANSI_Empty(t *testing.T) {
	result := stripANSI("")
	if result != "" {
		t.Errorf("stripANSI('') = %q, want ''", result)
	}
}

func TestStripANSI_OnlyEscapes(t *testing.T) {
	result := stripANSI("\x1b[0m\x1b[31m\x1b[0m")
	if result != "" {
		t.Errorf("stripANSI(only escapes) = %q, want ''", result)
	}
}

func TestStripANSI_Whitespace(t *testing.T) {
	result := stripANSI("  hello  ")
	if result != "hello" {
		t.Errorf("stripANSI with whitespace = %q, want 'hello'", result)
	}
}

func TestBuildSGR_ResetOnly(t *testing.T) {
	result := buildSGR(vt10x.DefaultFG, vt10x.DefaultBG, 0)
	if result != "\x1b[0m" {
		t.Errorf("buildSGR default = %q, want '\\x1b[0m'", result)
	}
}

func TestBuildSGR_Bold(t *testing.T) {
	result := buildSGR(vt10x.DefaultFG, vt10x.DefaultBG, vtAttrBold)
	if !strings.Contains(result, ";1") && !strings.Contains(result, "1;") {
		t.Errorf("buildSGR bold = %q, should contain bold param '1'", result)
	}
}

func TestBuildSGR_Italic(t *testing.T) {
	result := buildSGR(vt10x.DefaultFG, vt10x.DefaultBG, vtAttrItalic)
	if !strings.Contains(result, "3") {
		t.Errorf("buildSGR italic = %q, should contain italic param '3'", result)
	}
}

func TestBuildSGR_Underline(t *testing.T) {
	result := buildSGR(vt10x.DefaultFG, vt10x.DefaultBG, vtAttrUnderline)
	if !strings.Contains(result, "4") {
		t.Errorf("buildSGR underline = %q, should contain underline param '4'", result)
	}
}

func TestBuildSGR_Reverse(t *testing.T) {
	result := buildSGR(vt10x.DefaultFG, vt10x.DefaultBG, vtAttrReverse)
	if !strings.Contains(result, "7") {
		t.Errorf("buildSGR reverse = %q, should contain reverse param '7'", result)
	}
}

func TestBuildSGR_WithFGColor(t *testing.T) {
	result := buildSGR(vt10x.Color(1), vt10x.DefaultBG, 0)
	// Color 1 = standard red, should produce "31"
	if !strings.Contains(result, "31") {
		t.Errorf("buildSGR fg=1 = %q, should contain '31'", result)
	}
}

func TestBuildSGR_WithBGColor(t *testing.T) {
	result := buildSGR(vt10x.DefaultFG, vt10x.Color(2), 0)
	// Color 2 = standard green bg, should produce "42"
	if !strings.Contains(result, "42") {
		t.Errorf("buildSGR bg=2 = %q, should contain '42'", result)
	}
}

func TestFgColor_Standard(t *testing.T) {
	// Colors 0-7: 30+n
	result := fgColor(vt10x.Color(0))
	if result != "30" {
		t.Errorf("fgColor(0) = %q, want '30'", result)
	}
	result = fgColor(vt10x.Color(7))
	if result != "37" {
		t.Errorf("fgColor(7) = %q, want '37'", result)
	}
}

func TestFgColor_Bright(t *testing.T) {
	// Colors 8-15: 90+n-8
	result := fgColor(vt10x.Color(8))
	if result != "90" {
		t.Errorf("fgColor(8) = %q, want '90'", result)
	}
	result = fgColor(vt10x.Color(15))
	if result != "97" {
		t.Errorf("fgColor(15) = %q, want '97'", result)
	}
}

func TestFgColor_256(t *testing.T) {
	// Colors 16-255: 38;5;n
	result := fgColor(vt10x.Color(100))
	if result != "38;5;100" {
		t.Errorf("fgColor(100) = %q, want '38;5;100'", result)
	}
}

func TestFgColor_RGB(t *testing.T) {
	// Colors > 255: RGB r<<16|g<<8|b
	rgb := vt10x.Color(0xFF8000) // r=255 g=128 b=0
	result := fgColor(rgb)
	if result != "38;2;255;128;0" {
		t.Errorf("fgColor(RGB) = %q, want '38;2;255;128;0'", result)
	}
}

func TestBgColor_Standard(t *testing.T) {
	result := bgColor(vt10x.Color(0))
	if result != "40" {
		t.Errorf("bgColor(0) = %q, want '40'", result)
	}
}

func TestBgColor_Bright(t *testing.T) {
	result := bgColor(vt10x.Color(8))
	if result != "100" {
		t.Errorf("bgColor(8) = %q, want '100'", result)
	}
}

func TestBgColor_256(t *testing.T) {
	result := bgColor(vt10x.Color(200))
	if result != "48;5;200" {
		t.Errorf("bgColor(200) = %q, want '48;5;200'", result)
	}
}

func TestBgColor_RGB(t *testing.T) {
	rgb := vt10x.Color(0x102030) // r=16 g=32 b=48
	result := bgColor(rgb)
	if result != "48;2;16;32;48" {
		t.Errorf("bgColor(RGB) = %q, want '48;2;16;32;48'", result)
	}
}

func TestPadHeight_Shorter(t *testing.T) {
	result := padHeight("line1\nline2", 5)
	lines := strings.Split(result, "\n")
	if len(lines) != 5 {
		t.Errorf("padHeight shorter: got %d lines, want 5", len(lines))
	}
	if lines[0] != "line1" || lines[1] != "line2" {
		t.Error("original lines should be preserved")
	}
	for i := 2; i < 5; i++ {
		if lines[i] != "" {
			t.Errorf("padded line %d = %q, want empty", i, lines[i])
		}
	}
}

func TestPadHeight_Exact(t *testing.T) {
	result := padHeight("a\nb\nc", 3)
	lines := strings.Split(result, "\n")
	if len(lines) != 3 {
		t.Errorf("padHeight exact: got %d lines, want 3", len(lines))
	}
}

func TestPadHeight_Longer(t *testing.T) {
	result := padHeight("a\nb\nc\nd\ne", 3)
	lines := strings.Split(result, "\n")
	if len(lines) != 3 {
		t.Errorf("padHeight longer: got %d lines, want 3 (truncated)", len(lines))
	}
	if lines[0] != "a" || lines[1] != "b" || lines[2] != "c" {
		t.Error("truncated lines should be the first 3")
	}
}
