package widget

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

// newSimScreen returns a SimulationScreen sized for tests. The screen is
// pre-seeded with garbage so tests can verify FillArea / DrawBorderedPanel
// actually overwrite stale cells.
func newSimScreen(t *testing.T, cols, rows int) tcell.SimulationScreen {
	t.Helper()
	s := tcell.NewSimulationScreen("UTF-8")
	if err := s.Init(); err != nil {
		t.Fatalf("screen init: %v", err)
	}
	s.SetSize(cols, rows)
	// Seed every cell with a visible glyph so missing writes are detectable.
	garbageStyle := tcell.StyleDefault.Foreground(tcell.ColorRed)
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			s.SetContent(x, y, 'X', nil, garbageStyle)
		}
	}
	// Sync commits the seeded cells so GetContent reads them back. Without
	// this, GetContent would return the emulator's zero-value initial state
	// rather than the seed, and the "overwrite" assertions below would pass
	// trivially even if FillArea silently did nothing.
	s.Sync()
	t.Cleanup(s.Fini)
	return s
}

func TestFillArea_OverwritesSeededCells(t *testing.T) {
	s := newSimScreen(t, 20, 10)

	FillArea(s, 2, 3, 5, 4, ' ', tcell.StyleDefault)

	// Cells inside the rect must be blank.
	for y := 3; y < 7; y++ {
		for x := 2; x < 7; x++ {
			r, _, _, _ := s.GetContent(x, y)
			if r != ' ' {
				t.Errorf("cell (%d,%d) = %q, want space", x, y, r)
			}
		}
	}

	// Cells outside must still carry the seed.
	for _, p := range [][2]int{{0, 0}, {1, 3}, {7, 3}, {2, 2}, {2, 7}, {19, 9}} {
		r, _, _, _ := s.GetContent(p[0], p[1])
		if r != 'X' {
			t.Errorf("outside cell (%d,%d) should keep seed, got %q", p[0], p[1], r)
		}
	}
}

func TestFillArea_NoopOnZeroOrNegativeSize(t *testing.T) {
	s := newSimScreen(t, 10, 5)
	FillArea(s, 1, 1, 0, 3, ' ', tcell.StyleDefault)
	FillArea(s, 1, 1, 3, 0, ' ', tcell.StyleDefault)
	FillArea(s, 1, 1, -2, 3, ' ', tcell.StyleDefault)
	FillArea(s, 1, 1, 3, -2, ' ', tcell.StyleDefault)
	// All cells should still be seeded.
	for y := 0; y < 5; y++ {
		for x := 0; x < 10; x++ {
			r, _, _, _ := s.GetContent(x, y)
			if r != 'X' {
				t.Errorf("cell (%d,%d) = %q, want unchanged seed X", x, y, r)
			}
		}
	}
}

func TestDrawBorderedPanel_ClearsInteriorOfStaleCells(t *testing.T) {
	s := newSimScreen(t, 20, 10)

	inner := DrawBorderedPanel(s, 2, 1, 10, 6, "Hi", tcell.StyleDefault)

	// Interior cells must be blanked, not left as seeded 'X'.
	for y := inner.Y; y < inner.Y+inner.H; y++ {
		for x := inner.X; x < inner.X+inner.W; x++ {
			r, _, _, _ := s.GetContent(x, y)
			if r != ' ' {
				t.Errorf("interior cell (%d,%d) = %q, want blank", x, y, r)
			}
		}
	}

	// Border corners must be drawn.
	corners := map[[2]int]rune{
		{2, 1}:  '╭',
		{11, 1}: '╮',
		{2, 6}:  '╰',
		{11, 6}: '╯',
	}
	for pos, want := range corners {
		r, _, _, _ := s.GetContent(pos[0], pos[1])
		if r != want {
			t.Errorf("corner (%d,%d) = %q, want %q", pos[0], pos[1], r, want)
		}
	}

	// Title must be painted just after the top-left corner.
	r, _, _, _ := s.GetContent(3, 1)
	if r != 'H' {
		t.Errorf("title first rune = %q, want H", r)
	}
	r, _, _, _ = s.GetContent(4, 1)
	if r != 'i' {
		t.Errorf("title second rune = %q, want i", r)
	}

	// Returned inner rect must be consistent.
	if inner.X != 3 || inner.Y != 2 || inner.W != 8 || inner.H != 4 {
		t.Errorf("inner rect = %+v, want {X:3 Y:2 W:8 H:4}", inner)
	}
}

func TestDrawBorderedPanel_TinyPanelReturnsZeroInnerRect(t *testing.T) {
	s := newSimScreen(t, 10, 5)
	// 1x1 — too small for a border, must no-op safely and return zero rect
	// so callers can bail out on `inner.W <= 0 || inner.H <= 0`.
	got := DrawBorderedPanel(s, 0, 0, 1, 1, "", tcell.StyleDefault)
	if got != (InnerRect{}) {
		t.Errorf("tiny panel inner rect = %+v, want zero value", got)
	}
	// Seeded cell must be untouched — no border, no fill, no partial writes.
	r, _, _, _ := s.GetContent(0, 0)
	if r != 'X' {
		t.Errorf("tiny panel should not paint; got %q, want X", r)
	}
}
