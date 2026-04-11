package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/testutil"
)

func TestPendingBannerHeight(t *testing.T) {
	h := pendingBannerHeight()
	testutil.Equal(t, h, 12)
}

func TestDrawPendingBanner_ZeroWidth(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()

	rows := drawPendingBanner(screen, 0, 0, 0)
	testutil.Equal(t, rows, 0)
}

func TestDrawPendingBanner_Normal(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(120, 40)

	rows := drawPendingBanner(screen, 0, 0, 120)
	testutil.Equal(t, rows, pendingBannerHeight())
}

func TestDrawPendingBanner_NarrowWidth(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(50, 40)

	// Should not panic even with narrow width.
	rows := drawPendingBanner(screen, 0, 0, 50)
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

func TestTerminalPane_PendingState(t *testing.T) {
	tp := NewTerminalPane()

	// Initially not pending.
	tp.mu.Lock()
	testutil.Equal(t, tp.pending, false)
	tp.mu.Unlock()

	// Set pending.
	tp.SetPending(true)
	tp.mu.Lock()
	testutil.Equal(t, tp.pending, true)
	tp.mu.Unlock()

	// SetPending(false) clears it explicitly.
	tp.SetPending(false)
	tp.mu.Lock()
	testutil.Equal(t, tp.pending, false)
	tp.mu.Unlock()

	// Pending is cleared when a real session is set.
	tp.SetPending(true)
	mock := &mockAdapter{alive: true, totalWritten: 100, output: make([]byte, 100)}
	tp.SetSession(mock)
	tp.mu.Lock()
	testutil.Equal(t, tp.pending, false)
	tp.mu.Unlock()
}
