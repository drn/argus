package tui2

import (
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
