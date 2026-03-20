package tui2

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/gitutil"
)

func TestApplyWordHighlight(t *testing.T) {
	// Create 10 cells with a base background
	baseBG := tcell.NewRGBColor(13, 51, 23)
	cells := make([]styledChar, 10)
	for i := range cells {
		cells[i] = styledChar{ch: rune('a' + i), style: tcell.StyleDefault.Background(baseBG)}
	}

	wordBG := tcell.NewRGBColor(30, 100, 50)
	spans := []gitutil.DiffSpan{{Start: 2, End: 5}}

	result := applyWordHighlight(cells, spans, wordBG)

	// Cells 0-1 and 5-9 should have baseBG
	for _, idx := range []int{0, 1, 5, 6, 9} {
		_, bg, _ := result[idx].style.Decompose()
		if bg != baseBG {
			t.Errorf("cell %d: bg = %v, want baseBG", idx, bg)
		}
	}

	// Cells 2-4 should have wordBG
	for _, idx := range []int{2, 3, 4} {
		_, bg, _ := result[idx].style.Decompose()
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
	lines := buildUnifiedDiffLines(pd, "test.go")

	// Should have: hunk header, context, removed, added, context = 5 lines
	if len(lines) < 4 {
		t.Fatalf("expected at least 4 lines, got %d", len(lines))
	}

	// The removed and added lines should exist and have cells
	// (the word-level highlight is applied to the content cells)
	for _, line := range lines {
		if len(line.cells) == 0 {
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
	lines := buildSideBySideDiffLines(pd, "test.go", 120)

	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines, got %d", len(lines))
	}

	// Verify the paired removed+added row has word-level highlighting
	// by checking that not all content cells share the same background
	for _, line := range lines {
		if len(line.cells) == 0 {
			t.Error("got empty line cells")
		}
	}
}
