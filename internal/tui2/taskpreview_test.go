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
	tp.RefreshOutput([]byte("Hello, World!\r\n"), 36, 6)
	tp.Draw(screen)
	// Should render cached cells without panic
}

func TestTaskPreviewPanel_RefreshEmptyOutput(t *testing.T) {
	tp := NewTaskPreviewPanel()
	tp.SetTaskID("test-task")

	// Empty output sets status message
	tp.RefreshOutput(nil, 40, 10)

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
	tp.RefreshOutput(data, 10, 5)

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
	tp.RefreshOutput([]byte("data"), 40, 10)

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
	tp.RefreshOutput(raw, 20, 3)
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
