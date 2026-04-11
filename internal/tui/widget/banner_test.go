package widget

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestBannerHeight(t *testing.T) {
	h := BannerHeight()
	if h < 10 || h > 20 {
		t.Errorf("BannerHeight() = %d, expected between 10 and 20", h)
	}
}

func TestDrawBanner_NoZeroPanic(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()

	// Zero width should not panic.
	rows := DrawBanner(screen, 0, 0, 0)
	if rows != 0 {
		t.Errorf("DrawBanner with 0 width should return 0, got %d", rows)
	}
}

func TestDrawBanner_Normal(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(120, 40)

	rows := DrawBanner(screen, 0, 0, 120)
	if rows != BannerHeight() {
		t.Errorf("DrawBanner returned %d rows, expected %d", rows, BannerHeight())
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
	rows := DrawBanner(screen, 0, 0, 50)
	if rows == 0 {
		t.Error("expected non-zero rows for width 50")
	}
}

func TestFadeDashes(t *testing.T) {
	d := FadeDashes(12, false)
	if len(d) != 12 {
		t.Errorf("FadeDashes(12) len = %d, want 12", len(d))
	}

	d = FadeDashes(0, false)
	if d != "" {
		t.Errorf("FadeDashes(0) should be empty")
	}
}
