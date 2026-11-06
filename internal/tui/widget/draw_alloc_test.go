package widget

import (
	"fmt"
	"testing"

	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

func TestFillAreaMatchesLegacy(t *testing.T) {
	for _, r := range []rune{' ', 'A', 'é', '界', '\u0301', 0, -1} {
		t.Run(fmt.Sprint(r), func(t *testing.T) {
			actual := newSimScreen(t, 12, 8)
			expected := newSimScreen(t, 12, 8)
			style := tcell.StyleDefault.Foreground(tcell.ColorGreen).Bold(true)
			FillArea(actual, -1, 2, 7, 3, r, style)
			for y := 2; y < 5; y++ {
				for x := -1; x < 6; x++ {
					expected.SetContent(x, y, r, nil, style)
				}
			}
			for y := 0; y < 8; y++ {
				for x := 0; x < 12; x++ {
					gs, gstyle, gw := actual.Get(x, y)
					ws, wstyle, ww := expected.Get(x, y)
					testutil.Equal(t, gs, ws)
					testutil.Equal(t, gstyle, wstyle)
					testutil.Equal(t, gw, ww)
				}
			}
		})
	}
}

func TestFillAreaAllocations(t *testing.T) {
	s := newSimScreen(t, 88, 48)
	allocs := testing.AllocsPerRun(5, func() { FillArea(s, 0, 0, 88, 48, ' ', tcell.StyleDefault) })
	testutil.Equal(t, allocs <= 1, true)
}

func BenchmarkFillArea(b *testing.B) {
	s := tcell.NewSimulationScreen("UTF-8")
	if err := s.Init(); err != nil {
		b.Fatal(err)
	}
	defer s.Fini()
	s.SetSize(88, 48)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		FillArea(s, 0, 0, 88, 48, ' ', tcell.StyleDefault)
	}
}
