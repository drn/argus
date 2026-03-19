package tui2

import (
	"image/color"
	"testing"

	"github.com/charmbracelet/x/ansi"
	xvt "github.com/charmbracelet/x/vt"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/gitutil"
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

func TestTerminalPane_SetSessionNoFallback(t *testing.T) {
	// SetSession must NOT hardcode 80x24 — it should use GetInnerRect
	// dimensions (or leave at 0 if unavailable). The old code had an
	// explicit fallback to 80x24 which caused emulator/PTY mismatch.
	tp := NewTerminalPane()
	sess := &mockAdapter{alive: true, totalWritten: 100, output: make([]byte, 100)}
	tp.SetSession(sess)
	tp.mu.Lock()
	cols, rows := tp.ptyCols, tp.ptyRows
	tp.mu.Unlock()
	// Must not be the old hardcoded 80x24 fallback.
	if cols == 80 && rows == 24 {
		t.Errorf("SetSession fell back to hardcoded 80x24; should use panel dimensions")
	}
}

type mockAdapter struct {
	alive        bool
	totalWritten uint64
	output       []byte
}

func (m *mockAdapter) WriteInput(p []byte) (int, error) { return len(p), nil }
func (m *mockAdapter) Resize(rows, cols uint16) error    { return nil }
func (m *mockAdapter) RecentOutput() []byte              { return m.output }
func (m *mockAdapter) RecentOutputTail(n int) []byte {
	if n >= len(m.output) {
		return m.output
	}
	return m.output[len(m.output)-n:]
}
func (m *mockAdapter) TotalWritten() uint64          { return m.totalWritten }
func (m *mockAdapter) Alive() bool                   { return m.alive }
func (m *mockAdapter) PTYSize() (int, int)           { return 80, 24 }

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

func TestTerminalPane_MouseScroll(t *testing.T) {
	tp := NewTerminalPane()
	tp.SetRect(0, 0, 80, 24)
	handler := tp.MouseHandler()
	setFocus := func(p tview.Primitive) {}
	// Mouse event inside the box.
	ev := tcell.NewEventMouse(5, 5, tcell.ButtonNone, tcell.ModNone)

	// Scroll up via mouse wheel.
	consumed, _ := handler(tview.MouseScrollUp, ev, setFocus)
	if !consumed {
		t.Error("MouseScrollUp should be consumed")
	}
	if tp.ScrollOffset() != 3 {
		t.Errorf("after scroll up: offset = %d, want 3", tp.ScrollOffset())
	}

	// Scroll down via mouse wheel.
	consumed, _ = handler(tview.MouseScrollDown, ev, setFocus)
	if !consumed {
		t.Error("MouseScrollDown should be consumed")
	}
	if tp.ScrollOffset() != 0 {
		t.Errorf("after scroll down: offset = %d, want 0", tp.ScrollOffset())
	}

	// Diff mode scrolling.
	tp.EnterDiffMode("+line1\n+line2\n context", "test.go")
	tp.diffScroll = 0
	consumed, _ = handler(tview.MouseScrollDown, ev, setFocus)
	if !consumed {
		t.Error("MouseScrollDown in diff mode should be consumed")
	}
	if tp.diffScroll != 3 {
		t.Errorf("diff scroll after down = %d, want 3", tp.diffScroll)
	}
	consumed, _ = handler(tview.MouseScrollUp, ev, setFocus)
	if !consumed {
		t.Error("MouseScrollUp in diff mode should be consumed")
	}
	if tp.diffScroll != 0 {
		t.Errorf("diff scroll after up = %d, want 0", tp.diffScroll)
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
	diff := "--- a/test.go\n+++ b/test.go\n@@ -1,3 +1,3 @@\n context\n-removed\n+added\n"
	tp.EnterDiffMode(diff, "test.go")
	if !tp.InDiffMode() {
		t.Error("should be in diff mode")
	}
	if len(tp.diffUnifiedLines) == 0 {
		t.Error("unified diff lines should be populated")
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

func TestPaintEmu_HiddenCursorNotRendered(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	screen.SetSize(20, 5)

	tp := NewTerminalPane()
	cursorVisible := true
	emu := tp.newTrackedEmulatorWithCallback(20, 5, func(visible bool) {
		cursorVisible = visible
	})
	emu.Write([]byte("hello\x1b[?25l"))

	tp.paintEmu(screen, 0, 0, 20, 5, emu, 20, 5, true, cursorVisible)

	_, _, style, _ := screen.GetContent(5, 0)
	fg, bg, _ := style.Decompose()
	if fg == cursorFG || bg == cursorBG {
		t.Fatalf("hidden cursor should not be painted with cursor style: fg=%v bg=%v", fg, bg)
	}
}

func TestBuildUnifiedDiffLines(t *testing.T) {
	diff := "--- a/test.go\n+++ b/test.go\n@@ -1,3 +1,3 @@\n context\n-removed\n+added\n"
	pd := gitutil.ParseUnifiedDiff(diff)
	lines := buildUnifiedDiffLines(pd, "test.go")
	if len(lines) == 0 {
		t.Fatal("expected non-empty unified diff lines")
	}
	// Should have: hunk header + 3 content lines + trailing empty context = 5 lines
	if len(lines) < 4 {
		t.Errorf("expected at least 4 lines, got %d", len(lines))
	}
	// Each line should have styled cells
	for i, line := range lines {
		if len(line.cells) == 0 {
			t.Errorf("line %d has no cells", i)
		}
	}
}

func TestBuildUnifiedDiffLinesEmpty(t *testing.T) {
	pd := gitutil.ParseUnifiedDiff("")
	lines := buildUnifiedDiffLines(pd, "test.go")
	if lines != nil {
		t.Error("expected nil for empty diff")
	}
}

func TestBuildSideBySideDiffLines(t *testing.T) {
	diff := "--- a/test.go\n+++ b/test.go\n@@ -1,3 +1,3 @@\n context\n-removed\n+added\n"
	pd := gitutil.ParseUnifiedDiff(diff)
	lines := buildSideBySideDiffLines(pd, "test.go", 80)
	if len(lines) == 0 {
		t.Fatal("expected non-empty side-by-side diff lines")
	}
	for i, line := range lines {
		if len(line.cells) == 0 {
			t.Errorf("line %d has no cells", i)
		}
	}
}

func TestHighlightLines(t *testing.T) {
	lines := []string{"func main() {", "  fmt.Println(\"hello\")", "}"}
	hl := highlightLines(lines, "test.go")
	if len(hl) != 3 {
		t.Fatalf("expected 3 highlighted lines, got %d", len(hl))
	}
	// Go code should get syntax highlighting — at least some cells should
	// have non-default foreground.
	hasColor := false
	for _, line := range hl {
		for _, c := range line.cells {
			fg, _, _ := c.style.Decompose()
			if fg != tcell.ColorDefault {
				hasColor = true
				break
			}
		}
	}
	if !hasColor {
		t.Error("expected syntax-highlighted cells with non-default colors")
	}
}

func TestHighlightLinesUnknownExtension(t *testing.T) {
	lines := []string{"hello world"}
	hl := highlightLines(lines, "unknown.xyz123")
	if len(hl) != 1 {
		t.Fatalf("expected 1 line, got %d", len(hl))
	}
	// Should return plain (unstyled) text
	if len(hl[0].cells) != len("hello world") {
		t.Errorf("expected %d cells, got %d", len("hello world"), len(hl[0].cells))
	}
}

func TestTerminalPane_AnchorLock(t *testing.T) {
	tp := NewTerminalPane()

	// Simulate being scrolled up with a known total line count.
	tp.scrollOffset = 10
	tp.anchorTotalLines = 50

	// paintEmu anchor-lock: when totalLines grows, scrollOffset should increase.
	// We test this indirectly via the renderReplay path.
	// Create an emulator with enough content to produce scrollback.
	emu := newDrainedEmulator(20, 5)
	for i := 0; i < 30; i++ {
		emu.Write([]byte("line of content!!!!\n"))
	}

	screen := tcell.NewSimulationScreen("UTF-8")
	screen.Init()
	screen.SetSize(80, 24)

	// First paint establishes anchorTotalLines.
	tp.scrollOffset = 5
	tp.anchorTotalLines = 0
	tp.paintEmu(screen, 0, 0, 20, 5, emu, 20, 5, false, false)
	firstAnchor := tp.anchorTotalLines
	if firstAnchor == 0 {
		t.Fatal("anchorTotalLines should be set after first paint")
	}

	// Write more content to increase totalLines.
	for i := 0; i < 10; i++ {
		emu.Write([]byte("new output line!!!!\n"))
	}
	oldOffset := tp.scrollOffset
	tp.paintEmu(screen, 0, 0, 20, 5, emu, 20, 5, false, false)

	// scrollOffset should have increased by the delta.
	if tp.scrollOffset <= oldOffset {
		t.Errorf("anchor-lock failed: scrollOffset=%d should be > %d", tp.scrollOffset, oldOffset)
	}
}

func TestTerminalPane_AnchorLockResetsOnScrollToBottom(t *testing.T) {
	tp := NewTerminalPane()
	tp.scrollOffset = 5
	tp.anchorTotalLines = 50

	// Scrolling to bottom should reset anchor.
	tp.ScrollDown(10) // goes past 0, clamped to 0
	if tp.scrollOffset != 0 {
		t.Errorf("scrollOffset = %d, want 0", tp.scrollOffset)
	}
	if tp.anchorTotalLines != 0 {
		t.Errorf("anchorTotalLines = %d, want 0 after scroll to bottom", tp.anchorTotalLines)
	}

	// ResetScroll should also clear anchor.
	tp.scrollOffset = 5
	tp.anchorTotalLines = 50
	tp.ResetScroll()
	if tp.anchorTotalLines != 0 {
		t.Errorf("anchorTotalLines = %d, want 0 after ResetScroll", tp.anchorTotalLines)
	}
}

func TestTerminalPane_ReplayCaching(t *testing.T) {
	tp := NewTerminalPane()

	raw := []byte("hello world\nline two\nline three\n")

	screen := tcell.NewSimulationScreen("UTF-8")
	screen.Init()
	screen.SetSize(80, 24)

	// First render builds the emulator.
	tp.renderReplay(screen, 0, 0, 40, 10, raw, 0, 40, 10)
	if tp.replayEmu == nil {
		t.Fatal("replayEmu should be set after first render")
	}
	firstEmu := tp.replayEmu

	// Same data, same dimensions → should reuse the emulator.
	tp.renderReplay(screen, 0, 0, 40, 10, raw, 0, 40, 10)
	if tp.replayEmu != firstEmu {
		t.Error("replayEmu should be reused when data hasn't changed")
	}

	// Different data → should rebuild.
	raw2 := []byte("hello world\nline two\nline three\nline four\n")
	tp.renderReplay(screen, 0, 0, 40, 10, raw2, 0, 40, 10)
	if tp.replayEmu == firstEmu {
		t.Error("replayEmu should be rebuilt when data changes")
	}

	// Log-backed: different logSize → should rebuild.
	secondEmu := tp.replayEmu
	tp.renderReplay(screen, 0, 0, 40, 10, raw2, 1000, 40, 10)
	if tp.replayEmu == secondEmu {
		t.Error("replayEmu should be rebuilt when logSize changes")
	}

	// Same logSize → should reuse.
	thirdEmu := tp.replayEmu
	tp.renderReplay(screen, 0, 0, 40, 10, raw2, 1000, 40, 10)
	if tp.replayEmu != thirdEmu {
		t.Error("replayEmu should be reused when logSize unchanged")
	}
}

func TestTerminalPane_ReadLogTail(t *testing.T) {
	tp := NewTerminalPane()

	// No taskID → should return nil.
	tp.taskID = ""
	data, size := tp.readLogTail(1024)
	if data != nil || size != 0 {
		t.Error("readLogTail with no taskID should return nil")
	}

	// Non-existent task → should return nil.
	tp.taskID = "nonexistent-task-id-12345"
	data, size = tp.readLogTail(1024)
	if data != nil || size != 0 {
		t.Error("readLogTail with missing log should return nil")
	}
}

func TestTerminalPane_ResetVTClearsReplayCache(t *testing.T) {
	tp := NewTerminalPane()
	tp.replayEmu = newDrainedEmulator(80, 24)
	tp.replayEmuBytes = 100
	tp.replayEmuLogSize = 500
	tp.anchorTotalLines = 50

	tp.ResetVT()

	if tp.replayEmu != nil {
		t.Error("replayEmu should be nil after ResetVT")
	}
	if tp.replayEmuBytes != 0 {
		t.Errorf("replayEmuBytes = %d, want 0", tp.replayEmuBytes)
	}
	if tp.replayEmuLogSize != 0 {
		t.Errorf("replayEmuLogSize = %d, want 0", tp.replayEmuLogSize)
	}
	if tp.anchorTotalLines != 0 {
		t.Errorf("anchorTotalLines = %d, want 0", tp.anchorTotalLines)
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
