package tui

import "github.com/gdamore/tcell/v2"

// lazyScreen wraps a tcell.Screen. It exists as a passthrough wrapper so
// `app.screen` has a stable type the rest of the package can pin to (and
// so that smoke tests can inject a SimulationScreen through the same
// indirection production uses).
//
// A prior incarnation of this wrapper had a `skipClear` field that
// bypassed tview's screen-wide Clear() for PTY-forwarded keystrokes —
// removed in commit `e516ad33`. **Do NOT re-introduce skipClear or any
// equivalent.** It silently set off a 12-commit tearing-fix cycle
// (Mar 22 – May 12, 2026) the codebase only just dug itself out of —
// see `context/knowledge/gotchas/ui-threading.md` for the post-mortem.
//
// The PollEvent override exists for one specific case: tmux pane focus
// regain after a window switch. The multiplexer may have repainted our
// pane from a stale backing store while we were unfocused. When the
// EventFocus(true) event arrives, we invoke onFocusGained (which the App
// wires to a direct screen.Sync()) to repair any drift. One CSI 2J flash
// on a rare event is the right tradeoff for guaranteed correctness. The
// event is still returned for tview to handle (tview ignores it today).
type lazyScreen struct {
	tcell.Screen
	// onFocusGained, if set, is called whenever a *tcell.EventFocus with
	// Focused=true is observed in PollEvent. The App wires this to call
	// screen.Sync() directly — repair-screen-damage per tcell's intended
	// use of Sync (see gdamore/tcell issue #647).
	onFocusGained func()
}

func (l *lazyScreen) PollEvent() tcell.Event {
	ev := l.Screen.PollEvent()
	if fev, ok := ev.(*tcell.EventFocus); ok && fev.Focused && l.onFocusGained != nil {
		l.onFocusGained()
	}
	return ev
}
