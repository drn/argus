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

	// Empty terminal has no content anywhere. findInputRow falls back to
	// curY=0, but after trimming (the rendered row is all spaces) we get 0
	// lines — an empty panel is the right result.
	lines := replayVT10X(raw, 40, 10, true)
	if len(lines) != 0 {
		t.Errorf("expected 0 lines for empty terminal, got %d", len(lines))
	}

	lines = replayVT10X(raw, 40, 10, false)
	if len(lines) != 0 {
		t.Errorf("expected 0 lines for empty terminal without cursor, got %d", len(lines))
	}
}

func TestReplayVT10X_CursorParkedBelowContent(t *testing.T) {
	// Simulate Claude Code: content at row 0, cursor parked at an empty row
	// below (Ink hides hardware cursor with \x1b[?25l and parks it below the
	// TUI). findInputRow should find the content row; the empty parking rows
	// should be trimmed away; no stray cursor block on the empty row.
	raw := []byte(
		"\x1b[1;1H" + "Hello" + // content at row 1
			"\x1b[?25l" + // hide hardware cursor (Claude Code behavior)
			"\x1b[5;3H", // move cursor to row 5, col 3 (empty row)
	)

	lines := replayVT10X(raw, 40, 10, true)

	// Empty rows below content are trimmed; only the content row remains.
	if len(lines) != 1 {
		t.Fatalf("expected 1 line (content row, empty rows trimmed), got %d", len(lines))
	}

	// The content row gets the inputRow highlight, not the cursor block.
	contentLine := lines[0]
	if !strings.Contains(contentLine, "Hello") {
		t.Errorf("expected content line to contain 'Hello', got %q", contentLine)
	}
	// No cursor block — cursor is parked on a different (empty) row.
	if strings.Contains(contentLine, "\x1b[0;38;5;17;48;5;153m") {
		t.Errorf("cursor styling should not appear on content line when cursor is parked elsewhere: %q", contentLine)
	}
}

func TestRenderLine_HighlightsActiveInputRow(t *testing.T) {
	vt := vt10x.New(vt10x.WithSize(20, 2))
	vt.Write([]byte("Summarize recent com"))

	line := renderLine(vt, 0, 20, 5, true)

	if !strings.Contains(line, "\x1b[0;48;5;17m") {
		t.Fatalf("expected active row background highlight, got %q", line)
	}
	if !strings.Contains(line, "\x1b[0;38;5;17;48;5;153m") {
		t.Fatalf("expected explicit cursor styling, got %q", line)
	}
}

// TestFindInputRow_CursorOnContentRow verifies that when the cursor sits on a
// row with visible characters (e.g. Codex), findInputRow returns that row.
func TestFindInputRow_CursorOnContentRow(t *testing.T) {
	vt := vt10x.New(vt10x.WithSize(20, 5))
	vt.Write([]byte("hello"))
	vt.Lock()
	defer vt.Unlock()

	row := findInputRow(vt, 0, 20)
	if row != 0 {
		t.Errorf("findInputRow: cursor on content row, got %d want 0", row)
	}
}

// TestFindInputRow_CursorOnEmptyRow verifies that when the cursor is parked at
// an empty trailing row, findInputRow scans upward to the last non-empty row.
func TestFindInputRow_CursorOnEmptyRow(t *testing.T) {
	vt := vt10x.New(vt10x.WithSize(20, 5))
	vt.Write([]byte("> user input\n"))
	vt.Lock()
	defer vt.Unlock()

	// Cursor at row 1 (empty). Input content at row 0.
	row := findInputRow(vt, 1, 20)
	if row != 0 {
		t.Errorf("findInputRow: cursor on empty row, got %d want 0", row)
	}
}

// TestFindInputRow_PromptMarker verifies that ❯ is preferred over a tip row.
// Claude's layout: input (❯), tip row, empty rows, cursor.
func TestFindInputRow_PromptMarker(t *testing.T) {
	vt := vt10x.New(vt10x.WithSize(40, 5))
	// Row 0: Claude input prompt with ❯
	// Row 1: tip/hint text (no ❯)
	// Rows 2+: empty; cursor ends at row 2.
	vt.Write([]byte("❯ command\n> Tip: ...\n"))
	vt.Lock()
	defer vt.Unlock()

	// Should return row 0 (prompt marker), not row 1 (tip).
	row := findInputRow(vt, 2, 40)
	if row != 0 {
		t.Errorf("findInputRow: ❯ marker should win over tip row, got %d want 0", row)
	}
}

// TestFindInputRow_FallbackWithoutMarker verifies the fallback to last
// non-empty row when no ❯ marker is present (e.g. Codex).
func TestFindInputRow_FallbackWithoutMarker(t *testing.T) {
	vt := vt10x.New(vt10x.WithSize(20, 5))
	// Row 0: input; row 1: tip (no ❯ anywhere); cursor at row 2.
	vt.Write([]byte("> command\n> Tip: ...\n"))
	vt.Lock()
	defer vt.Unlock()

	// No ❯ found — fallback is last non-empty row (row 1).
	row := findInputRow(vt, 2, 20)
	if row != 1 {
		t.Errorf("findInputRow: no marker fallback, got %d want 1", row)
	}
}
