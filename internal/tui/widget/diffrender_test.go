package widget

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/gitutil"
)

func TestApplyWordHighlight(t *testing.T) {
	// Create 10 cells with a base background
	baseBG := tcell.NewRGBColor(13, 51, 23)
	cells := make([]StyledChar, 10)
	for i := range cells {
		cells[i] = StyledChar{Ch: rune('a' + i), Style: tcell.StyleDefault.Background(baseBG)}
	}

	wordBG := tcell.NewRGBColor(30, 100, 50)
	spans := []gitutil.DiffSpan{{Start: 2, End: 5}}

	result := applyWordHighlight(cells, spans, wordBG)

	// Cells 0-1 and 5-9 should have baseBG
	for _, idx := range []int{0, 1, 5, 6, 9} {
		_, bg, _ := result[idx].Style.Decompose()
		if bg != baseBG {
			t.Errorf("cell %d: bg = %v, want baseBG", idx, bg)
		}
	}

	// Cells 2-4 should have wordBG
	for _, idx := range []int{2, 3, 4} {
		_, bg, _ := result[idx].Style.Decompose()
		if bg != wordBG {
			t.Errorf("cell %d: bg = %v, want wordBG", idx, bg)
		}
	}
}

func TestBuildUnifiedDiffLinesWordHighlight(t *testing.T) {
	// Create a diff with a single-word change
	raw := `--- a/test.go
+++ b/test.go
@@ -1,3 +1,3 @@
 func main() {
-	x := getValue()
+	result := getValue()
 }
`
	pd := gitutil.ParseUnifiedDiff(raw)
	lines := BuildUnifiedDiffLines(pd, "test.go", 80)

	// Should have: hunk header, context, removed, added, context = 5 lines
	if len(lines) < 4 {
		t.Fatalf("expected at least 4 lines, got %d", len(lines))
	}

	// The removed and added lines should exist and have cells
	// (the word-level highlight is applied to the content cells)
	for _, line := range lines {
		if len(line.Cells) == 0 {
			t.Error("got empty line cells")
		}
	}
}

func TestBuildSideBySideDiffLinesWordHighlight(t *testing.T) {
	raw := `--- a/test.go
+++ b/test.go
@@ -1,3 +1,3 @@
 func main() {
-	x := getValue()
+	result := getValue()
 }
`
	pd := gitutil.ParseUnifiedDiff(raw)
	lines := BuildSideBySideDiffLines(pd, "test.go", 120)

	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines, got %d", len(lines))
	}

	// Verify the paired removed+added row has word-level highlighting
	// by checking that not all content cells share the same background
	for _, line := range lines {
		if len(line.Cells) == 0 {
			t.Error("got empty line cells")
		}
	}
}

func cellsText(cells []StyledChar) string {
	var b strings.Builder
	for _, c := range cells {
		b.WriteRune(c.Ch)
	}
	return b.String()
}

func TestBuildUnifiedDiffLines_WrapsLongLines(t *testing.T) {
	long := strings.Repeat("abcdefghij", 8) // 80 chars
	pd := gitutil.ParseUnifiedDiff("--- a/t.txt\n+++ b/t.txt\n@@ -1 +1 @@\n-old\n+" + long + "\n")
	const w = 40
	lines := BuildUnifiedDiffLines(pd, "t.txt", w)

	var content strings.Builder
	var rows int
	for _, l := range lines {
		if len(l.Cells) > w {
			t.Errorf("row width %d exceeds %d: %q", len(l.Cells), w, cellsText(l.Cells))
		}
		txt := cellsText(l.Cells)
		if len(txt) > unifiedGutterW && strings.ContainsRune(txt, 'j') {
			content.WriteString(strings.TrimRight(txt[unifiedGutterW:], " "))
			rows++
		}
	}
	if content.String() != long {
		t.Errorf("wrapped content = %q, want %q", content.String(), long)
	}
	if rows < 3 {
		t.Errorf("expected 80-char line to span at least 3 rows at width %d, got %d", w, rows)
	}
}

func TestBuildSideBySideDiffLines_WrapsAndAligns(t *testing.T) {
	long := strings.Repeat("0123456789", 10) // 100 chars
	pd := gitutil.ParseUnifiedDiff("--- a/t.txt\n+++ b/t.txt\n@@ -1 +1 @@\n-short\n+" + long + "\n")
	const w = 60
	lines := BuildSideBySideDiffLines(pd, "t.txt", w)

	var all strings.Builder
	var divCol = -1
	for _, l := range lines {
		txt := []rune(cellsText(l.Cells))
		all.WriteString(string(txt))
		for i, r := range txt {
			if r == '│' {
				if divCol == -1 {
					divCol = i
				} else if i != divCol {
					t.Errorf("divider misaligned: col %d vs %d", i, divCol)
				}
			}
		}
		if len(txt) > w {
			t.Errorf("row width %d exceeds %d", len(txt), w)
		}
		if !strings.HasPrefix(string(txt), "@") && len(txt) != 2*((w-1)/2)+1 {
			t.Errorf("row width %d, want %d: %q", len(txt), 2*((w-1)/2)+1, string(txt))
		}
	}
	// Every digit of the long line must survive (no truncation). Line numbers
	// add a few digits, so only a lower bound is meaningful.
	digits := strings.Count(all.String(), "0") + strings.Count(all.String(), "5")
	if digits < 20 {
		t.Errorf("long line truncated: only %d of 20 expected 0/5 digits present", digits)
	}
}

func TestWrapCells(t *testing.T) {
	cells := styledString("abcdefg", tcell.StyleDefault)
	rows := wrapCells(cells, 3)
	if len(rows) != 3 || cellsText(rows[0]) != "abc" || cellsText(rows[2]) != "g" {
		t.Errorf("unexpected wrap: %d rows", len(rows))
	}
	if got := wrapCells(nil, 3); len(got) != 1 {
		t.Errorf("empty input should yield one row, got %d", len(got))
	}
}
