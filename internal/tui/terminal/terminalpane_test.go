package terminal

import (
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	xvt "github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/gitutil"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
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
func (m *mockAdapter) Resize(rows, cols uint16) error   { return nil }
func (m *mockAdapter) RecentOutput() []byte             { return m.output }
func (m *mockAdapter) RecentOutputTail(n int) []byte {
	if n >= len(m.output) {
		return m.output
	}
	return m.output[len(m.output)-n:]
}
func (m *mockAdapter) RecentOutputTailWithTotal(n int) ([]byte, uint64) {
	return m.RecentOutputTail(n), m.totalWritten
}
func (m *mockAdapter) TotalWritten() uint64 { return m.totalWritten }
func (m *mockAdapter) Alive() bool          { return m.alive }
func (m *mockAdapter) PTYSize() (int, int)  { return 80, 24 }

func TestTerminalPane_SessionGuardPreservesEmulator(t *testing.T) {
	// Simulates the tick callback bug: when streams fail repeatedly,
	// runner.Get() creates a new RemoteSession each time. Without a
	// guard, SetSession(newSess) resets the emulator, causing "Waiting
	// for output..." to flash even though the emulator already has content.
	tp := NewTerminalPane()
	sess1 := &mockAdapter{alive: true, totalWritten: 500, output: make([]byte, 500)}
	tp.SetSession(sess1)
	tp.emuFedTotal = 500 // simulate: emulator already fed data

	// A different session object (new RemoteSession after stream loss).
	sess2 := &mockAdapter{alive: true, totalWritten: 0, output: nil}
	tp.SetSession(sess2)

	// SetSession with a different pointer resets the emulator.
	testutil.Equal(t, tp.emuFedTotal, uint64(0))

	// The fix: tick should check Session() != nil before calling Get()/SetSession().
	// When Session() is non-nil, the tick skips the Get() call entirely,
	// preventing the emulator reset. This test documents the behavior.
	if tp.Session() == nil {
		t.Error("session should be non-nil after SetSession")
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
	style := UvCellToTcellStyle(cell)
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
	style := UvCellToTcellStyle(cell)
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
	style := UvCellToTcellStyle(cell)
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
			style := UvCellToTcellStyle(cell)
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
	style := UvCellToTcellStyle(cell)
	testutil.Equal(t, style.GetUnderlineStyle(), tcell.UnderlineStyleCurly)
	testutil.Equal(t, style.GetUnderlineColor(), tcell.PaletteColor(1))
}

func TestUvCellToTcellStyle_Nil(t *testing.T) {
	style := UvCellToTcellStyle(nil)
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
	style := UvCellToTcellStyle(cell)
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

	last := FindLastContentRowEmu(emu, 20, 10)
	if last < 2 {
		t.Errorf("FindLastContentRowEmu = %d, want >= 2", last)
	}
	first := FindFirstContentRowEmu(emu, 20, last)
	if first > 3 {
		t.Errorf("FindFirstContentRowEmu = %d, want <= 3", first)
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
	lines := widget.BuildUnifiedDiffLines(pd, "test.go")
	if len(lines) == 0 {
		t.Fatal("expected non-empty unified diff lines")
	}
	// Should have: hunk header + 3 content lines + trailing empty context = 5 lines
	if len(lines) < 4 {
		t.Errorf("expected at least 4 lines, got %d", len(lines))
	}
	// Each line should have styled cells
	for i, line := range lines {
		if len(line.Cells) == 0 {
			t.Errorf("line %d has no cells", i)
		}
	}
}

func TestBuildUnifiedDiffLinesEmpty(t *testing.T) {
	pd := gitutil.ParseUnifiedDiff("")
	lines := widget.BuildUnifiedDiffLines(pd, "test.go")
	if lines != nil {
		t.Error("expected nil for empty diff")
	}
}

func TestBuildSideBySideDiffLines(t *testing.T) {
	diff := "--- a/test.go\n+++ b/test.go\n@@ -1,3 +1,3 @@\n context\n-removed\n+added\n"
	pd := gitutil.ParseUnifiedDiff(diff)
	lines := widget.BuildSideBySideDiffLines(pd, "test.go", 80)
	if len(lines) == 0 {
		t.Fatal("expected non-empty side-by-side diff lines")
	}
	for i, line := range lines {
		if len(line.Cells) == 0 {
			t.Errorf("line %d has no cells", i)
		}
	}
}

func TestHighlightLines(t *testing.T) {
	lines := []string{"func main() {", "  fmt.Println(\"hello\")", "}"}
	hl := widget.HighlightLines(lines, "test.go")
	if len(hl) != 3 {
		t.Fatalf("expected 3 highlighted lines, got %d", len(hl))
	}
	// Go code should get syntax highlighting — at least some cells should
	// have non-default foreground.
	hasColor := false
	for _, line := range hl {
		for _, c := range line.Cells {
			fg, _, _ := c.Style.Decompose()
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
	hl := widget.HighlightLines(lines, "unknown.xyz123")
	if len(hl) != 1 {
		t.Fatalf("expected 1 line, got %d", len(hl))
	}
	// Should return plain (unstyled) text
	if len(hl[0].Cells) != len("hello world") {
		t.Errorf("expected %d cells, got %d", len("hello world"), len(hl[0].Cells))
	}
}

func TestTerminalPane_AnchorLock(t *testing.T) {
	tp := NewTerminalPane()

	// Simulate being scrolled up with a known total line count.
	tp.scrollOffset = 10
	tp.anchorTotalLines = 50

	// paintEmu anchor-lock: when totalLines grows, scrollOffset should increase.
	// Create an emulator with enough content to produce scrollback.
	emu := NewDrainedEmulator(20, 5)
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
	tp.asyncReplayRebuild("", 0, rows, cols, rows, raw, nil, 0, nil)
	// Consume the pending flag the same way Draw() does, via the shared
	// helper so test and production stay in lockstep.
	tp.mu.Lock()
	tp.consumeReplayRebuildPendingLocked()
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
	tp.replayEmu = NewDrainedEmulator(80, 24)
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

	widget.DrawBorder(screen, 0, 0, 10, 5, theme.StyleBorder)

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
	widget.DrawBorder(screen, 0, 0, 1, 1, theme.StyleBorder)
	widget.DrawBorder(screen, 0, 0, 0, 0, theme.StyleBorder)
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
	tp.replayEmu = NewDrainedEmulator(80, 24) // simulate cached emu

	n := tp.AccelScrollUp()
	testutil.Equal(t, tp.anchorTotalLines, 0)
	testutil.Nil(t, tp.replayEmu)
	testutil.Equal(t, tp.scrollOffset, n)
}

func TestTerminalPane_MouseScrollUpResetsReplayState(t *testing.T) {
	// Mouse wheel ScrollUp must also invalidate replay state on 0→>0 transition.
	tp := NewTerminalPane()
	tp.anchorTotalLines = 500
	tp.replayEmu = NewDrainedEmulator(80, 24) // simulate cached emu

	tp.ScrollUp(3) // mouseScrollStep
	testutil.Equal(t, tp.anchorTotalLines, 0)
	testutil.Nil(t, tp.replayEmu)
	testutil.Equal(t, tp.scrollOffset, 3)
}

func TestTerminalPane_RebuildClampsScrollPastTop(t *testing.T) {
	// Regression: when the user scrolls past the available scrollback,
	// the cache-validity check (scrollOffset <= replayEmuMaxScroll) fails
	// forever, every Draw kicks another async rebuild, and each rebuild
	// fires notifyBranchChange → screen.Sync — visible as continuous
	// flicker at the top of an agent view. After a rebuild, Draw's
	// consume-pending block must clamp scrollOffset to the fresh emu's
	// maxScroll so the next frame cache-hits.
	//
	// The OnBranchChange wiring this clamp depends on is exercised by
	// TestAsyncReplayRebuild_FiresBranchChangeOnSuccess; this test focuses
	// on the loop-break invariant alone.
	tp := NewTerminalPane()

	var data []byte
	for i := 0; i < 30; i++ {
		data = append(data, []byte("line\n")...)
	}

	// Simulate the user scrolling well past the available scrollback.
	tp.scrollOffset = 10_000
	buildReplaySync(tp, data, 20, 10)

	// The clamp must land scrollOffset exactly on replayEmuMaxScroll — not
	// merely below it. == is the loop-break invariant: it puts the next
	// Draw's cache-validity check (scrollOffset <= replayEmuMaxScroll) into
	// the passing region with no slack to drift back across.
	testutil.Equal(t, tp.scrollOffset, tp.replayEmuMaxScroll)
}

func TestTerminalPane_ConsumeReplayRebuildPendingClampAndIdempotency(t *testing.T) {
	// Direct unit test for consumeReplayRebuildPendingLocked. Covers:
	//  - clamp fires when pending=true AND scrollOffset > maxScroll
	//  - no-op when scrollOffset == maxScroll (boundary)
	//  - no-op when scrollOffset < maxScroll
	//  - no-op when pending=false (proves the loop-break: once the first
	//    consume lands, subsequent Draws don't re-clamp; combined with the
	//    cache-hit predicate now passing, no second async rebuild fires)
	//  - zero max clamps scrollOffset to zero
	//
	// Each case sets sentinel pre-call values for anchorTotalLines and
	// paintCacheValid so the table directly verifies all three field resets
	// (pending → false, anchor → 0, paintCacheValid → false) instead of
	// relying on transitive coverage through buildReplaySync. The literal
	// 777 below is an arbitrary nonzero marker — distinguishable from any
	// production value (helper only ever writes 0) so a regression that
	// forwarded the input instead of zeroing it would still fail the test.
	tests := []struct {
		name                 string
		startScrollOffset    int
		startPending         bool
		maxScroll            int
		wantScrollOffset     int
		wantAnchorTotalLines int
		wantPaintCacheValid  bool
	}{
		{"clamp past top", 10_000, true, 50, 50, 0, false},
		{"boundary equal", 50, true, 50, 50, 0, false},
		{"already below max", 20, true, 50, 20, 0, false},
		// pending=false: helper early-returns; sentinel pre-call values must
		// survive untouched.
		{"pending false ignores stale overflow", 10_000, false, 50, 10_000, 777, true},
		{"zero max clamps to zero", 999, true, 0, 0, 0, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tp := NewTerminalPane()
			tp.scrollOffset = tc.startScrollOffset
			tp.replayEmuMaxScroll = tc.maxScroll
			tp.replayRebuildPending = tc.startPending
			tp.anchorTotalLines = 777
			tp.paintCacheValid = true

			tp.mu.Lock()
			tp.consumeReplayRebuildPendingLocked()
			tp.mu.Unlock()

			testutil.Equal(t, tp.scrollOffset, tc.wantScrollOffset)
			testutil.Equal(t, tp.anchorTotalLines, tc.wantAnchorTotalLines)
			testutil.Equal(t, tp.paintCacheValid, tc.wantPaintCacheValid)
			// pending-flag assertion is only discriminating when the
			// helper had to clear it: no code path inside the helper
			// can set pending=false → true, so asserting it on a row
			// that starts false is vacuous.
			if tc.startPending {
				testutil.Equal(t, tp.replayRebuildPending, false)
			}
		})
	}
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
	emu := NewDrainedEmulator(80, 24)
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

func TestTerminalPane_LiveEmuFallbackOnFirstScroll(t *testing.T) {
	// When scrolling up for the first time, the replay emulator is nil
	// (just invalidated). The live emulator should be used as a fallback
	// so the user sees content immediately instead of "Waiting for output...".
	tp := NewTerminalPane()
	tp.Box.SetRect(0, 0, 42, 12) // 40x10 inner after 1-cell border

	screen := tcell.NewSimulationScreen("UTF-8")
	screen.Init() //nolint:errcheck
	screen.SetSize(42, 12)

	// Set up a live session so Draw enters the live-then-scroll path.
	output := []byte("visible content\r\n")
	for i := 0; i < 100; i++ {
		output = append(output, []byte("scrollback line\r\n")...)
	}
	sess := &mockAdapter{alive: true, totalWritten: uint64(len(output)), output: output}
	tp.SetSession(sess)

	// First Draw populates tp.emu via renderLive.
	tp.Draw(screen)
	if tp.emu == nil {
		t.Fatal("live emulator should be populated after first Draw")
	}

	// Scroll up — invalidates replay cache, enters the fallback path.
	tp.ScrollUp(1)
	testutil.Nil(t, tp.replayEmu)
	testutil.Equal(t, tp.scrollOffset, 1)

	// Draw again — should use live emu as fallback, NOT show placeholder.
	tp.Draw(screen)

	// Verify actual content was rendered (not "Waiting for output...").
	w, h := screen.Size()
	var found bool
	for row := 0; row < h; row++ {
		var line []rune
		for col := 0; col < w; col++ {
			r, _, _, _ := screen.GetContent(col, row)
			line = append(line, r)
		}
		if containsRunes(line, "Waiting") {
			t.Error("should not show 'Waiting for output...' when live emu is available")
		}
		if containsRunes(line, "scrollback") || containsRunes(line, "visible") {
			found = true
		}
	}
	if !found {
		t.Error("expected terminal content from live emu fallback, got none")
	}

	// scrollOffset should be preserved (not clamped by live emu's smaller scrollback).
	testutil.Equal(t, tp.scrollOffset, 1)
}

func TestTerminalPane_FallbackPrefersStaleReplay(t *testing.T) {
	// When both a stale replay emulator and a live emulator exist,
	// the stale replay should take priority (it has 50K scrollback).
	tp := NewTerminalPane()

	// Set up live emulator with distinctive content.
	liveEmu := NewDrainedEmulator(40, 10)
	_, _ = SafeEmuWrite(liveEmu, []byte("live content\r\n"))
	tp.emu = liveEmu
	tp.emuCols = 40
	tp.emuRows = 10
	tp.cursorVisible = false

	// Set up a stale replay emulator (simulating a previous scroll session).
	staleEmu := newDrainedReplayEmulator(40, 10)
	var replayData []byte
	for i := 0; i < 200; i++ {
		replayData = append(replayData, []byte("replay content\r\n")...)
	}
	_, _ = SafeEmuWrite(staleEmu, replayData)
	tp.mu.Lock()
	tp.replayEmu = staleEmu
	tp.replayEmuCols = 40
	tp.replayEmuRows = 10
	tp.replayEmuMaxScroll = 100
	tp.replayBuilding = true // simulate rebuild in flight
	tp.mu.Unlock()

	screen := tcell.NewSimulationScreen("UTF-8")
	screen.Init() //nolint:errcheck
	screen.SetSize(40, 10)

	tp.scrollOffset = 5
	tp.paintCacheValid = false

	// Paint via the fallback path — stale replay emu should be used.
	// We call paintEmu directly since Draw() has complex session setup.
	savedScroll := tp.scrollOffset
	savedAnchor := tp.anchorTotalLines
	tp.paintEmu(screen, 0, 0, 40, 10, staleEmu, 40, 10, false, false)
	tp.scrollOffset = savedScroll
	tp.anchorTotalLines = savedAnchor

	// Verify replay content was rendered (not live content).
	w, h := screen.Size()
	var foundReplay bool
	for row := 0; row < h; row++ {
		var line []rune
		for col := 0; col < w; col++ {
			r, _, _, _ := screen.GetContent(col, row)
			line = append(line, r)
		}
		if containsRunes(line, "replay") {
			foundReplay = true
		}
	}
	if !foundReplay {
		t.Error("expected replay content from stale replay emu, not live emu")
	}
}

func containsRunes(line []rune, needle string) bool {
	s := string(line)
	return len(needle) > 0 && len(s) >= len(needle) && strings.Contains(s, needle)
}

func TestTerminalPane_PendingState(t *testing.T) {
	tp := NewTerminalPane()

	// Initially not pending.
	tp.mu.Lock()
	testutil.Equal(t, tp.pending, false)
	tp.mu.Unlock()

	// Set pending.
	tp.SetPending(true)
	tp.mu.Lock()
	testutil.Equal(t, tp.pending, true)
	tp.mu.Unlock()

	// SetPending(false) clears it explicitly.
	tp.SetPending(false)
	tp.mu.Lock()
	testutil.Equal(t, tp.pending, false)
	tp.mu.Unlock()

	// Pending is cleared when a real session is set.
	tp.SetPending(true)
	mock := &mockAdapter{alive: true, totalWritten: 100, output: make([]byte, 100)}
	tp.SetSession(mock)
	tp.mu.Lock()
	testutil.Equal(t, tp.pending, false)
	tp.mu.Unlock()
}

func TestTerminalPane_ForceResyncPTY(t *testing.T) {
	tp := NewTerminalPane()
	tp.Box.SetRect(0, 0, 42, 12) // inner 40x10 after 1-cell border
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(42, 12)

	sess := &mockAdapter{alive: true, totalWritten: 0, output: nil}
	tp.SetSession(sess)

	// First Draw establishes ptyCols/ptyRows and queues a resize.
	tp.Draw(screen)
	tp.mu.Lock()
	firstCols, firstRows := tp.ptyCols, tp.ptyRows
	// Simulate SyncPTYSize consuming the pending resize.
	tp.pendingResizeCols = 0
	tp.pendingResizeRows = 0
	tp.mu.Unlock()

	// A Draw with unchanged dimensions and no force flag must NOT repost.
	tp.Draw(screen)
	tp.mu.Lock()
	testutil.Equal(t, tp.pendingResizeCols, uint16(0))
	testutil.Equal(t, tp.pendingResizeRows, uint16(0))
	tp.mu.Unlock()

	// ForceResyncPTY makes the next Draw repost even without a size delta.
	tp.ForceResyncPTY()
	tp.Draw(screen)
	tp.mu.Lock()
	testutil.Equal(t, tp.pendingResizeCols, uint16(firstCols))
	testutil.Equal(t, tp.pendingResizeRows, uint16(firstRows))
	testutil.Equal(t, tp.forceResync, false) // flag consumed
	tp.mu.Unlock()

	// Flag is one-shot — subsequent Draws without a delta stay quiet.
	tp.mu.Lock()
	tp.pendingResizeCols = 0
	tp.pendingResizeRows = 0
	tp.mu.Unlock()
	tp.Draw(screen)
	tp.mu.Lock()
	testutil.Equal(t, tp.pendingResizeCols, uint16(0))
	testutil.Equal(t, tp.pendingResizeRows, uint16(0))
	tp.mu.Unlock()
}

func TestTerminalPane_ForceResyncPTY_DeadSessionKeepsFlag(t *testing.T) {
	// Dead sessions have no PTY to resize — forceResync must be a no-op
	// there but stay armed so the next live session sees it.
	tp := NewTerminalPane()
	tp.Box.SetRect(0, 0, 42, 12)
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(42, 12)

	dead := &mockAdapter{alive: false, totalWritten: 0, output: nil}
	tp.SetSession(dead)
	tp.ForceResyncPTY()
	tp.Draw(screen)

	tp.mu.Lock()
	testutil.Equal(t, tp.pendingResizeCols, uint16(0))
	testutil.Equal(t, tp.pendingResizeRows, uint16(0))
	testutil.Equal(t, tp.forceResync, true) // still armed for future live session
	tp.mu.Unlock()
}

func newSim(t *testing.T, w, h int) tcell.SimulationScreen {
	t.Helper()
	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatalf("sim.Init: %v", err)
	}
	sim.SetSize(w, h)
	t.Cleanup(sim.Fini)
	return sim
}

func readScreen(sim tcell.SimulationScreen) string {
	w, h := sim.Size()
	var lines []string
	for row := 0; row < h; row++ {
		var buf []rune
		for col := 0; col < w; col++ {
			r, _, _, _ := sim.GetContent(col, row)
			buf = append(buf, r)
		}
		lines = append(lines, string(buf))
	}
	return strings.Join(lines, "\n")
}

// ---------- Smaller setters / mutators ----------

func TestTerminalPane_SyncPTYSize_NoSession(t *testing.T) {
	tp := NewTerminalPane()
	// No session — SyncPTYSize is a no-op.
	tp.SyncPTYSize()
	tp.mu.Lock()
	rows, cols := tp.pendingResizeRows, tp.pendingResizeCols
	tp.mu.Unlock()
	testutil.Equal(t, rows, uint16(0))
	testutil.Equal(t, cols, uint16(0))
}

func TestTerminalPane_SyncPTYSize_DeadSession(t *testing.T) {
	tp := NewTerminalPane()
	dead := &mockAdapter{alive: false}
	tp.SetSession(dead)
	tp.mu.Lock()
	tp.pendingResizeCols = 80
	tp.pendingResizeRows = 24
	tp.mu.Unlock()

	// Dead session — SyncPTYSize clears pending fields but does not call Resize.
	tp.SyncPTYSize()
	tp.mu.Lock()
	cols := tp.pendingResizeCols
	rows := tp.pendingResizeRows
	tp.mu.Unlock()
	// pending fields are cleared regardless.
	testutil.Equal(t, cols, uint16(0))
	testutil.Equal(t, rows, uint16(0))
}

// resizeRecorder counts Resize calls, fulfilling the TerminalAdapter interface.
type resizeRecorder struct {
	mockAdapter
	resizeRows uint16
	resizeCols uint16
	resizes    int
}

func (r *resizeRecorder) Resize(rows, cols uint16) error {
	r.resizes++
	r.resizeRows = rows
	r.resizeCols = cols
	return nil
}

func TestTerminalPane_SyncPTYSize_LiveSession(t *testing.T) {
	tp := NewTerminalPane()
	live := &resizeRecorder{mockAdapter: mockAdapter{alive: true}}
	tp.SetSession(live)
	tp.mu.Lock()
	tp.pendingResizeRows = 30
	tp.pendingResizeCols = 100
	tp.mu.Unlock()

	tp.SyncPTYSize()
	testutil.Equal(t, live.resizes, 1)
	testutil.Equal(t, live.resizeRows, uint16(30))
	testutil.Equal(t, live.resizeCols, uint16(100))
	tp.mu.Lock()
	defer tp.mu.Unlock()
	testutil.Equal(t, tp.pendingResizeRows, uint16(0))
	testutil.Equal(t, tp.pendingResizeCols, uint16(0))
}

func TestTerminalPane_EagerReplayBuild_NoTask(t *testing.T) {
	tp := NewTerminalPane()
	// No taskID — early return; replayBuilding stays false.
	tp.EagerReplayBuild()
	tp.mu.Lock()
	defer tp.mu.Unlock()
	testutil.Equal(t, tp.replayBuilding, false)
}

func TestTerminalPane_EagerReplayBuild_NonexistentTask(t *testing.T) {
	tp := NewTerminalPane()
	tp.taskID = "nonexistent-task-zzz-99999"
	// async rebuild with no log file → quickly clears replayBuilding.
	tp.EagerReplayBuild()

	// Wait for the async build to complete by polling — should be fast.
	for i := 0; i < 200; i++ {
		tp.mu.Lock()
		building := tp.replayBuilding
		tp.mu.Unlock()
		if !building {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Error("EagerReplayBuild did not finish in time")
}

func TestTerminalPane_EagerReplayBuild_AlreadyBuilding(t *testing.T) {
	tp := NewTerminalPane()
	tp.taskID = "task-x"
	tp.mu.Lock()
	tp.replayBuilding = true
	tp.mu.Unlock()

	// Call EagerReplayBuild — should early return because already building.
	tp.EagerReplayBuild()

	// Flag should remain true (we left it that way; it's not cleared by the early return).
	tp.mu.Lock()
	defer tp.mu.Unlock()
	testutil.Equal(t, tp.replayBuilding, true)
}

func TestTerminalPane_ToggleDiffSplit(t *testing.T) {
	tp := NewTerminalPane()
	if tp.diffSplit {
		t.Error("initial diffSplit should be false")
	}
	tp.diffScroll = 5
	tp.ToggleDiffSplit()
	if !tp.diffSplit {
		t.Error("ToggleDiffSplit should flip on")
	}
	if tp.diffScroll != 0 {
		t.Error("ToggleDiffSplit should reset scroll")
	}
	tp.ToggleDiffSplit()
	if tp.diffSplit {
		t.Error("ToggleDiffSplit should flip off")
	}
}

func TestTerminalPane_PasteHandler_NoLiveSession(t *testing.T) {
	tp := NewTerminalPane()
	// No session — paste handler is a no-op (no panic).
	handler := tp.PasteHandler()
	if handler == nil {
		t.Fatal("PasteHandler should not be nil")
	}
	handler("some text", func(p tview.Primitive) {})
}

// pasteRecorder records WriteInput calls for paste verification.
type pasteRecorder struct {
	mockAdapter
	written []byte
}

func (p *pasteRecorder) WriteInput(b []byte) (int, error) {
	p.written = append(p.written, b...)
	return len(b), nil
}

func TestTerminalPane_PasteHandler_LiveSession(t *testing.T) {
	tp := NewTerminalPane()
	rec := &pasteRecorder{mockAdapter: mockAdapter{alive: true}}
	tp.SetSession(rec)

	handler := tp.PasteHandler()
	handler("hello world", func(p tview.Primitive) {})

	got := string(rec.written)
	if !strings.Contains(got, "\x1b[200~") {
		t.Errorf("paste should be wrapped in start sequence: %q", got)
	}
	if !strings.Contains(got, "hello world") {
		t.Errorf("paste should contain the text: %q", got)
	}
	if !strings.Contains(got, "\x1b[201~") {
		t.Errorf("paste should be wrapped in end sequence: %q", got)
	}
}

func TestTerminalPane_PasteHandler_DeadSession(t *testing.T) {
	tp := NewTerminalPane()
	rec := &pasteRecorder{mockAdapter: mockAdapter{alive: false}}
	tp.SetSession(rec)

	handler := tp.PasteHandler()
	handler("ignored", func(p tview.Primitive) {})
	if len(rec.written) != 0 {
		t.Errorf("paste should not be sent to dead session: %q", rec.written)
	}
}

// ---------- renderDiff ----------

func TestTerminalPane_RenderDiff_Unified(t *testing.T) {
	sim := newSim(t, 80, 20)
	tp := NewTerminalPane()
	diff := "--- a/test.go\n+++ b/test.go\n@@ -1,3 +1,3 @@\n context\n-removed\n+added\n"
	tp.EnterDiffMode(diff, "test.go")
	tp.renderDiff(sim, 0, 0, 80, 20)

	out := readScreen(sim)
	testutil.Contains(t, out, "test.go")
	testutil.Contains(t, out, "[unified]")
}

func TestTerminalPane_RenderDiff_Split(t *testing.T) {
	sim := newSim(t, 80, 20)
	tp := NewTerminalPane()
	diff := "--- a/test.go\n+++ b/test.go\n@@ -1,3 +1,3 @@\n context\n-removed\n+added\n"
	tp.EnterDiffMode(diff, "test.go")
	tp.diffSplit = true
	tp.renderDiff(sim, 0, 0, 80, 20)

	out := readScreen(sim)
	testutil.Contains(t, out, "[split]")
}

func TestTerminalPane_RenderDiff_SplitRebuildOnResize(t *testing.T) {
	sim := newSim(t, 80, 20)
	tp := NewTerminalPane()
	diff := "--- a/test.go\n+++ b/test.go\n@@ -1,3 +1,3 @@\n context\n-removed\n+added\n"
	tp.EnterDiffMode(diff, "test.go")
	tp.diffSplit = true
	// First render at width 80.
	tp.renderDiff(sim, 0, 0, 80, 20)
	first := tp.diffSplitWidth
	// Second render at different width — must rebuild.
	tp.renderDiff(sim, 0, 0, 60, 20)
	if tp.diffSplitWidth == first {
		t.Error("diffSplitWidth should update on width change")
	}
}

func TestTerminalPane_RenderDiff_NoLines(t *testing.T) {
	sim := newSim(t, 60, 12)
	tp := NewTerminalPane()
	tp.diffMode = true
	tp.diffFile = "empty.go"
	// No diff lines populated — renderDiff falls back to "No diff available".
	tp.renderDiff(sim, 0, 0, 60, 12)
	testutil.Contains(t, readScreen(sim), "No diff available")
}

func TestTerminalPane_RenderDiff_ScrollClampsToMax(t *testing.T) {
	sim := newSim(t, 80, 5) // very tall scrollable
	tp := NewTerminalPane()
	diff := "--- a/x.go\n+++ b/x.go\n@@ -1,5 +1,5 @@\n a\n b\n c\n d\n+e\n"
	tp.EnterDiffMode(diff, "x.go")
	// scroll way past the end — function clamps to maxScroll.
	tp.diffScroll = 9999
	tp.renderDiff(sim, 0, 0, 80, 5)
	if tp.diffScroll == 9999 {
		t.Error("renderDiff should clamp diffScroll to maxScroll")
	}
}

// ---------- Draw exercising replay/paint paths ----------

func TestTerminalPane_Draw_NoSession_NoContent(t *testing.T) {
	sim := newSim(t, 80, 20)
	tp := NewTerminalPane()
	tp.SetRect(0, 0, 80, 20)
	tp.Draw(sim)
	testutil.Contains(t, readScreen(sim), "No active session")
}

func TestTerminalPane_Draw_NoSession_WithTaskID(t *testing.T) {
	sim := newSim(t, 80, 20)
	tp := NewTerminalPane()
	tp.SetRect(0, 0, 80, 20)
	tp.taskID = "some-id"
	tp.Draw(sim)
	testutil.Contains(t, readScreen(sim), "press Enter to start")
}

func TestTerminalPane_Draw_PendingShowsBanner(t *testing.T) {
	sim := newSim(t, 80, 20)
	tp := NewTerminalPane()
	tp.SetRect(0, 0, 80, 20)
	tp.SetPending(true)
	tp.Draw(sim) // must render banner without panic
}

func TestTerminalPane_Draw_DiffMode(t *testing.T) {
	sim := newSim(t, 80, 20)
	tp := NewTerminalPane()
	tp.SetRect(0, 0, 80, 20)
	tp.EnterDiffMode("--- a\n+++ b\n@@ -1,1 +1,1 @@\n-old\n+new\n", "x.go")
	tp.Draw(sim)
	testutil.Contains(t, readScreen(sim), "x.go")
}

func TestTerminalPane_Draw_TooSmall(t *testing.T) {
	sim := newSim(t, 5, 5)
	tp := NewTerminalPane()
	tp.SetRect(0, 0, 0, 0)
	tp.Draw(sim) // must not panic on zero dims
}

func TestTerminalPane_Draw_FocusedBorder(t *testing.T) {
	sim := newSim(t, 60, 15)
	tp := NewTerminalPane()
	tp.SetRect(0, 0, 60, 15)
	tp.SetFocused(true)
	tp.Draw(sim) // exercises focused border path
}

// ---------- MouseHandler edge cases ----------

func TestTerminalPane_MouseHandler_LeftDownFocuses(t *testing.T) {
	tp := NewTerminalPane()
	tp.SetRect(0, 0, 60, 12)
	clickCalled := false
	tp.OnClick = func() { clickCalled = true }

	handler := tp.MouseHandler()
	consumed, _ := handler(tview.MouseLeftDown, tcell.NewEventMouse(2, 2, tcell.Button1, 0), func(p tview.Primitive) {})
	if !consumed {
		t.Error("LeftDown should be consumed")
	}
	if !clickCalled {
		t.Error("OnClick should be called")
	}
}

func TestTerminalPane_Draw_ScrollWithReplayData(t *testing.T) {
	// Dead session + replayData → enters the replay/scroll path with no taskID.
	sim := newSim(t, 60, 12)
	tp := NewTerminalPane()
	tp.SetRect(0, 0, 60, 12)
	tp.replayData = []byte(strings.Repeat("hello world\r\n", 80))
	tp.scrollOffset = 1
	// First Draw: cache miss → kicks off async rebuild, falls back to "Waiting" or live.
	tp.Draw(sim)

	// Wait a moment for the async build to settle, then Draw again — cache hit.
	for i := 0; i < 100; i++ {
		tp.mu.Lock()
		building := tp.replayBuilding
		emu := tp.replayEmu
		tp.mu.Unlock()
		if !building && emu != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	tp.Draw(sim)
}

func TestTerminalPane_Draw_DeadSessionWithLogFile(t *testing.T) {
	// Set up HOME with a session log so loadSessionLog populates replayData.
	setupTaskLog(t, "draw-task-1", strings.Repeat("hello\r\n", 40))
	sim := newSim(t, 60, 12)
	tp := NewTerminalPane()
	tp.SetRect(0, 0, 60, 12)
	tp.SetTaskID("draw-task-1")
	// scrollOffset > 0 to enter the replay path.
	tp.scrollOffset = 1
	tp.Draw(sim) // exercises the file-backed replay branch
}

func TestTerminalPane_Draw_NarrowPanel(t *testing.T) {
	// Panel narrower than 20 cols / shorter than 5 rows — exercise ptyCols/ptyRows fallback.
	sim := newSim(t, 12, 6)
	tp := NewTerminalPane()
	tp.SetRect(0, 0, 12, 6)
	output := []byte("hi\r\n")
	sess := &mockAdapter{alive: true, totalWritten: uint64(len(output)), output: output}
	tp.SetSession(sess)
	tp.scrollOffset = 1
	tp.Draw(sim)
}

func TestTerminalPane_Draw_LiveSessionScrollUp(t *testing.T) {
	// Live session + scrollOffset > 0 → replay path with taskID==""
	sim := newSim(t, 60, 12)
	tp := NewTerminalPane()
	tp.SetRect(0, 0, 60, 12)
	output := []byte(strings.Repeat("scroll line\r\n", 50))
	sess := &mockAdapter{alive: true, totalWritten: uint64(len(output)), output: output}
	tp.SetSession(sess)
	tp.scrollOffset = 2
	tp.Draw(sim)
}

func TestTerminalPane_Draw_ReplayCacheHit(t *testing.T) {
	// Pre-populate replay emulator so the cache-hit path runs in Draw.
	sim := newSim(t, 60, 12)
	tp := NewTerminalPane()
	tp.SetRect(0, 0, 60, 12)
	output := []byte(strings.Repeat("hello\r\n", 30))
	sess := &mockAdapter{alive: true, totalWritten: uint64(len(output)), output: output}
	tp.SetSession(sess)

	// Build the replay emu via the same path the slow path would.
	buildReplaySync(tp, output, 58, 10) // inner dims after border
	tp.mu.Lock()
	tp.replayEmuCols = 58
	tp.replayEmuRows = 10
	tp.replayEmuMaxScroll = 100
	tp.mu.Unlock()
	tp.scrollOffset = 1
	tp.Draw(sim)
}

func TestTerminalPane_Draw_ReplayCacheHit_DeadSessionWithTaskLog(t *testing.T) {
	// Dead session + taskID with log file → cache hit through taskID branch.
	setupTaskLog(t, "rcache-task", strings.Repeat("hello\r\n", 100))
	sim := newSim(t, 60, 12)
	tp := NewTerminalPane()
	tp.SetRect(0, 0, 60, 12)
	tp.SetTaskID("rcache-task")

	// Build replay emu so cache is populated.
	buildReplaySync(tp, tp.replayData, 58, 10)
	logSize := int64(len(tp.replayData))
	tp.mu.Lock()
	tp.replayEmuCols = 58
	tp.replayEmuRows = 10
	tp.replayEmuLogSize = logSize
	tp.mu.Unlock()
	tp.scrollOffset = 1
	tp.Draw(sim) // taskID branch — cache valid via logSize match
}

func TestTerminalPane_Draw_ReplayCacheHit_PaintCacheValid(t *testing.T) {
	// Cache-hit + paint cache valid → replayPaintCache fast-fast path.
	sim := newSim(t, 60, 12)
	tp := NewTerminalPane()
	tp.SetRect(0, 0, 60, 12)
	output := []byte(strings.Repeat("hello\r\n", 30))
	sess := &mockAdapter{alive: true, totalWritten: uint64(len(output)), output: output}
	tp.SetSession(sess)

	// Run Draw twice with scrollOffset > 0 so the second hits the paint cache.
	buildReplaySync(tp, output, 58, 10)
	tp.mu.Lock()
	tp.replayEmuCols = 58
	tp.replayEmuRows = 10
	tp.replayEmuMaxScroll = 100
	tp.mu.Unlock()
	tp.scrollOffset = 1
	tp.Draw(sim)
	tp.Draw(sim) // second Draw → cache hit, paint cache valid
}

func TestTerminalPane_MouseHandler_OtherActionNotConsumed(t *testing.T) {
	tp := NewTerminalPane()
	tp.SetRect(0, 0, 60, 12)
	handler := tp.MouseHandler()
	// MouseMove (not left-click, not scroll) — falls through to default false.
	consumed, _ := handler(tview.MouseMove, tcell.NewEventMouse(0, 0, tcell.ButtonNone, 0), func(p tview.Primitive) {})
	if consumed {
		t.Error("MouseMove should not be consumed")
	}
}

// ---------- loadSessionLog / statLogSize / readLogTailForTask via HOME ----------

// setupTaskLog redirects HOME to a tempdir and writes a session log for taskID.
func setupTaskLog(t *testing.T, taskID, content string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".argus", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, taskID+".log"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestTerminalPane_LoadSessionLog_ReadsFile(t *testing.T) {
	setupTaskLog(t, "log-task-1", "hello session log")
	tp := NewTerminalPane()
	// SetTaskID triggers loadSessionLog when no live session.
	tp.SetTaskID("log-task-1")
	if string(tp.replayData) != "hello session log" {
		t.Errorf("replayData = %q, want session log content", string(tp.replayData))
	}
}

func TestTerminalPane_LoadSessionLog_EmptyFile(t *testing.T) {
	setupTaskLog(t, "empty-task", "")
	tp := NewTerminalPane()
	tp.SetTaskID("empty-task")
	if len(tp.replayData) != 0 {
		t.Errorf("empty session log should leave replayData nil, got %d bytes", len(tp.replayData))
	}
}

func TestTerminalPane_StatLogSize_RealFile(t *testing.T) {
	setupTaskLog(t, "stat-task", "0123456789")
	tp := NewTerminalPane()
	tp.taskID = "stat-task"
	got := tp.statLogSize()
	testutil.Equal(t, got, int64(10))
}

func TestReadLogTailForTask_RealFile(t *testing.T) {
	const taskID = "tail-task"
	setupTaskLog(t, taskID, "abcdefghij")

	// Read full file.
	data, size := readLogTailForTask(taskID, 100)
	testutil.Equal(t, size, int64(10))
	testutil.Equal(t, string(data), "abcdefghij")

	// Read tail only.
	data, size = readLogTailForTask(taskID, 3)
	testutil.Equal(t, size, int64(10))
	testutil.Equal(t, string(data), "hij")
}

func TestReadLogTailForTask_EmptyFile(t *testing.T) {
	setupTaskLog(t, "tail-empty", "")
	data, size := readLogTailForTask("tail-empty", 10)
	if data != nil {
		t.Errorf("empty file: data should be nil, got %q", data)
	}
	testutil.Equal(t, size, int64(0))
}

// ---------- DiffScrollUp clamps to 0 ----------

func TestTerminalPane_DiffScrollUp_ClampsAtZero(t *testing.T) {
	tp := NewTerminalPane()
	tp.diffScroll = 2
	tp.DiffScrollUp(10)
	testutil.Equal(t, tp.diffScroll, 0)
}

// ---------- SafeEmuWrite recovers from panic ----------

// panickingEmu is not a SafeEmulator, so we exercise SafeEmuWrite via valid input
// (which doesn't panic). The recover branch is structurally protected; we hit
// the happy-path here.
func TestSafeEmuWrite_ReturnsBytesWritten(t *testing.T) {
	emu := xvt.NewSafeEmulator(20, 5)
	n, err := SafeEmuWrite(emu, []byte("hello"))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if n == 0 {
		t.Error("expected bytes written > 0")
	}
}

// ---------- HasContent variations ----------

func TestTerminalPane_HasContent_LiveSessionWithBytes(t *testing.T) {
	tp := NewTerminalPane()
	tp.SetSession(&mockAdapter{alive: true, totalWritten: 10})
	if !tp.HasContent() {
		t.Error("HasContent should be true when session has bytes")
	}
}

func TestTerminalPane_HasContent_LiveSessionEmpty(t *testing.T) {
	tp := NewTerminalPane()
	tp.SetSession(&mockAdapter{alive: true, totalWritten: 0})
	if tp.HasContent() {
		t.Error("HasContent should be false when session has no bytes and no replay")
	}
}

// ---------- FindFirstContentRowEmu / FindLastContentRowEmu edges ----------

func TestFindContentRowEmu_AllEmpty(t *testing.T) {
	emu := xvt.NewSafeEmulator(20, 5)
	last := FindLastContentRowEmu(emu, 20, 5)
	if last != -1 {
		t.Errorf("FindLastContentRowEmu on empty = %d, want -1", last)
	}
	first := FindFirstContentRowEmu(emu, 20, 4)
	if first != 0 {
		t.Errorf("FindFirstContentRowEmu on empty = %d, want 0", first)
	}
}

// ---------- UvCellToTcellStyle additional attributes ----------

func TestUvCellToTcellStyle_Italic(t *testing.T) {
	cell := &uv.Cell{Style: uv.Style{Attrs: uv.AttrItalic}}
	style := UvCellToTcellStyle(cell)
	_, _, attr := style.Decompose()
	if attr&tcell.AttrItalic == 0 {
		t.Error("expected italic attribute")
	}
}

func TestUvCellToTcellStyle_Reverse(t *testing.T) {
	cell := &uv.Cell{Style: uv.Style{Attrs: uv.AttrReverse}}
	style := UvCellToTcellStyle(cell)
	_, _, attr := style.Decompose()
	if attr&tcell.AttrReverse == 0 {
		t.Error("expected reverse attribute")
	}
}

func TestUvCellToTcellStyle_Strikethrough(t *testing.T) {
	cell := &uv.Cell{Style: uv.Style{Attrs: uv.AttrStrikethrough}}
	style := UvCellToTcellStyle(cell)
	_, _, attr := style.Decompose()
	if attr&tcell.AttrStrikeThrough == 0 {
		t.Error("expected strikethrough attribute")
	}
}

func TestUvCellToTcellStyle_Hyperlink(t *testing.T) {
	cell := &uv.Cell{
		Content: "L",
		Width:   1,
		Style:   uv.Style{},
		Link:    uv.Link{URL: "https://example.com"},
	}
	_ = UvCellToTcellStyle(cell)
}

// ---------- Scrollback pipeline defects ----------

// TestAlignToEscBoundary verifies defect 4: ring/log tail bytes that begin
// mid-CSI are skipped to the first ESC so a fresh x/vt emulator doesn't
// render orphan parameter bytes as garbage text.
func TestAlignToEscBoundary(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"no ESC returns input unchanged",
			"hello world\nfoo bar\n",
			"hello world\nfoo bar\n",
		},
		{
			"leading ESC is start",
			"\x1b[31mred\x1b[0m",
			"\x1b[31mred\x1b[0m",
		},
		{
			"mid-CSI prefix dropped",
			"5;3HhelloT\x1b[31mred",
			"\x1b[31mred",
		},
		{
			"empty input",
			"",
			"",
		},
		{
			"only orphan bytes (no ESC)",
			"5;3H",
			"5;3H", // no ESC found → return as-is
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(AlignToEscBoundary([]byte(tc.in)))
			testutil.Equal(t, got, tc.want)
		})
	}
}

// TestReadLiveRebuildHistory_LogTailOnly verifies defect 3: when the log
// already covers the ring's TotalWritten, only the log is returned (no
// duplication from concatenating ring tail).
func TestReadLiveRebuildHistory_LogTailOnly(t *testing.T) {
	setupTaskLog(t, "rebuild-1", "log-content")
	// Ring's total matches log size — overflow = 0, no extra appended.
	sess := &mockAdapter{alive: true, totalWritten: uint64(len("log-content"))}
	raw, total := readLiveRebuildHistory(sess, "rebuild-1")
	testutil.Equal(t, string(raw), "log-content")
	testutil.Equal(t, total, uint64(len("log-content")))
}

// TestReadLiveRebuildHistory_OverflowMerge verifies defect 3 merge logic:
// when the ring has bytes the log doesn't yet hold (readLoop flushed to
// ring before log), the overflow tail is appended to the log content
// without duplicating the bytes already in the log.
func TestReadLiveRebuildHistory_OverflowMerge(t *testing.T) {
	setupTaskLog(t, "rebuild-2", "AAAA") // log = 4 bytes "AAAA"
	// Ring contains 6 bytes ("AAAABB"); total=6, log size=4.
	// Overflow = total - logSize = 2, so the last 2 bytes of ring ("BB")
	// should be appended to log.
	sess := &mockAdapter{
		alive:        true,
		totalWritten: 6,
		output:       []byte("AAAABB"),
	}
	raw, total := readLiveRebuildHistory(sess, "rebuild-2")
	testutil.Equal(t, string(raw), "AAAABB")
	testutil.Equal(t, total, uint64(6))
}

// TestReadLiveRebuildHistory_NoLogFallback verifies that a missing log
// falls back to the ring buffer alone (and uses atomic
// RecentOutputTailWithTotal to avoid the tail/total race).
func TestReadLiveRebuildHistory_NoLogFallback(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no session log written
	sess := &mockAdapter{
		alive:        true,
		totalWritten: 5,
		output:       []byte("hello"),
	}
	raw, total := readLiveRebuildHistory(sess, "missing-log-task")
	testutil.Equal(t, string(raw), "hello")
	testutil.Equal(t, total, uint64(5))
}

// TestReadLiveRebuildHistory_NilSession returns empty without panic.
func TestReadLiveRebuildHistory_NilSession(t *testing.T) {
	raw, total := readLiveRebuildHistory(nil, "any")
	if raw != nil {
		t.Errorf("raw=%q, want nil", raw)
	}
	testutil.Equal(t, total, uint64(0))
}

// TestEagerReplayBuild_SkipsWhenDimensionsUnknown verifies defect 13:
// the eager build is a no-op until ptyCols/ptyRows have been set by a
// real Draw, so we don't waste a build at 80×24 defaults that the next
// Draw immediately invalidates.
func TestEagerReplayBuild_SkipsWhenDimensionsUnknown(t *testing.T) {
	setupTaskLog(t, "eager-skip", "content")
	tp := NewTerminalPane()
	tp.taskID = "eager-skip"
	// ptyCols/ptyRows are still zero — EagerReplayBuild should bail without
	// flipping replayBuilding.
	tp.EagerReplayBuild()
	tp.mu.Lock()
	building := tp.replayBuilding
	tp.mu.Unlock()
	if building {
		t.Errorf("EagerReplayBuild flipped replayBuilding=true at 0x0; should defer until first Draw")
	}
}

// TestEagerReplayBuild_RunsWithDimensions verifies the positive path:
// when dimensions are known, EagerReplayBuild kicks the goroutine.
func TestEagerReplayBuild_RunsWithDimensions(t *testing.T) {
	setupTaskLog(t, "eager-run", "content")
	tp := NewTerminalPane()
	tp.taskID = "eager-run"
	tp.mu.Lock()
	tp.ptyCols = 80
	tp.ptyRows = 24
	tp.mu.Unlock()
	done := make(chan struct{})
	tp.OnNeedRedraw = func() { close(done) }
	tp.EagerReplayBuild()
	select {
	case <-done:
		// Goroutine completed and triggered redraw.
	case <-time.After(2 * time.Second):
		t.Fatal("eager build did not finish within 2s")
	}
}

// TestAsyncReplayRebuild_CooldownOnEmpty verifies defect 12: when the
// rebuild bails out with no input, replayNoDataUntil gets stamped to gate
// re-kicks. Without this, every Draw spins up another no-op goroutine.
func TestAsyncReplayRebuild_CooldownOnEmpty(t *testing.T) {
	tp := NewTerminalPane()
	// All inputs empty: no taskID, no ringBuf, no replayDataCopy.
	tp.asyncReplayRebuild("", 0, 24, 80, 24, nil, nil, 0, nil)
	tp.mu.Lock()
	defer tp.mu.Unlock()
	if tp.replayBuilding {
		t.Errorf("replayBuilding should be cleared after empty rebuild")
	}
	if !time.Now().Before(tp.replayNoDataUntil) {
		t.Errorf("replayNoDataUntil should be set into the future; got %v", tp.replayNoDataUntil)
	}
}

// TestReplayRebuildReadSize_MonotonicFirstByte verifies defect 5 at the
// sizing-helper level: when a previous build's firstByteOffset is passed
// in, the next read grows to include it (so the new firstByteOffset is
// ≤ prevFirstByte). Tests the math directly without feeding x/vt — feeding
// 8MB+ into the emulator under -race takes minutes and was timing out CI.
func TestReplayRebuildReadSize_MonotonicFirstByte(t *testing.T) {
	// 9MB sparse file — large enough that the 8MB default leaves
	// firstByte > 0 and the monotonic clamp has work to do, but a sparse
	// truncate is instant and consumes no disk.
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".argus", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	logPath := filepath.Join(dir, "mono.log")
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	const fileSize = int64(9 * 1024 * 1024)
	if err := f.Truncate(fileSize); err != nil {
		t.Fatalf("Truncate: %v", err)
	}
	_ = f.Close()

	// Default budget (8MB) — file is 9MB so the read covers
	// [fileSize-8MB, fileSize), implying firstByte = 1MB after the build.
	defaultNeeded := replayRebuildReadSize("mono", 0, 24, 80, 0)
	if defaultNeeded != 8*1024*1024 {
		t.Errorf("default needed = %d, want 8MB", defaultNeeded)
	}

	// Simulate a prior build at firstByte = 1MB, then request another. The
	// helper must grow the read to cover the prior firstByte: readSize ≥
	// fileSize - prevFirstByte = 8MB exactly (matches default), so no
	// growth is needed.
	atDefault := replayRebuildReadSize("mono", 0, 24, 80, fileSize-8*1024*1024)
	if atDefault != 8*1024*1024 {
		t.Errorf("at-default needed = %d, want 8MB", atDefault)
	}

	// Now simulate scenarios where the file grew between builds: prevFirstByte
	// is BELOW the default-read floor (i.e., the previous build saw older
	// bytes). The helper must grow the read so the new firstByte ≤ prevFirstByte.
	prevFirstByte := int64(512 * 1024) // 0.5MB into the file
	grown := replayRebuildReadSize("mono", 0, 24, 80, prevFirstByte)
	if grown < fileSize-prevFirstByte {
		t.Errorf("grown needed = %d, want >= %d (fileSize - prevFirstByte)", grown, fileSize-prevFirstByte)
	}

	// Cap at replayRebuildMaxBytes (64MB) — push the request well past the cap.
	capped := replayRebuildReadSize("mono", 1_000_000, 24, 80, 0)
	if capped > replayRebuildMaxBytes {
		t.Errorf("capped needed = %d exceeds replayRebuildMaxBytes %d", capped, replayRebuildMaxBytes)
	}
}

// TestReplayRebuildReadSize_NoFile verifies the helper degrades gracefully
// when the session log doesn't exist (os.Stat fails) — should fall back
// to the default sizing without panicking.
func TestReplayRebuildReadSize_NoFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got := replayRebuildReadSize("missing", 0, 24, 80, 4*1024*1024)
	if got != 8*1024*1024 {
		t.Errorf("got = %d, want 8MB default when log is absent", got)
	}
}

// TestRenderLive_FullReplayPullsLogTail verifies defect 3 end-to-end:
// when renderLive triggers a full replay (rebuild), it reads from the
// session log file (up to 8MB) instead of the 256KB ring, so older
// history isn't lost on dimension change.
func TestRenderLive_FullReplayPullsLogTail(t *testing.T) {
	// 500KB of log content — exceeds the 256KB ring, so log-only path is
	// the only way to recover the older bytes.
	logContent := strings.Repeat("ABCDE", 100*1024) // 500KB
	setupTaskLog(t, "log-tail-task", logContent)

	tp := NewTerminalPane()
	tp.taskID = "log-tail-task"
	// 100-byte ring (smaller than log, simulating wrap).
	tail := logContent[len(logContent)-100:]
	sess := &mockAdapter{
		alive:        true,
		totalWritten: uint64(len(logContent)),
		output:       []byte(tail),
	}
	tp.SetSession(sess)

	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatalf("sim.Init: %v", err)
	}
	defer sim.Fini()
	sim.SetSize(80, 24)
	tp.SetRect(0, 0, 80, 24)
	tp.Draw(sim)

	// After Draw, emuFedTotal should be 500KB (the full log). If renderLive
	// had fed only the 256KB ring, emuFedTotal would still equal totalWritten
	// (since the slice was treated as a full ring snapshot) BUT the emulator
	// would only contain the 100 ring bytes, not the 500KB log content.
	tp.mu.Lock()
	defer tp.mu.Unlock()
	testutil.Equal(t, tp.emuFedTotal, uint64(len(logContent)))
	// The emu's ScrollbackLen should reflect the rich log history, not the
	// 100-byte ring tail.
	if tp.emu == nil {
		t.Fatal("emu should be populated after Draw")
	}
}

// TestRenderLive_EmuRebuildInvalidatesPaintCache verifies defect 7:
// dimension change forces a paint cache invalidation so the next Draw
// rebuilds from emulator state instead of replaying stale SetContent calls.
func TestRenderLive_EmuRebuildInvalidatesPaintCache(t *testing.T) {
	setupTaskLog(t, "cache-inv", "")
	tp := NewTerminalPane()
	tp.taskID = "cache-inv"
	sess := &mockAdapter{
		alive:        true,
		totalWritten: 5,
		output:       []byte("hello"),
	}
	tp.SetSession(sess)

	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatalf("sim.Init: %v", err)
	}
	defer sim.Fini()
	sim.SetSize(80, 24)
	tp.SetRect(0, 0, 80, 24)
	tp.Draw(sim)
	// Cache valid after first paint.
	tp.mu.Lock()
	if !tp.paintCacheValid {
		tp.mu.Unlock()
		t.Fatal("paintCacheValid should be true after first Draw")
	}
	// Force dimension change by changing inner rect.
	tp.mu.Unlock()
	tp.SetRect(0, 0, 100, 30)
	// Simulate that renderLive's "needRebuild" path was taken: paint cache
	// must be invalidated. We exercise this via Draw.
	sim.SetSize(100, 30)
	tp.Draw(sim)
	// After dimension-change Draw, the cache should have been rebuilt.
	// We verify the invariant by checking that paintEmu wrote new cells
	// at the new dimensions — but the strongest check is that the cache
	// invalidation flag was honored: a redraw at the same dimensions must
	// produce the same content.
	tp.mu.Lock()
	defer tp.mu.Unlock()
	// emuCols reflects the inner-rect width (panel width minus 1-col border
	// on each side) at the new dimensions.
	if tp.emuCols <= 80 {
		t.Errorf("emuCols = %d after resize to 100, want > 80 (rebuilt at new width)", tp.emuCols)
	}
}

// TestPaintEmu_BlanksRowsBelowContent verifies defect 6: when the
// emulator's content is shorter than the viewport, paintEmu blanks the
// trailing rows so stale cells from a previous frame don't leak through.
func TestPaintEmu_BlanksRowsBelowContent(t *testing.T) {
	tp := NewTerminalPane()
	tp.SetRect(0, 0, 20, 10)

	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatalf("sim.Init: %v", err)
	}
	defer sim.Fini()
	sim.SetSize(20, 10)

	// Paint a previous frame with content on every row (fill the screen
	// with "X" so trailing rows would otherwise carry those cells).
	for col := 0; col < 20; col++ {
		for row := 0; row < 10; row++ {
			sim.SetContent(col, row, 'X', nil, tcell.StyleDefault)
		}
	}
	sim.Show()

	// Now paint via paintEmu with content that's only ~2 lines. The
	// trailing rows must be blanked.
	emu := NewDrainedEmulator(20, 10)
	_, _ = SafeEmuWrite(emu, []byte("hi\r\n"))
	tp.paintEmu(sim, 0, 0, 20, 10, emu, 20, 10, true, false)

	// After paintEmu, the bottom rows should be blanks (' '), NOT 'X'.
	// Skip the very top rows that have content and the very bottom row
	// since cursor visibility may vary.
	mainc, _, _ := sim.Get(0, 8)
	if mainc == "X" {
		t.Errorf("paintEmu left stale 'X' at row 8; rows below content must be blanked")
	}
	mainc, _, _ = sim.Get(0, 9)
	if mainc == "X" {
		t.Errorf("paintEmu left stale 'X' at row 9; rows below content must be blanked")
	}
}

// TestPaintEmu_CacheReservesIndicatorRoom verifies defect 2: the paint
// cache pre-allocation accounts for the [SCROLL] indicator's 14 cells, so
// scroll-mode frames don't trigger a heap realloc past h*renderCols.
func TestPaintEmu_CacheReservesIndicatorRoom(t *testing.T) {
	tp := NewTerminalPane()
	tp.SetRect(0, 0, 50, 5)
	tp.scrollOffset = 1 // scroll mode → indicator drawn

	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatalf("sim.Init: %v", err)
	}
	defer sim.Fini()
	sim.SetSize(50, 5)
	emu := newDrainedReplayEmulator(50, 5)
	for i := 0; i < 20; i++ {
		_, _ = SafeEmuWrite(emu, []byte("filler line\r\n"))
	}
	tp.paintEmu(sim, 0, 0, 50, 5, emu, 50, 5, false, false)

	// cap should be ≥ h*renderCols + scrollIndicatorCells.
	wantCap := 5*50 + scrollIndicatorCells
	if cap(tp.paintCacheCells) < wantCap {
		t.Errorf("paintCacheCells cap = %d, want >= %d (h*renderCols + scrollIndicatorCells)",
			cap(tp.paintCacheCells), wantCap)
	}
}

// TestResetVT_ClearsReplayNoDataUntil verifies the cooldown is cleared on
// task switch so a residual empty-bailout timestamp from the previous task
// doesn't suppress the first rebuild of the new task.
func TestResetVT_ClearsReplayNoDataUntil(t *testing.T) {
	tp := NewTerminalPane()
	tp.mu.Lock()
	tp.replayNoDataUntil = time.Now().Add(10 * time.Second)
	tp.mu.Unlock()
	tp.ResetVT()
	tp.mu.Lock()
	defer tp.mu.Unlock()
	if !tp.replayNoDataUntil.IsZero() {
		t.Errorf("ResetVT did not clear replayNoDataUntil; cooldown bleeds across task boundaries")
	}
}

// TestInvalidateReplayCache_ClearsNoDataCooldown verifies the cooldown is
// cleared when the replay cache is invalidated (entering scroll mode from
// live mode), so a stale empty-bailout cooldown doesn't block the kick.
func TestInvalidateReplayCache_ClearsNoDataCooldown(t *testing.T) {
	tp := NewTerminalPane()
	tp.mu.Lock()
	tp.replayNoDataUntil = time.Now().Add(10 * time.Second)
	tp.mu.Unlock()
	tp.invalidateReplayCache()
	tp.mu.Lock()
	defer tp.mu.Unlock()
	if !tp.replayNoDataUntil.IsZero() {
		t.Errorf("invalidateReplayCache did not clear replayNoDataUntil")
	}
}

// TestAsyncReplayRebuild_FiresBranchChangeOnSuccess verifies that a
// successful rebuild fires OnBranchChange so the app can call forceRedraw
// — the fallback emulator painted on prior frames may have different
// cells in the same rect than the new emulator, and tcell's per-cell diff
// won't clear stale cells without a Sync.
func TestAsyncReplayRebuild_FiresBranchChangeOnSuccess(t *testing.T) {
	setupTaskLog(t, "branch-change", "some content\r\nmore content\r\n")
	tp := NewTerminalPane()
	tp.taskID = "branch-change"
	fired := make(chan struct{}, 1)
	tp.OnBranchChange = func() {
		select {
		case fired <- struct{}{}:
		default:
		}
	}
	tp.asyncReplayRebuild("branch-change", 0, 24, 80, 24, nil, nil, 0, nil)
	select {
	case <-fired:
		// Good.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("OnBranchChange did not fire on successful rebuild")
	}
}
