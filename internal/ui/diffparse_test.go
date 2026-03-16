package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestParseUnifiedDiff_Basic(t *testing.T) {
	raw := `diff --git a/main.go b/main.go
index abc123..def456 100644
--- a/main.go
+++ b/main.go
@@ -1,5 +1,5 @@
 package main

-import "fmt"
+import "log"

 func main() {`

	pd := ParseUnifiedDiff(raw)
	if pd.OldFile != "main.go" {
		t.Errorf("OldFile = %q, want %q", pd.OldFile, "main.go")
	}
	if pd.NewFile != "main.go" {
		t.Errorf("NewFile = %q, want %q", pd.NewFile, "main.go")
	}
	if len(pd.Hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(pd.Hunks))
	}
	h := pd.Hunks[0]
	if h.OldStart != 1 || h.OldCount != 5 {
		t.Errorf("old range = %d,%d, want 1,5", h.OldStart, h.OldCount)
	}
	if h.NewStart != 1 || h.NewCount != 5 {
		t.Errorf("new range = %d,%d, want 1,5", h.NewStart, h.NewCount)
	}

	// Should have: context, context, removed, added, context, context
	var removed, added, context int
	for _, dl := range h.Lines {
		switch dl.Type {
		case DiffRemoved:
			removed++
		case DiffAdded:
			added++
		case DiffContext:
			context++
		}
	}
	if removed != 1 {
		t.Errorf("removed lines = %d, want 1", removed)
	}
	if added != 1 {
		t.Errorf("added lines = %d, want 1", added)
	}
	if context < 3 {
		t.Errorf("context lines = %d, want >= 3", context)
	}
}

func TestParseUnifiedDiff_MultipleHunks(t *testing.T) {
	raw := `--- a/file.go
+++ b/file.go
@@ -1,3 +1,3 @@
 line1
-old2
+new2
 line3
@@ -10,3 +10,4 @@
 line10
+inserted
 line11
 line12`

	pd := ParseUnifiedDiff(raw)
	if len(pd.Hunks) != 2 {
		t.Fatalf("expected 2 hunks, got %d", len(pd.Hunks))
	}
	if pd.Hunks[1].NewStart != 10 {
		t.Errorf("hunk 2 NewStart = %d, want 10", pd.Hunks[1].NewStart)
	}
}

func TestParseUnifiedDiff_Empty(t *testing.T) {
	pd := ParseUnifiedDiff("")
	if len(pd.Hunks) != 0 {
		t.Errorf("expected 0 hunks for empty input, got %d", len(pd.Hunks))
	}
}

func TestParseUnifiedDiff_NewFile(t *testing.T) {
	raw := `diff --git a/new.go b/new.go
new file mode 100644
--- /dev/null
+++ b/new.go
@@ -0,0 +1,3 @@
+package main
+
+func hello() {}`

	pd := ParseUnifiedDiff(raw)
	if pd.NewFile != "new.go" {
		t.Errorf("NewFile = %q, want %q", pd.NewFile, "new.go")
	}
	if len(pd.Hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(pd.Hunks))
	}
	// All lines should be added
	for _, dl := range pd.Hunks[0].Lines {
		if dl.Type != DiffAdded {
			t.Errorf("expected all lines to be Added, got %v for %q", dl.Type, dl.Content)
		}
	}
}

func TestParseHunkHeader(t *testing.T) {
	tests := []struct {
		input                            string
		oldStart, oldCount, newStart, newCount int
	}{
		{"@@ -1,5 +1,5 @@", 1, 5, 1, 5},
		{"@@ -10,3 +10,4 @@ func main()", 10, 3, 10, 4},
		{"@@ -1 +1,2 @@", 1, 1, 1, 2},
		{"@@ -0,0 +1,3 @@", 1, 0, 1, 3},
	}
	for _, tc := range tests {
		h := parseHunkHeader(tc.input)
		if h.OldStart != tc.oldStart || h.OldCount != tc.oldCount {
			t.Errorf("parseHunkHeader(%q): old = %d,%d, want %d,%d",
				tc.input, h.OldStart, h.OldCount, tc.oldStart, tc.oldCount)
		}
		if h.NewStart != tc.newStart || h.NewCount != tc.newCount {
			t.Errorf("parseHunkHeader(%q): new = %d,%d, want %d,%d",
				tc.input, h.NewStart, h.NewCount, tc.newStart, tc.newCount)
		}
	}
}

func TestBuildSideBySide_PairsRemovedAdded(t *testing.T) {
	pd := ParsedDiff{
		Hunks: []DiffHunk{
			{
				OldStart: 1, OldCount: 3, NewStart: 1, NewCount: 3,
				Header: "@@ -1,3 +1,3 @@",
				Lines: []DiffLine{
					{Type: DiffContext, Content: "line1", OldNum: 1, NewNum: 1},
					{Type: DiffRemoved, Content: "old2", OldNum: 2},
					{Type: DiffAdded, Content: "new2", NewNum: 2},
					{Type: DiffContext, Content: "line3", OldNum: 3, NewNum: 3},
				},
			},
		},
	}

	rows := BuildSideBySide(pd)
	// Should have: header + context + paired(removed,added) + context = 4 rows
	if len(rows) < 4 {
		t.Fatalf("expected at least 4 rows, got %d", len(rows))
	}

	// Find the paired row (removed on left, added on right)
	found := false
	for _, r := range rows {
		if r.LeftType == DiffRemoved && r.RightType == DiffAdded {
			if r.LeftText != "old2" || r.RightText != "new2" {
				t.Errorf("paired row: left=%q right=%q, want old2/new2", r.LeftText, r.RightText)
			}
			found = true
		}
	}
	if !found {
		t.Error("expected a row with removed left and added right (paired)")
	}
}

func TestBuildSideBySide_AddedOnly(t *testing.T) {
	pd := ParsedDiff{
		Hunks: []DiffHunk{
			{
				Header: "@@ -1,2 +1,3 @@",
				Lines: []DiffLine{
					{Type: DiffContext, Content: "line1", OldNum: 1, NewNum: 1},
					{Type: DiffAdded, Content: "inserted", NewNum: 2},
					{Type: DiffContext, Content: "line2", OldNum: 2, NewNum: 3},
				},
			},
		},
	}

	rows := BuildSideBySide(pd)
	// Find the added-only row
	found := false
	for _, r := range rows {
		if r.RightType == DiffAdded && r.RightText == "inserted" {
			if r.LeftNum != 0 {
				t.Errorf("added-only row should have LeftNum=0, got %d", r.LeftNum)
			}
			found = true
		}
	}
	if !found {
		t.Error("expected an added-only row with 'inserted'")
	}
}

func TestFormatLineNum(t *testing.T) {
	tests := []struct {
		n, w int
		want string
	}{
		{0, 4, "    "},
		{1, 4, "   1"},
		{42, 4, "  42"},
		{9999, 4, "9999"},
		{12345, 4, "2345"}, // overflow: truncated
	}
	for _, tc := range tests {
		got := FormatLineNum(tc.n, tc.w)
		if got != tc.want {
			t.Errorf("FormatLineNum(%d, %d) = %q, want %q", tc.n, tc.w, got, tc.want)
		}
	}
}

func TestParseRange(t *testing.T) {
	tests := []struct {
		input      string
		wantStart  int
		wantCount  int
	}{
		{"1,5", 1, 5},
		{"10,3", 10, 3},
		{"1", 1, 1},
		{"0,0", 1, 0}, // 0 start normalized to 1
	}
	for _, tc := range tests {
		start, count := parseRange(tc.input)
		if start != tc.wantStart || count != tc.wantCount {
			t.Errorf("parseRange(%q) = %d,%d, want %d,%d", tc.input, start, count, tc.wantStart, tc.wantCount)
		}
	}
}

func TestCollectRun(t *testing.T) {
	lines := []DiffLine{
		{Type: DiffRemoved, Content: "a"},
		{Type: DiffRemoved, Content: "b"},
		{Type: DiffAdded, Content: "c"},
		{Type: DiffContext, Content: "d"},
	}
	run := collectRun(lines, 0, DiffRemoved)
	if len(run) != 2 {
		t.Errorf("collectRun removed: got %d, want 2", len(run))
	}
	run = collectRun(lines, 2, DiffAdded)
	if len(run) != 1 {
		t.Errorf("collectRun added: got %d, want 1", len(run))
	}
	run = collectRun(lines, 0, DiffAdded)
	if len(run) != 0 {
		t.Errorf("collectRun mismatch: got %d, want 0", len(run))
	}
}

func TestExpandTabs(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"no tabs", "no tabs"},
		{"\tindented", "  indented"},
		{"\t\tdouble", "    double"},
		{"mid\ttab", "mid  tab"},
		{"", ""},
	}
	for _, tc := range tests {
		got := expandTabs(tc.input)
		if got != tc.want {
			t.Errorf("expandTabs(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestParseUnifiedDiff_TabsExpanded(t *testing.T) {
	raw := "--- a/main.go\n+++ b/main.go\n@@ -1,3 +1,3 @@\n \tline1\n-\told\n+\tnew\n \tline3"
	pd := ParseUnifiedDiff(raw)
	if len(pd.Hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(pd.Hunks))
	}
	for _, dl := range pd.Hunks[0].Lines {
		if strings.Contains(dl.Content, "\t") {
			t.Errorf("Content should not contain tabs after parsing, got %q", dl.Content)
		}
	}
}

func TestRenderSideBySide_ConsistentDividerPosition(t *testing.T) {
	// Diff with tabs — after expansion, divider must be at the same column on every row
	raw := "--- a/main.go\n+++ b/main.go\n@@ -1,3 +1,4 @@\n \tline1\n+\tinserted\n \tline2\n \tline3"
	pd := ParseUnifiedDiff(raw)
	rows := BuildSideBySide(pd)
	theme := DefaultTheme()
	output := RenderSideBySide(rows, "main.go", 100, 20, 0, theme)

	lines := strings.Split(output, "\n")
	divPos := -1
	for i, line := range lines {
		stripped := ansi.Strip(line)
		pos := strings.Index(stripped, "│")
		if pos < 0 {
			continue // hunk header or separator
		}
		if divPos == -1 {
			divPos = pos
		} else if pos != divPos {
			t.Errorf("line %d: divider at col %d, expected %d\n  %q", i, pos, divPos, stripped)
		}
	}
}

func TestParseUnifiedDiff_NoNewlineMarker(t *testing.T) {
	raw := `--- a/file.txt
+++ b/file.txt
@@ -1,2 +1,2 @@
-old line
+new line
\ No newline at end of file`

	pd := ParseUnifiedDiff(raw)
	if len(pd.Hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(pd.Hunks))
	}
	// The "no newline" marker should be skipped
	for _, dl := range pd.Hunks[0].Lines {
		if strings.Contains(dl.Content, "No newline") {
			t.Error("should not include 'No newline' marker in parsed lines")
		}
	}
}
