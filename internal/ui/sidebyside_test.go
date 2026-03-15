package ui

import (
	"strings"
	"testing"
)

func TestRenderSideBySide_BasicOutput(t *testing.T) {
	rows := []SideBySideLine{
		{LeftNum: 1, LeftText: "old line", LeftType: DiffRemoved,
			RightNum: 1, RightText: "new line", RightType: DiffAdded},
		{LeftNum: 2, LeftText: "same", LeftType: DiffContext,
			RightNum: 2, RightText: "same", RightType: DiffContext},
	}

	theme := DefaultTheme()
	output := RenderSideBySide(rows, "test.go", 80, 10, 0, theme)

	if output == "" {
		t.Fatal("expected non-empty output")
	}
	// Should contain the divider character
	if !strings.Contains(output, "│") {
		t.Error("expected side-by-side divider '│'")
	}
}

func TestRenderSideBySide_EmptyRows(t *testing.T) {
	theme := DefaultTheme()
	output := RenderSideBySide(nil, "test.go", 80, 10, 0, theme)
	if !strings.Contains(output, "no diff") {
		t.Errorf("expected 'no diff' for empty rows, got %q", output)
	}
}

func TestRenderSideBySide_ScrollOffset(t *testing.T) {
	rows := make([]SideBySideLine, 20)
	for i := range rows {
		rows[i] = SideBySideLine{
			LeftNum: i + 1, LeftText: "left", LeftType: DiffContext,
			RightNum: i + 1, RightText: "right", RightType: DiffContext,
		}
	}

	theme := DefaultTheme()
	// Scroll to offset 10, visible 5
	output := RenderSideBySide(rows, "test.go", 80, 5, 10, theme)
	if output == "" {
		t.Fatal("expected non-empty output with scroll offset")
	}
	// Count rendered lines
	lines := strings.Split(output, "\n")
	if len(lines) > 5 {
		t.Errorf("expected at most 5 visible lines, got %d", len(lines))
	}
}

func TestRenderSideBySide_HunkHeader(t *testing.T) {
	rows := []SideBySideLine{
		{LeftText: "@@ -1,3 +1,3 @@", RightText: "@@ -1,3 +1,3 @@",
			LeftType: DiffContext, RightType: DiffContext},
		{LeftNum: 1, LeftText: "line", LeftType: DiffContext,
			RightNum: 1, RightText: "line", RightType: DiffContext},
	}

	theme := DefaultTheme()
	output := RenderSideBySide(rows, "test.go", 80, 10, 0, theme)
	if !strings.Contains(output, "@@") {
		t.Error("expected hunk header in output")
	}
}

func TestRenderSideBySide_Separator(t *testing.T) {
	rows := []SideBySideLine{
		{LeftNum: 1, LeftText: "line", LeftType: DiffContext,
			RightNum: 1, RightText: "line", RightType: DiffContext},
		{LeftText: "───", RightText: "───"},
		{LeftNum: 10, LeftText: "line10", LeftType: DiffContext,
			RightNum: 10, RightText: "line10", RightType: DiffContext},
	}

	theme := DefaultTheme()
	output := RenderSideBySide(rows, "test.go", 80, 10, 0, theme)
	if !strings.Contains(output, "─") {
		t.Error("expected separator in output")
	}
}

func TestRenderDiffHeader(t *testing.T) {
	theme := DefaultTheme()
	header := RenderDiffHeader("main.go", 0, 5, "split", theme)
	if !strings.Contains(header, "DIFF") {
		t.Error("expected 'DIFF' in header")
	}
	if !strings.Contains(header, "main.go") {
		t.Error("expected filename in header")
	}
	if !strings.Contains(header, "[1/5]") {
		t.Error("expected file count in header")
	}
	if !strings.Contains(header, "split") {
		t.Error("expected mode label in header")
	}
}

func TestFormatSideContent_Removed(t *testing.T) {
	removedStyle := DefaultTheme().Error
	addedStyle := DefaultTheme().Complete
	result := formatSideContent("old code", "old code", DiffRemoved, 20, removedStyle, addedStyle)
	if !strings.Contains(result, "-") {
		t.Error("removed line should have '-' prefix")
	}
}

func TestFormatSideContent_Added(t *testing.T) {
	removedStyle := DefaultTheme().Error
	addedStyle := DefaultTheme().Complete
	result := formatSideContent("new code", "new code", DiffAdded, 20, removedStyle, addedStyle)
	if !strings.Contains(result, "+") {
		t.Error("added line should have '+' prefix")
	}
}

func TestFormatSideContent_Context(t *testing.T) {
	removedStyle := DefaultTheme().Error
	addedStyle := DefaultTheme().Complete
	result := formatSideContent("same", "same", DiffContext, 20, removedStyle, addedStyle)
	if result == "" {
		t.Error("context line should not be empty")
	}
}

func TestTruncatePlain(t *testing.T) {
	tests := []struct {
		input string
		maxW  int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello"},
		{"", 5, ""},
		{"abc", 3, "abc"},
	}
	for _, tc := range tests {
		got := truncatePlain(tc.input, tc.maxW)
		if got != tc.want {
			t.Errorf("truncatePlain(%q, %d) = %q, want %q", tc.input, tc.maxW, got, tc.want)
		}
	}
}

func TestRenderSideBySide_NarrowWidth(t *testing.T) {
	rows := []SideBySideLine{
		{LeftNum: 1, LeftText: "short", LeftType: DiffContext,
			RightNum: 1, RightText: "short", RightType: DiffContext},
	}
	theme := DefaultTheme()
	// Very narrow width
	output := RenderSideBySide(rows, "test.go", 20, 5, 0, theme)
	if output == "" {
		t.Fatal("should render even with narrow width")
	}
}
