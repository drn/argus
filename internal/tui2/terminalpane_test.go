package tui2

import (
	"image/color"
	"testing"

	"github.com/charmbracelet/x/ansi"
	xvt "github.com/charmbracelet/x/vt"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/gdamore/tcell/v2"
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
	tp.emu = xvt.NewSafeEmulator(80, 24)
	tp.emuFedTotal = 100
	tp.scrollOffset = 5

	tp.ResetVT()

	if tp.emu != nil {
		t.Error("emu should be nil after reset")
	}
	if tp.emuFedTotal != 0 {
		t.Errorf("emuFedTotal = %d, want 0", tp.emuFedTotal)
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

func TestUvColorToTcell(t *testing.T) {
	tests := []struct {
		name  string
		color color.Color
		want  tcell.Color
	}{
		{"nil_default", nil, tcell.ColorDefault},
		{"basic_0", ansi.BasicColor(0), tcell.PaletteColor(0)},
		{"basic_1", ansi.BasicColor(1), tcell.PaletteColor(1)},
		{"indexed_87", ansi.IndexedColor(87), tcell.PaletteColor(87)},
		{"indexed_255", ansi.IndexedColor(255), tcell.PaletteColor(255)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := uvColorToTcell(tt.color)
			if got != tt.want {
				t.Errorf("uvColorToTcell(%v) = %v, want %v", tt.color, got, tt.want)
			}
		})
	}
}

func TestUvColorToTcell_RGB(t *testing.T) {
	// RGB color should convert to a valid tcell color (not default).
	c := color.RGBA{R: 255, G: 128, B: 0, A: 255}
	got := uvColorToTcell(c)
	if got == tcell.ColorDefault {
		t.Error("RGB color should not map to ColorDefault")
	}
}

func TestUvCellToTcellStyle(t *testing.T) {
	// Bold + red foreground.
	cell := &uv.Cell{
		Content: "A",
		Width:   1,
		Style: uv.Style{
			Fg:    ansi.BasicColor(1),
			Bg:    nil,
			Attrs: uv.AttrBold,
		},
	}
	style := uvCellToTcellStyle(cell)
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

func TestUvCellToTcellStyle_Nil(t *testing.T) {
	style := uvCellToTcellStyle(nil)
	fg, bg, _ := style.Decompose()
	if fg != tcell.ColorDefault || bg != tcell.ColorDefault {
		t.Error("nil cell should produce default style")
	}
}

func TestUvCellToTcellStyle_NoActiveInputBG(t *testing.T) {
	// Default-colored cell should NOT get activeInputBG tinting.
	cell := &uv.Cell{
		Content: " ",
		Width:   1,
		Style:   uv.Style{},
	}
	style := uvCellToTcellStyle(cell)
	_, bg, _ := style.Decompose()
	if bg != tcell.ColorDefault {
		t.Errorf("default cell bg = %v, want ColorDefault (no activeInputBG)", bg)
	}
}

func TestRowHasContentEmu(t *testing.T) {
	emu := xvt.NewSafeEmulator(20, 5)
	emu.Write([]byte("hello\n"))

	if !rowHasContentEmu(emu, 0, 20) {
		t.Error("row 0 should have content")
	}
	if rowHasContentEmu(emu, 3, 20) {
		t.Error("row 3 should be empty")
	}
}

func TestFindContentRowsEmu(t *testing.T) {
	emu := xvt.NewSafeEmulator(20, 10)
	emu.Write([]byte("\n\nhello\nworld\n"))

	last := findLastContentRowEmu(emu, 20, 10)
	if last < 2 {
		t.Errorf("findLastContentRowEmu = %d, want >= 2", last)
	}
	first := findFirstContentRowEmu(emu, 20, last)
	if first > 3 {
		t.Errorf("findFirstContentRowEmu = %d, want <= 3", first)
	}
}

func TestScrollbackLen(t *testing.T) {
	// Write enough lines to push content into scrollback.
	emu := xvt.NewSafeEmulator(20, 5)
	for i := 0; i < 20; i++ {
		emu.Write([]byte("line content here!\n"))
	}
	sbLen := emu.ScrollbackLen()
	if sbLen == 0 {
		t.Error("expected scrollback lines after overflow, got 0")
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
