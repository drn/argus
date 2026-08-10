package tui

import (
	"os"
	"testing"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
)

// osMkdirAll is the underlying os.MkdirAll, exposed as a variable so tests
// can stub it (e.g. to simulate filesystem failures without breaking
// surrounding tests' real directory creation).
var osMkdirAll = os.MkdirAll

func mkdirAll(path string) error {
	return osMkdirAll(path, 0o755)
}

// drawSim returns a SimulationScreen sized to a default 80x24 terminal,
// with Init called and Fini registered as test cleanup. Use this for tests
// that just need a Draw target and don't care about size.
func drawSim(t *testing.T) tcell.SimulationScreen {
	t.Helper()
	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sim.Fini() })
	sim.SetSize(80, 24)
	return sim
}

// formAdvanceKey returns a key that triggers the focus-advance branch in
// form HandleKey paths without polluting any text field. KeyEnter on the
// last field (sandbox) sets done=true; on earlier fields it advances focus.
var formAdvanceKey = tcell.NewEventKey(tcell.KeyEnter, 0, 0)

// findScreenText scans sim's rendered cells for the first occurrence of
// needle (read left-to-right, top-to-bottom) and returns the (col, row) of
// its starting cell, or (-1, -1) when not found. Useful for asserting a
// render actually painted specific text (catching a silently-blank pane)
// and for reading back the tcell.Style of a known text run via
// Get(col, row).
func findScreenText(sim tcell.SimulationScreen, needle string) (int, int) {
	w, h := sim.Size()
	runes := []rune(needle)
	if len(runes) == 0 || len(runes) > w {
		return -1, -1
	}
	for row := 0; row < h; row++ {
		for col := 0; col <= w-len(runes); col++ {
			match := true
			for i, want := range runes {
				s, _, _ := sim.Get(col+i, row)
				r, _ := utf8.DecodeRuneInString(s)
				if r != want {
					match = false
					break
				}
			}
			if match {
				return col, row
			}
		}
	}
	return -1, -1
}
