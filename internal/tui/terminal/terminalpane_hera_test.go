package terminal

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

// topBorderText returns the runes painted on the pane's top border row, where
// DrawBorderedPanel centers the title.
func topBorderText(t *testing.T, tp *TerminalPane, w, h int) string {
	t.Helper()
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	t.Cleanup(sim.Fini)
	sim.SetSize(w, h)
	tp.SetRect(0, 0, w, h)
	tp.Draw(sim)
	sim.Show() // flush SetContent writes into the buffer GetContents reads
	cells, _, _ := sim.GetContents()
	var b strings.Builder
	for x := 0; x < w; x++ {
		c := cells[x]
		if len(c.Runes) > 0 {
			b.WriteRune(c.Runes[0])
		}
	}
	return b.String()
}

// TestTerminalPane_BorderTitle proves the configurable title: the default reads
// "Agent", and SetBorderTitle swaps it (used by the native Hera coordinator
// pane). This is the only terminalpane change M6b makes to the agent surface.
func TestTerminalPane_BorderTitle(t *testing.T) {
	def := NewTerminalPane()
	testutil.Contains(t, topBorderText(t, def, 40, 10), "Agent")

	custom := NewTerminalPane()
	custom.SetBorderTitle(" Coordinator ")
	got := topBorderText(t, custom, 40, 10)
	testutil.Contains(t, got, "Coordinator")
	if strings.Contains(got, "Agent") {
		t.Fatalf("custom-titled pane still shows Agent: %q", got)
	}
}

// TestTerminalPane_SizeSeedAlignment is the M6b size-alignment invariant: two
// TerminalPanes given the SAME outer rect compute the SAME PTY resize. A native
// Hera pane and the main agent view are both TerminalPanes, so feeding the same
// task into either at the same dimensions resizes the PTY identically — there
// is no size delta to trigger a SIGWINCH-induced agent repaint (CLAUDE.md rule
// 5). Seed from an unset rect (SetSession before SetRect) so Draw posts a real
// resize rather than matching the seed.
func TestTerminalPane_SizeSeedAlignment(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(120, 40)

	mk := func() *resizeRecorder {
		tp := NewTerminalPane()
		rec := &resizeRecorder{mockAdapter: mockAdapter{alive: true}}
		tp.SetSession(rec) // rect still unset → seed stays zero
		tp.SetRect(0, 0, 100, 36)
		tp.Draw(sim)     // computes pendingResize from the rect
		tp.SyncPTYSize() // applies it to the recorder
		return rec
	}
	a := mk()
	b := mk()
	testutil.Equal(t, a.resizes >= 1, true)
	testutil.Equal(t, a.resizeRows, b.resizeRows)
	testutil.Equal(t, a.resizeCols, b.resizeCols)
}
