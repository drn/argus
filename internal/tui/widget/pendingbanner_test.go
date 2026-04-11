package widget

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/testutil"
)

func TestPendingBannerHeight(t *testing.T) {
	h := PendingBannerHeight()
	testutil.Equal(t, h, 12)
}

func TestDrawPendingBanner_ZeroWidth(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()

	rows := DrawPendingBanner(screen, 0, 0, 0)
	testutil.Equal(t, rows, 0)
}

func TestDrawPendingBanner_Normal(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(120, 40)

	rows := DrawPendingBanner(screen, 0, 0, 120)
	testutil.Equal(t, rows, PendingBannerHeight())
}

func TestDrawPendingBanner_NarrowWidth(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(50, 40)

	// Should not panic even with narrow width.
	rows := DrawPendingBanner(screen, 0, 0, 50)
	if rows == 0 {
		t.Error("expected non-zero rows for width 50")
	}
}

func TestPendingBannerLineWidths(t *testing.T) {
	// All lines should have the same rune width for proper alignment.
	for i, line := range pendingBannerLines {
		runes := []rune(line)
		if len(runes) != pendingBannerTextWidth {
			t.Errorf("line %d has %d runes, want %d", i, len(runes), pendingBannerTextWidth)
		}
	}
}
