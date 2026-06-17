package tui

import (
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/terminal"
	"github.com/gdamore/tcell/v2"
)

func TestTaskPreviewPanel_DrawEmpty(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(60, 20)

	tp := NewTaskPreviewPanel()
	tp.SetRect(1, 1, 58, 18)
	tp.Draw(screen)
	// Should render "No task selected" without panic
}

func TestTaskPreviewPanel_DrawNoSession(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(60, 20)

	tp := NewTaskPreviewPanel()
	tp.SetRect(1, 1, 58, 18)
	tp.SetTaskID("nonexistent-task")
	// No RefreshOutput called — should show "Loading..."
	tp.Draw(screen)
}

func TestTaskPreviewPanel_ZeroDimensions(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(1, 1)

	tp := NewTaskPreviewPanel()
	tp.SetRect(0, 0, 0, 0)
	tp.Draw(screen) // must not panic
}

func TestTaskPreviewPanel_RefreshAndDraw(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(40, 10)

	tp := NewTaskPreviewPanel()
	tp.SetRect(1, 1, 38, 8)
	tp.SetTaskID("test-task")

	// Pre-render cells with simple PTY output
	tp.RefreshOutput("test-task", []byte("Hello, World!\r\n"), uint64(len("Hello, World!\r\n")), 36, 6, 36, 6)
	tp.Draw(screen)
	// Should render cached cells without panic
}

func TestTaskPreviewPanel_RefreshEmptyOutput(t *testing.T) {
	tp := NewTaskPreviewPanel()
	tp.SetTaskID("test-task")

	// Empty output sets status message
	tp.RefreshOutput("test-task", nil, 0, 40, 10, 40, 10)

	tp.mu.Lock()
	msg := tp.statusMsg
	tp.mu.Unlock()
	if msg != "Waiting for output..." {
		t.Errorf("expected 'Waiting for output...', got %q", msg)
	}
}

func TestTaskPreviewPanel_DrawSize(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(60, 20)

	tp := NewTaskPreviewPanel()
	tp.SetRect(1, 1, 58, 18)

	// Before Draw(), DrawSize returns 0,0
	w, h := tp.DrawSize()
	if w != 0 || h != 0 {
		t.Errorf("expected 0,0 before Draw, got %d,%d", w, h)
	}

	tp.Draw(screen)

	// After Draw(), DrawSize returns inner dimensions
	w, h = tp.DrawSize()
	if w <= 0 || h <= 0 {
		t.Errorf("expected positive dimensions after Draw, got %d,%d", w, h)
	}
}

func TestSafeEmuWrite_PanicRecovery(t *testing.T) {
	// Create a small emulator and feed data with cursor positioning
	// beyond its bounds (simulates replaying large-terminal PTY data).
	emu := terminal.NewDrainedEmulator(10, 5)

	// ESC[82;1H moves cursor to row 82, then ESC M (reverse index) triggers
	// InsertLineArea which panics if row > buffer length.
	data := []byte("\x1b[82;1H\x1bM")
	_, err := terminal.SafeEmuWrite(emu, data)
	// Either it doesn't panic (upstream fixed) or we recover gracefully.
	if err != nil {
		t.Logf("terminal.SafeEmuWrite recovered from panic: %v", err)
	}
}

func TestTaskPreviewPanel_RefreshPanicRecovery(t *testing.T) {
	tp := NewTaskPreviewPanel()
	tp.SetTaskID("test-task")

	// Feed data that might trigger emulator panic due to size mismatch.
	// CSI 82;1H + reverse index into a 5-row emulator.
	data := []byte("hello\r\n\x1b[82;1H\x1bM")
	tp.RefreshOutput("test-task", data, uint64(len(data)), 10, 5, 10, 5)

	tp.mu.Lock()
	msg := tp.statusMsg
	cells := tp.cells
	tp.mu.Unlock()

	// If panic was recovered, statusMsg should be set and cells nil.
	// If no panic (upstream fixed), cells should be populated.
	if cells == nil && msg != "Preview unavailable" {
		t.Errorf("expected 'Preview unavailable' on panic recovery, got %q", msg)
	}
}

func TestTaskPreviewPanel_SetTaskIDClears(t *testing.T) {
	tp := NewTaskPreviewPanel()
	tp.SetTaskID("task-1")
	tp.RefreshOutput("task-1", []byte("data"), uint64(len("data")), 40, 10, 40, 10)

	// Switching task should clear cells
	tp.SetTaskID("task-2")
	tp.mu.Lock()
	cells := tp.cells
	tp.mu.Unlock()
	if cells != nil {
		t.Error("expected cells to be nil after task change")
	}
}

func TestTaskPreviewPanel_RefreshUsesLatestVisibleLines(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(40, 10)

	tp := NewTaskPreviewPanel()
	tp.SetRect(1, 1, 38, 8)
	tp.SetTaskID("test-task")

	raw := []byte(strings.Join([]string{
		"line-1",
		"line-2",
		"line-3",
		"line-4",
		"line-5",
		"line-6",
	}, "\r\n") + "\r\n")
	tp.RefreshOutput("test-task", raw, uint64(len(raw)), 20, 3, 20, 3)
	tp.Draw(screen)

	if !previewScreenContains(screen, "line-4") {
		t.Fatal("expected preview to include line-4 from the latest visible rows")
	}
	if !previewScreenContains(screen, "line-6") {
		t.Fatal("expected preview to include the newest output line")
	}
	if previewScreenContains(screen, "line-1") {
		t.Fatal("expected preview to drop old top-of-buffer lines")
	}
}

func TestTaskPreviewPanel_LargerEmuThanViewport(t *testing.T) {
	// Simulates a live session where the PTY is taller than the preview panel.
	// Content positioned at the bottom of the tall emulator should still appear
	// in the shorter viewport (not blank space at top).
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(40, 10)

	tp := NewTaskPreviewPanel()
	tp.SetRect(1, 1, 38, 8)
	tp.SetTaskID("test-task")

	// ANSI sequence positions cursor at row 18 (in a 20-row emulator) and writes text.
	// With a 6-row viewport, we should see the bottom content, not blank rows.
	raw := []byte("\x1b[18;1Hbottom-content\r\n\x1b[19;1Hvery-last-line\r\n")
	// emuCols=36, emuRows=20 (PTY size), viewCols=36, viewRows=6 (panel size)
	tp.RefreshOutput("test-task", raw, uint64(len(raw)), 36, 20, 36, 6)
	tp.Draw(screen)

	if !previewScreenContains(screen, "bottom-content") {
		t.Fatal("expected bottom-positioned content to appear in shorter viewport")
	}
	if !previewScreenContains(screen, "very-last-line") {
		t.Fatal("expected last line to appear in viewport")
	}
}

// TestTaskPreviewPanel_AlignsRawToEscBoundary guards the preview path against
// the smudge defect: ring buffer / log tails routinely begin mid-CSI, and
// without ESC-boundary alignment x/vt renders the orphan parameter bytes as
// garbage text (e.g. "5;3H" at the top of the emulator). The preview must
// strip the partial prefix before feeding the emulator. Mirrors the
// TerminalPane behavior in `renderLive`'s full-replay path and
// `asyncReplayRebuild`'s scrollback feed.
func TestTaskPreviewPanel_AlignsRawToEscBoundary(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(40, 10)

	tp := NewTaskPreviewPanel()
	tp.SetRect(1, 1, 38, 8)
	tp.SetTaskID("test-task")

	// Raw bytes start mid-CSI ("5;3H..."): if fed verbatim the emulator
	// renders "5;3H" as orphan literal text. Alignment skips to the
	// first ESC so only the well-formed sequence is parsed.
	raw := []byte("5;3HpartialCSI\x1b[2J\x1b[1;1HCLEAN\r\n")
	tp.RefreshOutput("test-task", raw, uint64(len(raw)), 36, 6, 36, 6)
	tp.Draw(screen)

	if previewScreenContains(screen, "5;3HpartialCSI") {
		t.Fatal("expected mid-CSI orphan bytes to be skipped, but they rendered as literal text")
	}
	if !previewScreenContains(screen, "CLEAN") {
		t.Fatal("expected post-alignment content to render")
	}
}

func TestTaskPreviewPanel_SmallerEmuThanViewport(t *testing.T) {
	// When PTY is shorter than the preview panel, content should still render
	// correctly at the top of the viewport (no blank-top regression).
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(40, 20)

	tp := NewTaskPreviewPanel()
	tp.SetRect(1, 1, 38, 18)
	tp.SetTaskID("test-task")

	// PTY is only 5 rows tall, viewport is 16 rows. Content at row 3.
	raw := []byte("\x1b[3;1Hshort-pty-content\r\n\x1b[4;1Hmore-content\r\n")
	// emuCols=36, emuRows=5 (small PTY), viewCols=36, viewRows=16 (tall panel)
	tp.RefreshOutput("test-task", raw, uint64(len(raw)), 36, 5, 36, 16)
	tp.Draw(screen)

	if !previewScreenContains(screen, "short-pty-content") {
		t.Fatal("expected content from short PTY to appear in taller viewport")
	}
	if !previewScreenContains(screen, "more-content") {
		t.Fatal("expected second line from short PTY to appear")
	}
}

// TestTaskPreviewPanel_NoGhostAcrossIncrementalRefresh is the panel-level
// regression guard for the ghost-cell defect that motivated the persistent
// emulator (Slack D03SBKHGK). The agent draws a progress counter "20 widgets",
// then a later frame collapses the line to "9 widgets" + ESC[K (erase the
// orphaned trailing char). The ring buffer delivers these as a growing tail
// across two refreshes — exactly how the tick loop feeds the panel. With the
// old throwaway-per-refresh emulator the erase fell outside the replayed
// window and a ghost "s" survived; the persistent emulator applies it.
func TestTaskPreviewPanel_NoGhostAcrossIncrementalRefresh(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(40, 10)

	tp := NewTaskPreviewPanel()
	tp.SetRect(1, 1, 38, 8)
	tp.SetTaskID("ghost-task")

	frame1 := []byte("\x1b[2J\x1b[H20 widgets")
	// Frame 2 returns home and overwrites with the shorter "9 widgets", then
	// ESC[K erases the orphaned trailing "s" left from "20 widgets".
	frame2 := []byte("\x1b[H9 widgets\x1b[K")
	cumulative := append(append([]byte{}, frame1...), frame2...)

	// Two incremental refreshes, mirroring the ring tail growing between ticks.
	tp.RefreshOutput("ghost-task", frame1, uint64(len(frame1)), 36, 6, 36, 6)
	tp.RefreshOutput("ghost-task", cumulative, uint64(len(cumulative)), 36, 6, 36, 6)
	tp.Draw(screen)

	if !previewScreenContains(screen, "9 widgets") {
		t.Fatal("expected collapsed frame to render")
	}
	if previewScreenContains(screen, "widgetss") || previewScreenContains(screen, "20 widgets") {
		t.Fatal("ghost cells from the superseded progress frame survived the refresh")
	}
}

// TestTaskPreviewPanel_RendersScrollbackRows exercises the grid build's
// sbLen>0 branch (ScrollbackCellAt). When more lines are written than the
// emulator is tall, the oldest scroll into scrollback (capped at 1 by
// PreviewVT). With a viewport taller than the emulator, the grid must include
// that scrollback row alongside the main-screen rows — and it must be the
// current task's own scrolled-off line, never a prior task's (rebuild's
// ClearScrollback guarantees isolation; this asserts the rendering path works).
func TestTaskPreviewPanel_RendersScrollbackRows(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(40, 12)

	tp := NewTaskPreviewPanel()
	tp.SetRect(1, 1, 38, 10)
	tp.SetTaskID("sb-task")

	// 5 lines into a 4-row emulator (no trailing newline): exactly one scroll
	// fires after "line-4", pushing "line-1" into the (cap-1) scrollback;
	// "line-2".."line-5" stay on the main screen.
	raw := []byte("line-1\r\nline-2\r\nline-3\r\nline-4\r\nline-5")
	// emuRows=4 (short), viewRows=8 (tall) so the viewport reaches into scrollback.
	tp.RefreshOutput("sb-task", raw, uint64(len(raw)), 36, 4, 36, 8)
	tp.Draw(screen)

	if !previewScreenContains(screen, "line-5") {
		t.Fatal("expected newest main-screen row to render")
	}
	if !previewScreenContains(screen, "line-1") {
		t.Fatal("expected the scrolled-off row to render from scrollback (sbLen>0 branch)")
	}
}

// TestTaskPreviewPanel_ConcurrentRefreshAndDraw exercises the vtMu invariant:
// RefreshOutput runs on both the tick goroutine and onTaskCursorChange's
// background goroutine, while Draw runs on the tview goroutine. vtMu serializes
// the two RefreshOutput callers (the persistent emulator is stateful) and Draw
// takes only tp.mu. This test fires both patterns concurrently; it must be run
// under -race to be meaningful (the suite is).
func TestTaskPreviewPanel_ConcurrentRefreshAndDraw(t *testing.T) {
	tp := NewTaskPreviewPanel()
	tp.SetRect(1, 1, 38, 8)
	tp.SetTaskID("race-task")
	raw := []byte("\x1b[2J\x1b[Hconcurrent output")

	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { screen.Fini() })
	screen.SetSize(40, 10)

	var wg sync.WaitGroup
	// Two concurrent RefreshOutput callers (tick + cursor-change goroutines).
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 60 {
				tp.RefreshOutput("race-task", raw, uint64(len(raw))+uint64(j), 36, 6, 36, 6)
			}
		}()
	}
	// A Draw loop (tview goroutine) reading cached cells under tp.mu.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 60 {
			tp.Draw(screen)
		}
	}()
	wg.Wait()
}

func previewScreenContains(screen tcell.SimulationScreen, needle string) bool {
	w, h := screen.Size()
	for row := 0; row < h; row++ {
		var b strings.Builder
		for col := 0; col < w; col++ {
			r, _, _, _ := screen.GetContent(col, row)
			if r == 0 {
				r = ' '
			}
			b.WriteRune(r)
		}
		if strings.Contains(b.String(), needle) {
			return true
		}
	}
	return false
}

func TestReadGitDiff_NotWorktreeSubdir(t *testing.T) {
	got := readGitDiff(t.TempDir())
	testutil.Equal(t, got, "")
}

func TestLoadSessionLog_NoFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got := LoadSessionLog("nonexistent")
	if got != nil {
		t.Error("expected nil for nonexistent log")
	}
}

func TestStatSessionLog_NoFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got := statSessionLog("nonexistent")
	testutil.Equal(t, got, int64(0))
}

func TestLoadSessionLog_Large(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	logPath := agent.SessionLogPath("big")
	if err := os.MkdirAll(strings.TrimSuffix(logPath, "/big.log"), 0o755); err != nil {
		t.Fatal(err)
	}

	parentDir := logPath[:strings.LastIndex(logPath, "/")]
	os.MkdirAll(parentDir, 0o755)
	content := strings.Repeat("a", 80*1024)
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got := LoadSessionLog("big")

	if len(got) != 64*1024 {
		t.Errorf("expected 64KB, got %d", len(got))
	}
}

func TestStatSessionLog_RealFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	logPath := agent.SessionLogPath("x")
	parentDir := logPath[:strings.LastIndex(logPath, "/")]
	os.MkdirAll(parentDir, 0o755)
	os.WriteFile(logPath, []byte("hello"), 0o644)
	got := statSessionLog("x")
	testutil.Equal(t, got, int64(5))
}

func TestGrayscaleColor(t *testing.T) {
	tests := []struct {
		name string
		in   tcell.Color
		want tcell.Color
	}{
		{"default passes through", tcell.ColorDefault, tcell.ColorDefault},
		{"pure red maps to luminance gray", tcell.NewRGBColor(255, 0, 0), tcell.NewRGBColor(76, 76, 76)},
		{"pure green maps to luminance gray", tcell.NewRGBColor(0, 255, 0), tcell.NewRGBColor(149, 149, 149)},
		{"white stays white", tcell.NewRGBColor(255, 255, 255), tcell.NewRGBColor(255, 255, 255)},
		{"black stays black", tcell.NewRGBColor(0, 0, 0), tcell.NewRGBColor(0, 0, 0)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testutil.Equal(t, terminal.GrayscaleColor(tc.in), tc.want)
		})
	}
}

func TestGrayscaleColor_PaletteResolvesToGray(t *testing.T) {
	// A 256-palette color must resolve through Hex() to a true gray (r==g==b),
	// not pass through as a still-colored palette index.
	got := terminal.GrayscaleColor(tcell.PaletteColor(196)) // bright red
	r, g, b := got.RGB()
	if r < 0 || r != g || g != b {
		t.Fatalf("expected gray (r==g==b), got rgb(%d,%d,%d)", r, g, b)
	}
}

func TestDesaturateStyle_GraysBothChannelsKeepsAttrs(t *testing.T) {
	style := tcell.StyleDefault.
		Foreground(tcell.NewRGBColor(200, 30, 30)).
		Background(tcell.NewRGBColor(20, 20, 220)).
		Bold(true)
	out := terminal.DesaturateStyle(style)

	fg, bg, attr := out.Decompose()
	assertGray(t, fg)
	assertGray(t, bg)
	if attr&tcell.AttrBold == 0 {
		t.Error("expected bold attribute to survive desaturation")
	}
}

func TestDesaturateStyle_GraysUnderlineColor(t *testing.T) {
	// SGR 58 sets an explicit (colored) underline. Decompose() doesn't return
	// it, so desaturateStyle must gray it via the dedicated underline channel —
	// otherwise it leaks color into the otherwise-grayscale preview.
	style := tcell.StyleDefault.
		Foreground(tcell.NewRGBColor(200, 30, 30)).
		Underline(tcell.UnderlineStyleCurly, tcell.NewRGBColor(0, 200, 0))
	out := terminal.DesaturateStyle(style)

	assertGray(t, out.GetUnderlineColor())
	// The underline STYLE (curly) must survive — only its color is grayed.
	testutil.Equal(t, out.GetUnderlineStyle(), tcell.UnderlineStyleCurly)
}

func TestDesaturateStyle_DefaultUnderlineColorUntouched(t *testing.T) {
	// A cell with an underline but no explicit color: ulColor is ColorDefault
	// (invalid), so it must pass through unchanged rather than become hard gray.
	style := tcell.StyleDefault.Underline(true)
	out := terminal.DesaturateStyle(style)
	testutil.Equal(t, out.GetUnderlineColor(), tcell.ColorDefault)
}

func TestDesaturateStyle_DefaultForegroundColoredBackground(t *testing.T) {
	// The common terminal pattern: default fg, colored bg. The default fg must
	// stay default (terminal's own), the bg grays.
	style := tcell.StyleDefault.Background(tcell.NewRGBColor(20, 20, 220))
	out := terminal.DesaturateStyle(style)

	fg, bg, _ := out.Decompose()
	testutil.Equal(t, fg, tcell.ColorDefault)
	assertGray(t, bg)
	if !bg.Valid() {
		t.Error("expected background to be grayed, not left default")
	}
}

func TestDesaturateStyle_ReverseAttrSurvives(t *testing.T) {
	style := tcell.StyleDefault.
		Foreground(tcell.NewRGBColor(200, 30, 30)).
		Background(tcell.NewRGBColor(20, 20, 220)).
		Reverse(true)
	out := terminal.DesaturateStyle(style)

	fg, bg, attr := out.Decompose()
	assertGray(t, fg)
	assertGray(t, bg)
	if attr&tcell.AttrReverse == 0 {
		t.Error("expected reverse attribute to survive desaturation")
	}
}

func TestTaskPreviewPanel_RendersGrayscale(t *testing.T) {
	tp := NewTaskPreviewPanel()
	tp.SetTaskID("color-task")
	// SGR 31 = red fg, 44 = blue bg.
	out := []byte("\x1b[31;44mRED ON BLUE\x1b[0m\r\n")
	tp.RefreshOutput("color-task", out, uint64(len(out)), 36, 6, 36, 6)

	tp.mu.Lock()
	cells := tp.cells
	tp.mu.Unlock()
	if cells == nil {
		t.Fatal("expected rendered cells")
	}
	for y := range cells {
		for x := range cells[y] {
			fg, bg, _ := cells[y][x].style.Decompose()
			assertGray(t, fg)
			assertGray(t, bg)
		}
	}
}

// assertGray passes if c is ColorDefault or a true gray (r==g==b).
func assertGray(t *testing.T, c tcell.Color) {
	t.Helper()
	if !c.Valid() {
		return // ColorDefault — preserved, acceptable
	}
	r, g, b := c.RGB()
	if r != g || g != b {
		t.Fatalf("expected gray (r==g==b), got rgb(%d,%d,%d)", r, g, b)
	}
}
