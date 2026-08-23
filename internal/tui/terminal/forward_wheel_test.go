package terminal

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/app/agentview"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// recAdapter records WriteInput so tests can assert what the pane forwarded to
// the agent, and replays a fixed output stream for the emulator to build from.
type recAdapter struct {
	alive  bool
	output []byte
	wrote  []byte
}

func (a *recAdapter) WriteInput(p []byte, origin agentview.InputOrigin) (int, error) {
	a.wrote = append(a.wrote, p...)
	return len(p), nil
}
func (a *recAdapter) Resize(rows, cols uint16) error { return nil }
func (a *recAdapter) RecentOutput() []byte           { return a.output }
func (a *recAdapter) RecentOutputTail(n int) []byte {
	if n >= len(a.output) {
		return a.output
	}
	return a.output[len(a.output)-n:]
}
func (a *recAdapter) RecentOutputTailWithTotal(n int) ([]byte, uint64) {
	return a.RecentOutputTail(n), uint64(len(a.output))
}
func (a *recAdapter) TotalWritten() uint64 { return uint64(len(a.output)) }
func (a *recAdapter) Alive() bool          { return a.alive }
func (a *recAdapter) PTYSize() (int, int)  { return 80, 24 }

func drawnWheelPane(t *testing.T, output []byte) (*TerminalPane, *recAdapter) {
	t.Helper()
	sess := &recAdapter{alive: true, output: output}
	tp := NewTerminalPane()
	tp.SetTaskID("")
	tp.SetSession(sess)
	tp.SetFocused(true)
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(screen.Fini)
	screen.SetSize(80, 24)
	tp.SetRect(0, 0, 80, 24)
	tp.Draw(screen) // build the live emulator from the output stream
	return tp, sess
}

// BUG-026: when the live agent has grabbed the screen as a full-screen TUI
// (alternate screen — the signal it wants the wheel itself, like a real
// terminal hands the wheel to the foreground app), a mouse wheel-up must be
// FORWARDED to the agent as SGR mouse bytes, NOT consumed for the pane's own
// (usually empty) terminal scrollback. Otherwise an agent that redraws in place
// produces no scrollback and wheel-up does nothing.
func TestTerminalPane_WheelForwardsToAltScreenAgent(t *testing.T) {
	// Enter alt-screen + SGR mouse reporting, then draw a screenful in place.
	out := []byte("\x1b[?1049h\x1b[?1006h\x1b[?1000h\x1b[Hhello\r\nworld\r\n")
	tp, sess := drawnWheelPane(t, out)
	if !tp.emu.IsAltScreen() {
		t.Fatal("setup: emulator should be in alt-screen after ESC[?1049h")
	}

	h := tp.MouseHandler()
	consumed, _ := h(tview.MouseScrollUp, tcell.NewEventMouse(10, 5, tcell.Button1, tcell.ModNone), func(tview.Primitive) {})
	if !consumed {
		t.Fatal("wheel-up should be consumed")
	}
	if tp.ScrollOffset() != 0 {
		t.Fatalf("scrollOffset should stay 0 (wheel forwarded to agent, pane not scrolled), got %d", tp.ScrollOffset())
	}
	if !strings.Contains(string(sess.wrote), "\x1b[<64;") {
		t.Fatalf("expected SGR wheel-up (ESC[<64;...M) forwarded to agent, got %q", sess.wrote)
	}

	// Wheel-down forwards button 65.
	sess.wrote = nil
	h(tview.MouseScrollDown, tcell.NewEventMouse(10, 5, tcell.Button1, tcell.ModNone), func(tview.Primitive) {})
	if !strings.Contains(string(sess.wrote), "\x1b[<65;") {
		t.Fatalf("expected SGR wheel-down (ESC[<65;...M) forwarded to agent, got %q", sess.wrote)
	}
}

// When the agent is NOT a full-screen app (main screen, no mouse grab), the
// wheel keeps scrolling the pane's own terminal scrollback (unchanged behavior)
// and nothing is forwarded to the agent.
func TestTerminalPane_WheelScrollsScrollbackWhenNotAltScreen(t *testing.T) {
	tp, sess := drawnWheelPane(t, []byte("just some plain output\r\n"))
	if tp.emu.IsAltScreen() {
		t.Fatal("setup: emulator should NOT be in alt-screen")
	}

	h := tp.MouseHandler()
	h(tview.MouseScrollUp, tcell.NewEventMouse(10, 5, tcell.Button1, tcell.ModNone), func(tview.Primitive) {})
	if tp.ScrollOffset() == 0 {
		t.Fatal("non-alt-screen wheel-up should scroll the pane's terminal scrollback (scrollOffset > 0)")
	}
	if len(sess.wrote) != 0 {
		t.Fatalf("non-alt-screen wheel must NOT forward to the agent, got %q", sess.wrote)
	}
}
