package widget

import (
	"strings"
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

func TestDrawLaunchBanner_CustomSubtitle(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(80, 20)

	rows := DrawLaunchBanner(screen, 0, 0, 80, LaunchSubtitleStarting)
	testutil.Equal(t, rows, PendingBannerHeight())
	var sb strings.Builder
	for c := 0; c < 80; c++ {
		s, _, _ := screen.Get(c, 9) // subtitle row
		sb.WriteString(s)
	}
	testutil.Contains(t, sb.String(), "S T A R T I N G")
}
