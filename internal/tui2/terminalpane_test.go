package tui2

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/hinshun/vt10x"
)

func TestTerminalPane_SetSession(t *testing.T) {
	tp := NewTerminalPane()
	if tp.Session() != nil {
		t.Error("initial session should be nil")
	}
	tp.SetTaskID("task-1")
	if tp.taskID != "task-1" {
		t.Errorf("taskID = %q, want task-1", tp.taskID)
	}
	tp.SetFocused(true)
	if !tp.focused {
		t.Error("should be focused")
	}
	tp.SetPRURL("https://github.com/foo/bar/pull/1")
	if tp.taskPR != "https://github.com/foo/bar/pull/1" {
		t.Errorf("taskPR = %q", tp.taskPR)
	}
}

func TestTerminalPane_Scrollback(t *testing.T) {
	tp := NewTerminalPane()
	tp.ScrollUp(5)
	if tp.ScrollOffset() != 5 {
		t.Errorf("scrollOffset = %d, want 5", tp.ScrollOffset())
	}
	tp.ScrollDown(3)
	if tp.ScrollOffset() != 2 {
		t.Errorf("scrollOffset = %d, want 2", tp.ScrollOffset())
	}
	tp.ScrollDown(10)
	if tp.ScrollOffset() != 0 {
		t.Errorf("scrollOffset = %d, want 0", tp.ScrollOffset())
	}
	tp.ScrollUp(10)
	tp.ResetScroll()
	if tp.ScrollOffset() != 0 {
		t.Errorf("after reset scrollOffset = %d, want 0", tp.ScrollOffset())
	}
}

func TestTerminalPane_ResetVT(t *testing.T) {
	tp := NewTerminalPane()
	tp.vtTerm = vt10x.New(vt10x.WithSize(80, 24))
	tp.vtFedTotal = 100
	tp.scrollOffset = 5

	tp.ResetVT()

	if tp.vtTerm != nil {
		t.Error("vtTerm should be nil after reset")
	}
	if tp.vtFedTotal != 0 {
		t.Errorf("vtFedTotal = %d, want 0", tp.vtFedTotal)
	}
	if tp.scrollOffset != 0 {
		t.Errorf("scrollOffset = %d, want 0", tp.scrollOffset)
	}
}

func TestTerminalPane_HasContent(t *testing.T) {
	tp := NewTerminalPane()
	if tp.HasContent() {
		t.Error("empty pane should not have content")
	}
	tp.replayData = []byte("hello")
	if !tp.HasContent() {
		t.Error("pane with replay data should have content")
	}
}

func TestTerminalPane_DiffMode(t *testing.T) {
	tp := NewTerminalPane()
	if tp.InDiffMode() {
		t.Error("should not be in diff mode initially")
	}
	tp.EnterDiffMode("+added\n-removed\n context", "test.go")
	if !tp.InDiffMode() {
		t.Error("should be in diff mode")
	}
	if len(tp.diffContent) == 0 {
		t.Error("diff content should be populated")
	}
	tp.ExitDiffMode()
	if tp.InDiffMode() {
		t.Error("should not be in diff mode after exit")
	}
}

func TestVtColorToTcell(t *testing.T) {
	tests := []struct {
		name  string
		color vt10x.Color
		want  tcell.Color
	}{
		{"default_fg", vt10x.DefaultFG, tcell.ColorDefault},
		{"default_bg", vt10x.DefaultBG, tcell.ColorDefault},
		{"black", 0, tcell.PaletteColor(0)},
		{"red", 1, tcell.PaletteColor(1)},
		{"xterm_87", 87, tcell.PaletteColor(87)},
		{"xterm_255", 255, tcell.PaletteColor(255)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := vtColorToTcell(tt.color)
			if got != tt.want {
				t.Errorf("vtColorToTcell(%d) = %v, want %v", tt.color, got, tt.want)
			}
		})
	}
}

func TestCellStyle(t *testing.T) {
	// Bold + red foreground
	cell := vt10x.Glyph{
		Char: 'A',
		FG:   1,
		BG:   vt10x.DefaultBG,
		Mode: vtAttrBold,
	}
	style := cellStyle(cell)
	fg, bg, attr := style.Decompose()
	if fg != tcell.PaletteColor(1) {
		t.Errorf("fg = %v, want PaletteColor(1)", fg)
	}
	if bg != tcell.ColorDefault {
		t.Errorf("bg = %v, want ColorDefault", bg)
	}
	if attr&tcell.AttrBold == 0 {
		t.Error("expected bold attribute")
	}
}

func TestCellStyle_NoActiveInputBG(t *testing.T) {
	// Default-colored cell should NOT get activeInputBG tinting.
	// The native surface shows upstream PTY output as-is.
	cell := vt10x.Glyph{
		Char: ' ',
		FG:   vt10x.DefaultFG,
		BG:   vt10x.DefaultBG,
		Mode: 0,
	}
	style := cellStyle(cell)
	_, bg, _ := style.Decompose()
	if bg != tcell.ColorDefault {
		t.Errorf("default cell bg = %v, want ColorDefault (no activeInputBG)", bg)
	}
}

func TestRowHasContent(t *testing.T) {
	vt := vt10x.New(vt10x.WithSize(20, 5))
	vt.Write([]byte("hello\n"))
	vt.Lock()
	defer vt.Unlock()

	if !rowHasContent(vt, 0, 20) {
		t.Error("row 0 should have content")
	}
	if rowHasContent(vt, 2, 20) {
		t.Error("row 2 should be empty")
	}
}

func TestFindContentRows(t *testing.T) {
	vt := vt10x.New(vt10x.WithSize(20, 10))
	vt.Write([]byte("\n\nhello\nworld\n"))
	vt.Lock()
	defer vt.Unlock()

	last := findLastContentRow(vt, 20, 10)
	if last < 2 {
		t.Errorf("findLastContentRow = %d, want >= 2", last)
	}
	first := findFirstContentRow(vt, 20, last)
	if first > 3 {
		t.Errorf("findFirstContentRow = %d, want <= 3", first)
	}
}

func TestEstimateVTRows(t *testing.T) {
	raw := []byte("line1\nline2\nline3\nline4\nline5\n")
	rows := estimateVTRows(raw, 80, 10)
	if rows < 10 {
		t.Errorf("estimateVTRows = %d, want >= 10", rows)
	}
}

func TestParseDiffLines(t *testing.T) {
	diff := "+added\n-removed\n context\n@@ header"
	lines := parseDiffLines(diff)
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d", len(lines))
	}
	if lines[0].lineType != diffAdded {
		t.Errorf("line 0 type = %d, want diffAdded", lines[0].lineType)
	}
	if lines[1].lineType != diffRemoved {
		t.Errorf("line 1 type = %d, want diffRemoved", lines[1].lineType)
	}
	if lines[2].lineType != diffContext {
		t.Errorf("line 2 type = %d, want diffContext", lines[2].lineType)
	}
	if lines[3].lineType != diffHeader {
		t.Errorf("line 3 type = %d, want diffHeader", lines[3].lineType)
	}
}

func TestParseDiffLinesEmpty(t *testing.T) {
	if parseDiffLines("") != nil {
		t.Error("expected nil for empty diff")
	}
}

func TestDiffLineStyle(t *testing.T) {
	addedFG, _, _ := diffLineStyle(diffAdded).Decompose()
	removedFG, _, _ := diffLineStyle(diffRemoved).Decompose()
	contextFG, _, _ := diffLineStyle(diffContext).Decompose()
	headerFG, _, _ := diffLineStyle(diffHeader).Decompose()

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

	ch, _, _, _ := screen.GetContent(0, 0)
	if ch != '╭' {
		t.Errorf("top-left = %c, want ╭", ch)
	}
	ch, _, _, _ = screen.GetContent(9, 0)
	if ch != '╮' {
		t.Errorf("top-right = %c, want ╮", ch)
	}
}

func TestDrawBorderTooSmall(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.Init()
	screen.SetSize(20, 10)
	// Should not panic
	drawBorder(screen, 0, 0, 1, 1, StyleBorder)
	drawBorder(screen, 0, 0, 0, 0, StyleBorder)
}
