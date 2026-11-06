package terminal

import (
	"fmt"
	"strings"
	"testing"

	xvt "github.com/charmbracelet/x/vt"
	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

func TestFirstCellText(t *testing.T) {
	for _, tc := range []struct{ content, want string }{
		{"", " "}, {"ASCII", "A"}, {"éx", "é"}, {"界x", "界"}, {"e\u0301", "e"}, {"👩‍💻", "👩"}, {"\xffx", "�"}, {"�x", "�"}, {"\x00x", "\x00"},
	} {
		t.Run(fmt.Sprintf("%q", tc.content), func(t *testing.T) { testutil.Equal(t, firstCellText(tc.content), tc.want) })
	}
}

func TestPaintCacheMatchesLegacyCells(t *testing.T) {
	// This fixture emits no terminal responses, so it needs no reader. The
	// upstream Close races with a concurrent drain reader; keep it local.
	emu := xvt.NewSafeEmulator(12, 3)
	t.Cleanup(func() { testutil.NoError(t, emu.Close()) })
	_, err := emu.Write([]byte("\x1b[31mAé界e\u0301\x1b[0m"))
	testutil.NoError(t, err)
	actual := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, actual.Init())
	defer actual.Fini()
	actual.SetSize(12, 3)
	expected := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, expected.Init())
	defer expected.Fini()
	expected.SetSize(12, 3)
	for y := 0; y < 3; y++ {
		for x := 0; x < 12; x++ {
			actual.SetContent(x, y, 'Z', nil, tcell.StyleDefault.Background(tcell.ColorBlue))
		}
	}
	// Reference the legacy first-rune SetContent path independently of the
	// new text helper, including wide-glyph continuation and blank cells.
	for y := 0; y < 3; y++ {
		for x := 0; x < 12; x++ {
			r := ' '
			style := tcell.StyleDefault
			if y == 0 {
				if cell := emu.CellAt(x, y); cell != nil {
					if runes := []rune(cell.Content); len(runes) > 0 {
						r = runes[0]
					}
					style = UvCellToTcellStyle(cell)
				}
			}
			expected.SetContent(x, y, r, nil, style)
		}
	}
	tp := NewTerminalPane()
	tp.paintEmu(actual, 0, 0, 12, 3, emu, 12, 3, false, false, false)
	compare := func() {
		for y := 0; y < 3; y++ {
			for x := 0; x < 12; x++ {
				gs, gstyle, gw := actual.Get(x, y)
				ws, wstyle, ww := expected.Get(x, y)
				testutil.Equal(t, gs, ws)
				testutil.Equal(t, gstyle, wstyle)
				testutil.Equal(t, gw, ww)
			}
		}
	}
	compare()
	_, err = emu.Write([]byte("\rZ"))
	testutil.NoError(t, err)
	actual.Clear()
	tp.replayPaintCache(actual)
	compare() // immutable cached text, not live emu pointers
}

func TestPaintCacheAllocations(t *testing.T) {
	s := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, s.Init())
	defer s.Fini()
	s.SetSize(88, 48)
	tp := NewTerminalPane()
	tp.paintCacheCells = make([]cachedCell, 88*48)
	for n := range tp.paintCacheCells {
		tp.paintCacheCells[n] = cachedCell{x: n % 88, y: n / 88, text: "A", style: tcell.StyleDefault}
	}
	tp.replayPaintCache(s)
	allocs := testing.AllocsPerRun(5, func() { tp.replayPaintCache(s) })
	testutil.Equal(t, allocs <= 1, true)
}

func BenchmarkTerminalCellPainting(b *testing.B) {
	for _, mode := range []string{"fresh", "cache"} {
		b.Run(mode, func(b *testing.B) {
			s := tcell.NewSimulationScreen("UTF-8")
			if err := s.Init(); err != nil {
				b.Fatal(err)
			}
			defer s.Fini()
			s.SetSize(88, 48)
			emu := xvt.NewSafeEmulator(88, 48)
			b.Cleanup(func() { testutil.NoError(b, emu.Close()) })
			_, err := emu.Write([]byte(strings.Repeat("\x1b[32mASCII text é界\x1b[0m\r\n", 48)))
			if err != nil {
				b.Fatal(err)
			}
			tp := NewTerminalPane()
			tp.paintEmu(s, 0, 0, 88, 48, emu, 88, 48, false, false, false)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if mode == "cache" {
					tp.replayPaintCache(s)
				} else {
					tp.paintEmu(s, 0, 0, 88, 48, emu, 88, 48, false, false, false)
				}
			}
		})
	}
}
