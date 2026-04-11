package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestSettingsPage_Draw(t *testing.T) {
	sv := testSettingsView(t)
	sp := NewSettingsPage(sv)

	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(120, 40)

	sp.SetRect(0, 0, 120, 40)
	// Should not panic.
	sp.Draw(screen)
}

func TestSettingsPage_DrawSmall(t *testing.T) {
	sv := testSettingsView(t)
	sp := NewSettingsPage(sv)

	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(40, 10)

	sp.SetRect(0, 0, 40, 10)
	// Should not panic — falls back to no-banner mode.
	sp.Draw(screen)
}
