package tui

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

func TestTodoBannerHeight(t *testing.T) {
	h := todoBannerHeight()
	testutil.Equal(t, h, 12)
}

func TestDrawTodoBanner_ZeroWidth(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()

	rows := drawTodoBanner(screen, 0, 0, 0)
	testutil.Equal(t, rows, 0)
}

func TestDrawTodoBanner_Normal(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(120, 40)

	rows := drawTodoBanner(screen, 0, 0, 120)
	testutil.Equal(t, rows, todoBannerHeight())
}

func TestDrawTodoBanner_NarrowWidth(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(30, 40)

	// Should not panic even with narrow width.
	rows := drawTodoBanner(screen, 0, 0, 30)
	if rows == 0 {
		t.Error("expected non-zero rows for width 30")
	}
}

func TestTodoBannerLineWidths(t *testing.T) {
	// All lines must have exactly todoBannerTextWidth runes so centering
	// math in drawTodoBanner produces consistent padding.
	for i, line := range todoBannerLines {
		got := len([]rune(line))
		testutil.Equal(t, got, todoBannerTextWidth)
		_ = i
	}
}
