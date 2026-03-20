package tui2

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestBannerHeight(t *testing.T) {
	h := bannerHeight()
	if h < 10 || h > 20 {
		t.Errorf("bannerHeight() = %d, expected between 10 and 20", h)
	}
}

func TestDrawBanner_NoZeroPanic(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()

	// Zero width should not panic.
	rows := drawBanner(screen, 0, 0, 0)
	if rows != 0 {
		t.Errorf("drawBanner with 0 width should return 0, got %d", rows)
	}
}

func TestDrawBanner_Normal(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(120, 40)

	rows := drawBanner(screen, 0, 0, 120)
	if rows != bannerHeight() {
		t.Errorf("drawBanner returned %d rows, expected %d", rows, bannerHeight())
	}
}

func TestDrawBanner_NarrowWidth(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(50, 40)

	// Should not panic even with narrow width.
	rows := drawBanner(screen, 0, 0, 50)
	if rows == 0 {
		t.Error("expected non-zero rows for width 50")
	}
}

func TestFadeDashes(t *testing.T) {
	d := fadeDashes(12, false)
	if len(d) != 12 {
		t.Errorf("fadeDashes(12) len = %d, want 12", len(d))
	}

	d = fadeDashes(0, false)
	if d != "" {
		t.Errorf("fadeDashes(0) should be empty")
	}
}

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
