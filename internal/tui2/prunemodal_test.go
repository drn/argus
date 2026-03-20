package tui2

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestPruneModalDraw(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.Init() //nolint:errcheck
	screen.SetSize(80, 24)

	m := NewPruneModal(3)
	m.SetRect(0, 0, 80, 24)
	m.Draw(screen)

	// Tick to toggle animation.
	m.Tick()
	m.Draw(screen)
}

func TestPruneModalDraw_SmallScreen(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.Init() //nolint:errcheck
	screen.SetSize(20, 5)

	m := NewPruneModal(1)
	m.SetRect(0, 0, 20, 5)
	// Should not panic on small screen.
	m.Draw(screen)
}

func TestPruneModalDraw_ZeroSize(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.Init() //nolint:errcheck
	screen.SetSize(80, 24)

	m := NewPruneModal(1)
	// Zero rect — should return early without panic.
	m.SetRect(0, 0, 0, 0)
	m.Draw(screen)
}

func TestPruneModalTick(t *testing.T) {
	m := NewPruneModal(2)
	if m.tickEven {
		t.Error("tickEven should start false")
	}
	m.Tick()
	if !m.tickEven {
		t.Error("tickEven should be true after first Tick")
	}
	m.Tick()
	if m.tickEven {
		t.Error("tickEven should be false after second Tick")
	}
}

func TestPruneModalIncrement(t *testing.T) {
	m := NewPruneModal(5)
	if m.current != 0 {
		t.Errorf("current should start at 0, got %d", m.current)
	}
	m.Increment()
	if m.current != 1 {
		t.Errorf("current should be 1 after Increment, got %d", m.current)
	}
	m.Increment()
	if m.current != 2 {
		t.Errorf("current should be 2 after second Increment, got %d", m.current)
	}
}

func TestPruneModalProgressDisplay(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.Init() //nolint:errcheck
	screen.SetSize(80, 24)

	m := NewPruneModal(5)
	m.SetRect(0, 0, 80, 24)

	// Before any increments — shows static count.
	m.Draw(screen)
	all := readAllScreenText(screen, 80, 24)
	if !strings.Contains(all, "Cleaning up 5 worktree(s)") {
		t.Errorf("expected static count message, got %q", all)
	}

	// After increments — shows progress format.
	m.Increment()
	m.Increment()
	screen.Clear()
	m.Draw(screen)
	all = readAllScreenText(screen, 80, 24)
	if !strings.Contains(all, "(2/5)") {
		t.Errorf("expected progress (2/5), got %q", all)
	}
}

// readAllScreenText reads all text from the simulation screen.
func readAllScreenText(screen tcell.SimulationScreen, width, height int) string {
	var lines []string
	for row := range height {
		var runes []rune
		for col := range width {
			r, _, _, _ := screen.GetContent(col, row)
			runes = append(runes, r)
		}
		lines = append(lines, string(runes))
	}
	return strings.Join(lines, "\n")
}
