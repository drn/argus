package tui2

import (
	"image/color"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	xvt "github.com/charmbracelet/x/vt"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/gitutil"
	"github.com/drn/argus/internal/testutil"
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

func TestUvCellToTcellStyle_Faint(t *testing.T) {
	cell := &uv.Cell{
		Content: "D",
		Width:   1,
		Style: uv.Style{
			Attrs: uv.AttrFaint,
		},
	}
	style := uvCellToTcellStyle(cell)
	_, _, attr := style.Decompose()
	if attr&tcell.AttrDim == 0 {
		t.Error("expected dim attribute for AttrFaint")
	}
}

func TestUvCellToTcellStyle_Blink(t *testing.T) {
	cell := &uv.Cell{
		Content: "B",
		Width:   1,
		Style: uv.Style{
			Attrs: uv.AttrBlink,
		},
	}
	style := uvCellToTcellStyle(cell)
	_, _, attr := style.Decompose()
	if attr&tcell.AttrBlink == 0 {
		t.Error("expected blink attribute for AttrBlink")
	}
}

func TestUvCellToTcellStyle_UnderlineStyles(t *testing.T) {
	tests := []struct {
		name string
		ul   ansi.Underline
		want tcell.UnderlineStyle
	}{
		{"single", ansi.UnderlineSingle, tcell.UnderlineStyleSolid},
		{"double", ansi.UnderlineDouble, tcell.UnderlineStyleDouble},
		{"curly", ansi.UnderlineCurly, tcell.UnderlineStyleCurly},
		{"dotted", ansi.UnderlineDotted, tcell.UnderlineStyleDotted},
		{"dashed", ansi.UnderlineDashed, tcell.UnderlineStyleDashed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cell := &uv.Cell{
				Content: "U",
				Width:   1,
				Style:   uv.Style{Underline: tt.ul},
			}
			style := uvCellToTcellStyle(cell)
			got := style.GetUnderlineStyle()
			testutil.Equal(t, got, tt.want)
		})
	}
}

func TestUvCellToTcellStyle_UnderlineColor(t *testing.T) {
	cell := &uv.Cell{
		Content: "U",
		Width:   1,
		Style: uv.Style{
			Underline:      ansi.UnderlineCurly,
			UnderlineColor: ansi.BasicColor(1),
		},
	}
	style := uvCellToTcellStyle(cell)
	testutil.Equal(t, style.GetUnderlineStyle(), tcell.UnderlineStyleCurly)
	testutil.Equal(t, style.GetUnderlineColor(), tcell.PaletteColor(1))
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

func TestNewTrackedEmulator_DefaultCursorHidden(t *testing.T) {
	tp := NewTerminalPane()
	cursorVisible := true // will be overwritten by callback
	_ = tp.newTrackedEmulatorWithCallback(20, 5, func(visible bool) {
		cursorVisible = visible
	})
	if cursorVisible {
		t.Fatal("new emulator should default cursor to hidden (agents hide cursor)")
	}
}

func TestPaintEmu_HiddenCursorNoContentExtension(t *testing.T) {
	// When cursor is hidden and at (0, lastRow), paintEmu should NOT extend
	// lastContentRow to include the cursor — otherwise a phantom cursor cell
	// appears at the bottom-left.
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	screen.SetSize(20, 10)

	tp := NewTerminalPane()
	emu := tp.newTrackedEmulatorWithCallback(20, 10, func(visible bool) {})
	// Write one line of content, then move cursor to bottom-left.
	emu.Write([]byte("hello\x1b[10;1H"))

	// Paint with cursorVisible=false — the cursor at (0,9) should NOT
	// cause content to extend to row 9.
	tp.paintEmu(screen, 0, 0, 20, 10, emu, 20, 10, true, false)

	// Row 9 col 0 should NOT have cursor styling.
	_, _, style, _ := screen.GetContent(0, 9)
	fg, bg, _ := style.Decompose()
	if fg == cursorFG || bg == cursorBG {
		t.Fatalf("hidden cursor at bottom-left should not be painted: fg=%v bg=%v", fg, bg)
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

// buildReplaySync calls asyncReplayRebuild synchronously (not in a goroutine)
// for testing. This exercises the production code path without needing a
// QueueUpdateDraw callback. After calling, the replay emulator is populated
// and tp fields are updated under tp.mu.
func buildReplaySync(tp *TerminalPane, raw []byte, cols, rows int) {
	tp.asyncReplayRebuild("", 0, rows, cols, rows, raw, nil, nil)
	// Consume the pending flag the same way Draw() does.
	tp.mu.Lock()
	if tp.replayRebuildPending {
		tp.replayRebuildPending = false
		tp.anchorTotalLines = 0
		tp.paintCacheValid = false
	}
	tp.mu.Unlock()
}

func TestTerminalPane_ReplayCaching(t *testing.T) {
	tp := NewTerminalPane()

	raw := []byte("hello world\nline two\nline three\n")

	// First build creates the emulator.
	buildReplaySync(tp, raw, 40, 10)
	if tp.replayEmu == nil {
		t.Fatal("replayEmu should be set after first build")
	}
	firstEmu := tp.replayEmu

	// Same data, same dimensions → asyncReplayRebuild always rebuilds
	// (caching is checked in Draw's fast path, not in the build itself).
	// But the emulator fields should be populated correctly.
	buildReplaySync(tp, raw, 40, 10)
	if tp.replayEmu == nil {
		t.Fatal("replayEmu should be set after second build")
	}

	// Different data → new emulator.
	raw2 := []byte("hello world\nline two\nline three\nline four\n")
	buildReplaySync(tp, raw2, 40, 10)
	if tp.replayEmu == firstEmu {
		t.Error("replayEmu should be rebuilt when data changes")
	}
	testutil.Equal(t, tp.replayEmuBytes, uint64(len(raw2)))
}

func TestTerminalPane_ReadLogTailForTask(t *testing.T) {
	// No taskID → should return nil.
	data, size := readLogTailForTask("", 1024)
	if data != nil || size != 0 {
		t.Error("readLogTailForTask with empty taskID should return nil")
	}

	// Non-existent task → should return nil.
	data, size = readLogTailForTask("nonexistent-task-id-12345", 1024)
	if data != nil || size != 0 {
		t.Error("readLogTailForTask with missing log should return nil")
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

// countingAdapter wraps mockAdapter and counts RecentOutput calls.
type countingAdapter struct {
	mockAdapter
	recentOutputCalls int
}

func (c *countingAdapter) RecentOutput() []byte {
	c.recentOutputCalls++
	return c.mockAdapter.RecentOutput()
}

func TestTerminalPane_RenderLiveSkipsCopyWhenIdle(t *testing.T) {
	tp := NewTerminalPane()
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.Init() //nolint:errcheck
	screen.SetSize(80, 24)

	output := []byte("hello world\r\n")
	sess := &countingAdapter{
		mockAdapter: mockAdapter{alive: true, totalWritten: uint64(len(output)), output: output},
	}
	tp.SetSession(sess)

	// First render — must fetch the buffer to populate the emulator.
	tp.renderLive(screen, 0, 0, 40, 10, 40, 10)
	testutil.Equal(t, sess.recentOutputCalls, 1)
	testutil.Equal(t, tp.emuFedTotal, uint64(len(output)))

	firstEmu := tp.emu

	// Second render with same TotalWritten — should NOT call RecentOutput.
	tp.renderLive(screen, 0, 0, 40, 10, 40, 10)
	testutil.Equal(t, sess.recentOutputCalls, 1) // still 1
	if tp.emu != firstEmu {
		t.Error("emulator should be reused when no new bytes")
	}

	// Simulate new output arriving.
	newOutput := []byte("hello world\r\nline two\r\n")
	sess.totalWritten = uint64(len(newOutput))
	sess.output = newOutput
	tp.renderLive(screen, 0, 0, 40, 10, 40, 10)
	testutil.Equal(t, sess.recentOutputCalls, 2) // now 2
	testutil.Equal(t, tp.emuFedTotal, uint64(len(newOutput)))
}

func TestTerminalPane_StatLogSize(t *testing.T) {
	tp := NewTerminalPane()

	// No taskID → should return 0.
	tp.taskID = ""
	size := tp.statLogSize()
	testutil.Equal(t, size, int64(0))

	// Non-existent task → should return 0.
	tp.taskID = "nonexistent-task-id-99999"
	size = tp.statLogSize()
	testutil.Equal(t, size, int64(0))
}

func TestTerminalPane_ScrollCacheFastPath(t *testing.T) {
	// When a cached replay emulator exists and the data source hasn't changed,
	// Draw's fast path should reuse the emulator without triggering a rebuild.
	tp := NewTerminalPane()

	// Generate enough content to create scrollback.
	var raw []byte
	for i := 0; i < 50; i++ {
		raw = append(raw, []byte("line of scrollable content here!\n")...)
	}

	// Build the replay emulator.
	buildReplaySync(tp, raw, 40, 10)
	if tp.replayEmu == nil {
		t.Fatal("replayEmu should be set after build")
	}
	testutil.Equal(t, tp.replayEmuBytes, uint64(len(raw)))

	// Scrolling should NOT invalidate the cache (replayEmuBytes still matches).
	firstEmu := tp.replayEmu
	tp.scrollOffset = 5
	// The Draw fast path checks: replayEmuCols/Rows match AND
	// (replayEmuBytes == len(raw) for non-log-backed). Verify fields.
	testutil.Equal(t, tp.replayEmuCols, 40)
	testutil.Equal(t, tp.replayEmuRows, 10)

	// The fast path would check tp.replayEmuBytes == sess.TotalWritten() or uint64(len(replayData)).
	// Since we built with raw, replayEmuBytes matches. Cache is valid.
	if tp.replayEmu != firstEmu {
		t.Error("replayEmu should be same pointer after scroll (no rebuild)")
	}
}

func TestTerminalPane_ScrollCacheDimensionChange(t *testing.T) {
	// Changing terminal dimensions must invalidate the replay cache.
	// Draw's fast path checks replayEmuCols/Rows — a mismatch triggers rebuild.
	tp := NewTerminalPane()

	raw := []byte("hello world\nline two\nline three\n")

	buildReplaySync(tp, raw, 40, 10)
	testutil.Equal(t, tp.replayEmuCols, 40)
	testutil.Equal(t, tp.replayEmuRows, 10)

	// Rebuild at different cols → fields update.
	buildReplaySync(tp, raw, 60, 10)
	testutil.Equal(t, tp.replayEmuCols, 60)

	// Rebuild at different rows → fields update.
	buildReplaySync(tp, raw, 60, 15)
	testutil.Equal(t, tp.replayEmuRows, 15)
}

// countingScreen wraps a simulation screen and counts SetContent calls.
type countingScreen struct {
	tcell.SimulationScreen
	setContentCalls int
}

func (cs *countingScreen) SetContent(x, y int, ch rune, comb []rune, style tcell.Style) {
	cs.setContentCalls++
	cs.SimulationScreen.SetContent(x, y, ch, comb, style)
}

func TestTerminalPane_PaintCacheReplay(t *testing.T) {
	tp := NewTerminalPane()
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.Init() //nolint:errcheck
	screen.SetSize(80, 24)

	output := []byte("hello world\r\nsecond line\r\n")
	sess := &countingAdapter{
		mockAdapter: mockAdapter{alive: true, totalWritten: uint64(len(output)), output: output},
	}
	tp.SetSession(sess)

	// First render — builds emulator and populates paint cache.
	tp.renderLive(screen, 0, 0, 40, 10, 40, 10)
	testutil.Equal(t, sess.recentOutputCalls, 1)
	if !tp.paintCacheValid {
		t.Fatal("paint cache should be valid after first render")
	}
	if len(tp.paintCacheCells) == 0 {
		t.Fatal("paint cache should have cells")
	}

	// Capture the screen content after first paint.
	ch1, _, style1, _ := screen.GetContent(0, 0)

	// Clear screen to verify cache replay restores content.
	screen.Clear()

	// Second render — same TotalWritten, same viewport → should replay cache.
	tp.renderLive(screen, 0, 0, 40, 10, 40, 10)
	testutil.Equal(t, sess.recentOutputCalls, 1) // still 1 — no emulator access

	// Verify screen content matches.
	ch2, _, style2, _ := screen.GetContent(0, 0)
	testutil.Equal(t, ch2, ch1)
	testutil.Equal(t, style2, style1)
}

func TestTerminalPane_PaintCacheInvalidatedOnScroll(t *testing.T) {
	tp := NewTerminalPane()
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.Init() //nolint:errcheck
	screen.SetSize(80, 24)

	output := []byte("hello\r\n")
	sess := &mockAdapter{alive: true, totalWritten: uint64(len(output)), output: output}
	tp.SetSession(sess)

	tp.renderLive(screen, 0, 0, 40, 10, 40, 10)
	if !tp.paintCacheValid {
		t.Fatal("cache should be valid")
	}

	tp.ScrollUp(1)
	if tp.paintCacheValid {
		t.Error("cache should be invalidated after ScrollUp")
	}
}

func TestTerminalPane_PaintCacheInvalidatedOnNewBytes(t *testing.T) {
	tp := NewTerminalPane()
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.Init() //nolint:errcheck
	screen.SetSize(80, 24)

	output := []byte("hello\r\n")
	sess := &mockAdapter{alive: true, totalWritten: uint64(len(output)), output: output}
	tp.SetSession(sess)

	tp.renderLive(screen, 0, 0, 40, 10, 40, 10)
	if !tp.paintCacheValid {
		t.Fatal("cache should be valid")
	}

	// Simulate new output — cache is still "valid" in the flag sense,
	// but renderLive takes the newBytes>0 path which rebuilds the cache.
	newOutput := []byte("hello\r\nworld\r\n")
	sess.totalWritten = uint64(len(newOutput))
	sess.output = newOutput
	tp.renderLive(screen, 0, 0, 40, 10, 40, 10)
	// Cache should still be valid (rebuilt with new content).
	if !tp.paintCacheValid {
		t.Error("cache should be valid after rebuild")
	}
}

func TestTerminalPane_PaintCacheInvalidatedOnReset(t *testing.T) {
	tp := NewTerminalPane()
	tp.paintCacheValid = true
	tp.paintCacheCells = []cachedCell{{x: 0, y: 0, ch: 'A', style: tcell.StyleDefault}}

	tp.ResetVT()
	if tp.paintCacheValid {
		t.Error("cache should be invalidated after ResetVT")
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

func TestTerminalPane_AccelScroll(t *testing.T) {
	t.Run("first press scrolls 1 line", func(t *testing.T) {
		tp := NewTerminalPane()
		n := tp.AccelScrollUp()
		testutil.Equal(t, n, 1)
		testutil.Equal(t, tp.ScrollOffset(), 1)
	})

	t.Run("rapid presses accelerate", func(t *testing.T) {
		tp := NewTerminalPane()
		// Simulate rapid key repeats (no delay).
		n1 := tp.AccelScrollUp()
		testutil.Equal(t, n1, 1)

		n2 := tp.AccelScrollUp()
		testutil.Equal(t, n2, 2)

		n3 := tp.AccelScrollUp()
		testutil.Equal(t, n3, 3)

		// Total offset = 1+2+3 = 6
		testutil.Equal(t, tp.ScrollOffset(), 6)
	})

	t.Run("pause resets acceleration", func(t *testing.T) {
		tp := NewTerminalPane()
		tp.AccelScrollUp()
		tp.AccelScrollUp()
		tp.AccelScrollUp()
		// Simulate a pause longer than the accel window.
		tp.lastScrollTime = time.Now().Add(-200 * time.Millisecond)
		n := tp.AccelScrollUp()
		testutil.Equal(t, n, 1) // reset to 1
	})

	t.Run("caps at max", func(t *testing.T) {
		tp := NewTerminalPane()
		for i := 0; i < 20; i++ {
			tp.AccelScrollUp()
		}
		n := tp.AccelScrollUp()
		testutil.Equal(t, n, scrollAccelMax)
	})

	t.Run("accel scroll down", func(t *testing.T) {
		tp := NewTerminalPane()
		tp.ScrollUp(100) // start scrolled up
		n := tp.AccelScrollDown()
		testutil.Equal(t, n, 1)
		testutil.Equal(t, tp.ScrollOffset(), 99)
	})

	t.Run("accel scroll down clamps at zero", func(t *testing.T) {
		tp := NewTerminalPane()
		tp.ScrollUp(3)
		// Rapid accelerated scrolls down should clamp.
		tp.AccelScrollDown()
		tp.AccelScrollDown()
		tp.AccelScrollDown()
		tp.AccelScrollDown()
		testutil.Equal(t, tp.ScrollOffset(), 0)
	})

	t.Run("reset clears acceleration", func(t *testing.T) {
		tp := NewTerminalPane()
		tp.AccelScrollUp()
		tp.AccelScrollUp()
		tp.AccelScrollUp()
		tp.ResetScroll()
		testutil.Equal(t, tp.scrollAccel, 0)
		// Next scroll should start fresh.
		n := tp.AccelScrollUp()
		testutil.Equal(t, n, 1)
	})
}

func TestTerminalPane_ReplayAnchorReset(t *testing.T) {
	// Verify that asyncReplayRebuild sets replayRebuildPending, which
	// causes Draw to reset anchorTotalLines on rebuild. This prevents
	// false anchor-lock when transitioning from live to replay.
	tp := NewTerminalPane()

	// Simulate live mode having set anchorTotalLines.
	tp.anchorTotalLines = 100

	// First scroll up transitions to replay path.
	tp.ScrollUp(1)

	// Build replay data: enough to fill some scrollback.
	var data []byte
	for i := 0; i < 200; i++ {
		data = append(data, []byte("line content here\r\n")...)
	}

	// buildReplaySync consumes the pending flag and resets anchorTotalLines.
	buildReplaySync(tp, data, 80, 24)
	testutil.Equal(t, tp.anchorTotalLines, 0)
	testutil.Equal(t, tp.scrollOffset, 1)
}

func TestTerminalPane_ScrollUpAfterReturnToLive(t *testing.T) {
	// Regression: scroll up → scroll back to bottom (live mode sets
	// anchorTotalLines) → scroll up again. Without the fix, two bugs:
	// 1) Stale replay emu: cached emu content is behind live, so
	//    scrollOffset=1 shows content from hundreds of lines ago.
	// 2) Anchor-lock mismatch: anchorTotalLines from live mode causes
	//    paintEmu to bump scrollOffset by (replayTotal - liveTotal).
	tp := NewTerminalPane()

	// Build replay data with enough content to produce scrollback.
	var data []byte
	for i := 0; i < 200; i++ {
		data = append(data, []byte("line content here\r\n")...)
	}

	// Step 1: First scroll up — triggers rebuild, anchorTotalLines reset.
	tp.ScrollUp(1)
	buildReplaySync(tp, data, 80, 24)
	testutil.Equal(t, tp.scrollOffset, 1)
	if tp.replayEmu == nil {
		t.Fatal("replayEmu should be non-nil after build")
	}

	// Step 2: Scroll back to bottom → simulate live mode setting anchorTotalLines.
	tp.ResetScroll()
	tp.anchorTotalLines = 50
	if tp.replayEmu == nil {
		t.Fatal("replayEmu should still be cached after ResetScroll")
	}

	// Step 3: Scroll up again — must invalidate stale replay emu AND anchor.
	tp.ScrollUp(1)
	testutil.Equal(t, tp.anchorTotalLines, 0)
	testutil.Nil(t, tp.replayEmu) // stale emu cleared, forces rebuild

	// Rebuild from fresh data — scrollOffset stays at 1.
	buildReplaySync(tp, data, 80, 24)
	testutil.Equal(t, tp.scrollOffset, 1)
}

func TestTerminalPane_AccelScrollUpResetsReplayState(t *testing.T) {
	// AccelScrollUp must also invalidate replay state on 0→>0 transition.
	tp := NewTerminalPane()
	tp.anchorTotalLines = 500
	tp.replayEmu = newDrainedEmulator(80, 24) // simulate cached emu

	n := tp.AccelScrollUp()
	testutil.Equal(t, tp.anchorTotalLines, 0)
	testutil.Nil(t, tp.replayEmu)
	testutil.Equal(t, tp.scrollOffset, n)
}

func TestTerminalPane_MouseScrollUpResetsReplayState(t *testing.T) {
	// Mouse wheel ScrollUp must also invalidate replay state on 0→>0 transition.
	tp := NewTerminalPane()
	tp.anchorTotalLines = 500
	tp.replayEmu = newDrainedEmulator(80, 24) // simulate cached emu

	tp.ScrollUp(3) // mouseScrollStep
	testutil.Equal(t, tp.anchorTotalLines, 0)
	testutil.Nil(t, tp.replayEmu)
	testutil.Equal(t, tp.scrollOffset, 3)
}

func TestTerminalPane_ReplayEmuMaxScrollUsesActualCapacity(t *testing.T) {
	// replayEmuMaxScroll should reflect the emulator's actual scrollback
	// capacity, not just the current scroll offset at build time. This prevents
	// unnecessary rebuilds when scrolling further up.
	tp := NewTerminalPane()

	// Generate enough output to produce scrollback (100 lines in a 10-row emu).
	var data []byte
	for i := 0; i < 100; i++ {
		data = append(data, []byte("scrollback line content!\n")...)
	}

	tp.scrollOffset = 2
	buildReplaySync(tp, data, 20, 10)

	// maxScroll should be much larger than the scroll offset (2) because
	// the emulator has many lines of scrollback.
	if tp.replayEmuMaxScroll <= 2 {
		t.Errorf("replayEmuMaxScroll=%d should be >> scroll offset 2 (reflects actual scrollback capacity)", tp.replayEmuMaxScroll)
	}
}

func TestTerminalPane_ReplayEmulatorHasLargeScrollback(t *testing.T) {
	if testing.Short() {
		t.Skip("feeds 12K lines to emulator")
	}
	// Replay emulators must have a scrollback buffer larger than the default
	// 10K lines. Feed 12K lines — if SetScrollbackSize(50K) were removed,
	// the default 10K buffer would cap scrollback below 10K.
	tp := NewTerminalPane()
	emu := tp.newTrackedReplayEmulatorWithCallback(80, 24, nil)

	// Feed 12K lines (exceeds default 10K scrollback).
	for i := 0; i < 12_000; i++ {
		emu.Write([]byte("line of content for scrollback testing\n"))
	}

	sbLen := emu.ScrollbackLen()
	// With 50K buffer and 24-row viewport: 12000 - 24 = 11976 scrollback lines.
	// With default 10K buffer: scrollback would be capped at 10000.
	if sbLen <= 10_000 {
		t.Errorf("replay emulator scrollback=%d, want >10000 (50K buffer should hold 12K lines)", sbLen)
	}
}

func TestTerminalPane_ScrollUpWhileAlreadyScrolled(t *testing.T) {
	// Scrolling further up while already scrolled should NOT invalidate
	// the replay emu — it's still current for the scrolled region.
	tp := NewTerminalPane()
	tp.scrollOffset = 5
	emu := newDrainedEmulator(80, 24)
	tp.replayEmu = emu
	tp.anchorTotalLines = 840

	tp.ScrollUp(1)
	// Should NOT invalidate — we're already in scroll mode.
	testutil.Equal(t, tp.anchorTotalLines, 840)
	if tp.replayEmu != emu {
		t.Error("replayEmu should NOT be invalidated while already scrolled")
	}
	testutil.Equal(t, tp.scrollOffset, 6)
}
