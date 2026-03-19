package tui2

import (
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
