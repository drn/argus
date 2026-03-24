package tui2

import (
	"strings"
	"testing"

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
	tp.RefreshOutput([]byte("Hello, World!\r\n"), 36, 6, 36, 6)
	tp.Draw(screen)
	// Should render cached cells without panic
}

func TestTaskPreviewPanel_RefreshEmptyOutput(t *testing.T) {
	tp := NewTaskPreviewPanel()
	tp.SetTaskID("test-task")

	// Empty output sets status message
	tp.RefreshOutput(nil, 40, 10, 40, 10)

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
	emu := newDrainedEmulator(10, 5)

	// ESC[82;1H moves cursor to row 82, then ESC M (reverse index) triggers
	// InsertLineArea which panics if row > buffer length.
	data := []byte("\x1b[82;1H\x1bM")
	_, err := safeEmuWrite(emu, data)
	// Either it doesn't panic (upstream fixed) or we recover gracefully.
	if err != nil {
		t.Logf("safeEmuWrite recovered from panic: %v", err)
	}
}

func TestTaskPreviewPanel_RefreshPanicRecovery(t *testing.T) {
	tp := NewTaskPreviewPanel()
	tp.SetTaskID("test-task")

	// Feed data that might trigger emulator panic due to size mismatch.
	// CSI 82;1H + reverse index into a 5-row emulator.
	data := []byte("hello\r\n\x1b[82;1H\x1bM")
	tp.RefreshOutput(data, 10, 5, 10, 5)

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
	tp.RefreshOutput([]byte("data"), 40, 10, 40, 10)

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
	tp.RefreshOutput(raw, 20, 3, 20, 3)
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
	tp.RefreshOutput(raw, 36, 20, 36, 6)
	tp.Draw(screen)

	if !previewScreenContains(screen, "bottom-content") {
		t.Fatal("expected bottom-positioned content to appear in shorter viewport")
	}
	if !previewScreenContains(screen, "very-last-line") {
		t.Fatal("expected last line to appear in viewport")
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
	tp.RefreshOutput(raw, 36, 5, 36, 16)
	tp.Draw(screen)

	if !previewScreenContains(screen, "short-pty-content") {
		t.Fatal("expected content from short PTY to appear in taller viewport")
	}
	if !previewScreenContains(screen, "more-content") {
		t.Fatal("expected second line from short PTY to appear")
	}
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
