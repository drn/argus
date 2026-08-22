package tui

import (
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

// altScreenAdapter is a minimal agentview.TerminalAdapter whose output drives the
// pane's emulator into alternate-screen mode (DECSET 1049) — a full-screen agent.
type altScreenAdapter struct{ out []byte }

func (a *altScreenAdapter) WriteInput(p []byte) (int, error)       { return len(p), nil }
func (a *altScreenAdapter) WriteInputSystem(p []byte) (int, error) { return len(p), nil }
func (a *altScreenAdapter) Resize(rows, cols uint16) error         { return nil }
func (a *altScreenAdapter) RecentOutput() []byte                   { return a.out }
func (a *altScreenAdapter) RecentOutputTail(n int) []byte {
	if n >= len(a.out) {
		return a.out
	}
	return a.out[len(a.out)-n:]
}
func (a *altScreenAdapter) RecentOutputTailWithTotal(n int) ([]byte, uint64) {
	return a.RecentOutputTail(n), uint64(len(a.out))
}
func (a *altScreenAdapter) TotalWritten() uint64 { return uint64(len(a.out)) }
func (a *altScreenAdapter) Alive() bool          { return true }
func (a *altScreenAdapter) PTYSize() (int, int)  { return 80, 24 }

// TestSmoke_AgentScrollSuppressedInAltScreen is the BUG-031 regression for the
// main agent view: Shift+Up / Shift+PgUp on a full-screen (alt-screen) agent must
// NOT enter argus's scroll mode (which replays in-place frames as garbage); it
// suppresses and surfaces the affordance via the status bar.
func TestSmoke_AgentScrollSuppressedInAltScreen(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)

	// Feed the pane an alt-screen frame, then Draw so the emulator absorbs it.
	app.agentPane.SetSession(&altScreenAdapter{out: []byte("\x1b[?1049h\x1b[2J\x1b[Hhello")})
	app.agentPane.SetRect(0, 0, 100, 30)
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	t.Cleanup(sim.Fini)
	sim.SetSize(100, 30)
	app.agentPane.Draw(sim)

	if !app.agentPane.InAltScreen() {
		t.Fatal("agent pane should be in alt-screen after feeding DECSET 1049")
	}

	// Shift+Up (scrollback key) → suppressed, status-bar affordance set.
	app.statusbar.ClearInfo()
	app.handleAgentKey(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModShift))
	testutil.Equal(t, app.agentPane.ScrollOffset(), 0)
	if app.statusbar.Info() == "" {
		t.Error("expected a status-bar affordance for alt-screen Shift+Up, got none")
	}

	// Shift+PgUp behaves identically.
	app.statusbar.ClearInfo()
	app.handleAgentKey(tcell.NewEventKey(tcell.KeyPgUp, 0, tcell.ModShift))
	testutil.Equal(t, app.agentPane.ScrollOffset(), 0)
	if app.statusbar.Info() == "" {
		t.Error("expected a status-bar affordance for alt-screen Shift+PgUp, got none")
	}
}
