package tui

import (
	"os"
	"testing"

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
