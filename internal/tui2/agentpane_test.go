package tui2

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestAgentPane_SetSession(t *testing.T) {
	ap := NewAgentPane()
	if ap.session != nil {
		t.Error("initial session should be nil")
	}

	ap.SetTaskID("task-1")
	if ap.taskID != "task-1" {
		t.Errorf("taskID = %q, want task-1", ap.taskID)
	}

	ap.SetFocused(true)
	if !ap.focused {
		t.Error("should be focused")
	}
}

func TestParseDiffLines(t *testing.T) {
	diff := `--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,4 @@
 context line
-removed line
+added line
+another added
 more context`

	lines := parseDiffLines(diff)

	if len(lines) == 0 {
		t.Fatal("expected parsed diff lines")
	}

	// Check types
	types := make(map[diffLineType]int)
	for _, l := range lines {
		types[l.lineType]++
	}

	if types[diffAdded] < 2 {
		t.Errorf("expected >= 2 added lines, got %d", types[diffAdded])
	}
	if types[diffRemoved] < 1 {
		t.Errorf("expected >= 1 removed lines, got %d", types[diffRemoved])
	}
	if types[diffHeader] < 1 {
		t.Errorf("expected >= 1 header lines, got %d", types[diffHeader])
	}
}

func TestParseDiffLinesEmpty(t *testing.T) {
	lines := parseDiffLines("")
	if lines != nil {
		t.Errorf("expected nil for empty diff, got %d lines", len(lines))
	}
}

func TestAgentPaneDiffMode(t *testing.T) {
	ap := NewAgentPane()
	if ap.InDiffMode() {
		t.Error("should not be in diff mode initially")
	}

	ap.EnterDiffMode("+added\n-removed\n context", "test.go")
	if !ap.InDiffMode() {
		t.Error("should be in diff mode after EnterDiffMode")
	}

	ap.ExitDiffMode()
	if ap.InDiffMode() {
		t.Error("should not be in diff mode after ExitDiffMode")
	}
}

func TestAgentPaneDrawZeroDims(t *testing.T) {
	ap := NewAgentPane()
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.Init()
	screen.SetSize(80, 24)

	// Should not panic with zero inner rect
	ap.Draw(screen)
}

func TestAgentPaneDrawPlaceholder(t *testing.T) {
	ap := NewAgentPane()
	ap.SetTaskID("test-task")
	ap.SetRect(1, 1, 60, 20)

	screen := tcell.NewSimulationScreen("UTF-8")
	screen.Init()
	screen.SetSize(80, 24)

	// Should render placeholder without panic
	ap.Draw(screen)
}

func TestAgentPaneScrollback(t *testing.T) {
	ap := NewAgentPane()
	ap.ScrollUp(5)
	if ap.ScrollOffset() != 5 {
		t.Errorf("ScrollOffset() = %d, want 5", ap.ScrollOffset())
	}
	ap.ScrollDown(3)
	if ap.ScrollOffset() != 2 {
		t.Errorf("ScrollOffset() = %d, want 2", ap.ScrollOffset())
	}
	ap.ScrollToBottom()
	if ap.ScrollOffset() != 0 {
		t.Errorf("ScrollOffset() = %d, want 0", ap.ScrollOffset())
	}
}

func TestDiffLineStyle(t *testing.T) {
	added := diffLineStyle(diffAdded)
	removed := diffLineStyle(diffRemoved)
	context := diffLineStyle(diffContext)
	header := diffLineStyle(diffHeader)

	addedFG, _, _ := added.Decompose()
	removedFG, _, _ := removed.Decompose()
	contextFG, _, _ := context.Decompose()
	headerFG, _, _ := header.Decompose()

	if addedFG == removedFG {
		t.Error("added and removed should have different colors")
	}
	if contextFG == addedFG {
		t.Error("context and added should have different colors")
	}
	if headerFG == contextFG {
		t.Error("header and context should have different colors")
	}
}

func TestDrawBorder(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.Init()
	screen.SetSize(20, 10)

	drawBorder(screen, 0, 0, 10, 5, StyleBorder)

	// Check corners
	ch, _, _, _ := screen.GetContent(0, 0)
	if ch != '\u256d' {
		t.Errorf("top-left corner = %q, want \u256d", ch)
	}
	ch, _, _, _ = screen.GetContent(9, 0)
	if ch != '\u256e' {
		t.Errorf("top-right corner = %q, want \u256e", ch)
	}
}

func TestDrawBorderTooSmall(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.Init()
	screen.SetSize(20, 10)

	// Should not panic with tiny dimensions
	drawBorder(screen, 0, 0, 1, 1, StyleBorder)
	drawBorder(screen, 0, 0, 0, 0, StyleBorder)
}
