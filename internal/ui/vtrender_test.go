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

func TestRenderLine_HighlightsActiveInputRow(t *testing.T) {
	vt := vt10x.New(vt10x.WithSize(20, 2))
	vt.Write([]byte("Summarize recent com"))

	line := renderLine(vt, 0, 20, 5)

	if !strings.Contains(line, "\x1b[0;48;5;17m") {
		t.Fatalf("expected active row background highlight, got %q", line)
	}
	if !strings.Contains(line, "\x1b[0;38;5;17;48;5;153m") {
		t.Fatalf("expected explicit cursor styling, got %q", line)
	}
}
