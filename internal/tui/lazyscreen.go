package tui

import "github.com/gdamore/tcell/v2"

// lazyScreen wraps a tcell.Screen. It exists as a passthrough wrapper so
// `app.screen` has a stable type the rest of the package can pin to (and
// so that smoke tests can inject a SimulationScreen through the same
// indirection production uses).
//
// A prior incarnation of this wrapper skipped tview's screen-wide Clear()
// for PTY-forwarded keystrokes, shaving ~10K cell writes per keystroke.
// That optimization was removed because it leaked stale cells whenever the
// layout shifted between frames (resize, page swap, agent-header toggle),
// producing ghost status bars and persistent render tearing. tcell's Show()
// diffs cells against their last-emitted state, so a full Clear followed
// by widgets restoring the same content emits nothing to the terminal —
// the optimization was saving in-process cell writes, not terminal I/O.
//
// The PollEvent override exists to recover one drift scenario the
// OnBranchChange callback set cannot cover: a tmux pane regaining focus
// after the user switched away. tmux may have repainted the pane from a
// stale backing store while we were unfocused, but no layout shift
// happened on our side — so no branch-change callback fires and afterDraw
// has no reason to Sync. Intercepting tcell's *EventFocus and calling
// onFocusGained lets the App set pendingSync, so the next draw (≤1s via
// the tick loop) Syncs and clears the drift. The event itself is still
// returned for tview to handle (tview ignores focus events but is given
// the chance to evolve).
type lazyScreen struct {
	tcell.Screen
	// onFocusGained, if set, is called whenever a *tcell.EventFocus with
	// Focused=true is observed in PollEvent. The App wires this to
	// forceRedraw so multiplexer drift accumulated while unfocused gets
	// cleared on the next draw cycle.
	onFocusGained func()
}

func (l *lazyScreen) PollEvent() tcell.Event {
	ev := l.Screen.PollEvent()
	if fev, ok := ev.(*tcell.EventFocus); ok && fev.Focused && l.onFocusGained != nil {
		l.onFocusGained()
	}
	return ev
}
