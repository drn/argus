package tui2

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/model"
)

func TestTaskPage_EmptyState(t *testing.T) {
	tl := NewTaskListView()
	flex := tview.NewFlex()
	tp := NewTaskPage(flex, tl)

	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(120, 40)

	tp.SetRect(0, 0, 120, 40)
	// Should draw banner + hint without panic.
	tp.Draw(screen)

	// Verify the hint text is present somewhere on screen.
	found := screenContains(screen, emptyHint)
	if !found {
		t.Error("empty state should render hint text")
	}
}

func TestTaskPage_WithTasks(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{{ID: "1", Name: "test", Project: "proj"}})

	flex := tview.NewFlex().AddItem(tl, 0, 1, true)
	tp := NewTaskPage(flex, tl)

	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(120, 40)

	tp.SetRect(0, 0, 120, 40)
	// Should draw the inner flex (task list), not the banner.
	tp.Draw(screen)

	// Hint should NOT be present.
	if screenContains(screen, emptyHint) {
		t.Error("task page with tasks should not show empty hint")
	}
}

func TestTaskPage_ZeroDimensions(t *testing.T) {
	tl := NewTaskListView()
	flex := tview.NewFlex()
	tp := NewTaskPage(flex, tl)

	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(1, 1)

	tp.SetRect(0, 0, 0, 0)
	// Must not panic.
	tp.Draw(screen)
}

// screenContains checks if a string appears anywhere on the simulation screen.
func screenContains(screen tcell.SimulationScreen, needle string) bool {
	w, h := screen.Size()
	for row := 0; row < h; row++ {
		var line []rune
		for col := 0; col < w; col++ {
			r, _, _, _ := screen.GetContent(col, row)
			line = append(line, r)
		}
		if containsRunes(line, needle) {
			return true
		}
	}
	return false
}

func containsRunes(line []rune, needle string) bool {
	s := string(line)
	return len(needle) > 0 && len(s) >= len(needle) && contains(s, needle)
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
