package ui

import (
	"strings"
	"testing"

	"github.com/hinshun/vt10x"
)

func TestReplayVT10X_TrimsLeadingEmptyLines(t *testing.T) {
	// Codex-style TUI: positions content in the bottom portion of the terminal,
	// leaving the top rows empty. replayVT10X should trim those leading empty rows.
	// Use cursor-positioning to put text at row 5 of a 10-row terminal.
	raw := []byte(
		"\x1b[5;1H" + // move to row 5, col 1
			"Hello from Codex" +
			"\x1b[6;1H" + // row 6
			"Second line",
	)

	lines := replayVT10X(raw, 40, 10, true)

	if len(lines) == 0 {
		t.Fatal("expected non-empty lines")
	}

	// Leading empty rows should be trimmed — first line should have content.
	first := stripANSI(lines[0])
	if strings.TrimSpace(first) == "" {
		t.Errorf("leading empty line not trimmed: first line = %q", lines[0])
	}
	if !strings.Contains(first, "Hello from Codex") {
		t.Errorf("expected first line to contain 'Hello from Codex', got %q", first)
	}
}

func TestReplayVT10X_TrimsTrailingEmptyLines(t *testing.T) {
	// Content only at top — trailing empty rows should be trimmed.
	raw := []byte(
		"\x1b[1;1H" + "Top line",
	)

	lines := replayVT10X(raw, 40, 10, true)

	if len(lines) == 0 {
		t.Fatal("expected non-empty lines")
	}

	// Should only have the one content line (trailing rows trimmed).
	if len(lines) != 1 {
		t.Errorf("expected 1 line after trailing trim, got %d", len(lines))
	}
	if !strings.Contains(stripANSI(lines[0]), "Top line") {
		t.Errorf("expected 'Top line', got %q", lines[0])
	}
}

func TestReplayVT10X_EmptyTerminal(t *testing.T) {
	raw := []byte{}
	lines := replayVT10X(raw, 40, 10, true)
	if len(lines) != 0 {
		t.Errorf("expected empty lines for empty input, got %d", len(lines))
	}
}

// TestRenderLine_DimAttribute verifies that SGR 2 (dim/faint) is tracked and
// emitted correctly. Codex uses dim for placeholder text and box borders; without
// this, all UI elements render at full brightness making placeholder text
// indistinguishable from regular input.
func TestRenderLine_DimAttribute(t *testing.T) {
	// Simulate codex-style input line: bold prompt + dim placeholder text.
	// \x1b[1m = bold, \x1b[2m = dim, \x1b[0m = reset
	raw := []byte("\x1b[1;1H\x1b[1m>\x1b[0m \x1b[2mType a message\x1b[0m")
	lines := replayVT10X(raw, 40, 5, false)
	if len(lines) == 0 {
		t.Fatal("expected non-empty output")
	}

	line := lines[0]
	// The dim SGR code "2" must appear in the rendered ANSI for the placeholder.
	if !strings.Contains(line, "\x1b[") {
		t.Errorf("expected ANSI sequences in output, got %q", line)
	}
	// Strip ANSI and verify content is preserved.
	plain := stripANSI(line)
	if !strings.Contains(plain, ">") {
		t.Errorf("bold prompt '>' not found in %q", plain)
	}
	if !strings.Contains(plain, "Type a message") {
		t.Errorf("placeholder text not found in %q", plain)
	}
	// The dim attribute (SGR 2) must appear somewhere after the bold prompt.
	promptIdx := strings.Index(line, ">")
	if promptIdx < 0 {
		t.Fatalf("could not find '>' in %q", line)
	}
	if !strings.Contains(line[promptIdx:], "2m") {
		t.Errorf("dim SGR code not emitted after prompt in %q", line)
	}
}

// TestBuildSGR_Dim verifies buildSGR emits SGR 2 for dim cells with default colors.
// Codex uses dim for placeholder text and box borders — the rendered ANSI must
// include the dim code so text appears faint in the terminal.
func TestBuildSGR_Dim(t *testing.T) {
	sgr := buildSGR(vt10x.DefaultFG, vt10x.DefaultBG, vtAttrDim)
	// With only the dim bit set and default colors, the SGR must be exactly \x1b[0;2m
	if sgr != "\x1b[0;2m" {
		t.Errorf("expected \\x1b[0;2m for dim+defaults, got %q", sgr)
	}
}

// TestBuildSGR_DimReset verifies dim is absent when mode has no dim bit.
func TestBuildSGR_DimReset(t *testing.T) {
	sgr := buildSGR(vt10x.DefaultFG, vt10x.DefaultBG, 0)
	// No attributes, no colors: must be just the reset.
	if sgr != "\x1b[0m" {
		t.Errorf("expected \\x1b[0m for no attrs, got %q", sgr)
	}
}
