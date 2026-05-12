package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/testutil"
)

func TestLazyScreen_ClearDelegates(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	sim.Init()
	defer sim.Fini()
	sim.SetSize(10, 5)

	ls := &lazyScreen{Screen: sim}

	// Write a character, then Clear — must erase it. lazyScreen is a pure
	// passthrough; any skip behavior would re-introduce the ghosting bug.
	sim.SetContent(0, 0, 'X', nil, tcell.StyleDefault)
	ls.Clear()
	str, _, _ := sim.Get(0, 0)
	testutil.Equal(t, str, " ")
}

// pollEventScreen is a tcell.Screen stub that returns a fixed event from
// PollEvent. Used to drive lazyScreen.PollEvent through specific event types
// without spinning up a SimulationScreen and replaying real input bytes.
type pollEventScreen struct {
	tcell.Screen
	ev tcell.Event
}

func (p *pollEventScreen) PollEvent() tcell.Event { return p.ev }

// TestLazyScreen_FocusGainedFiresCallback pins the focus-regain recovery
// path: when tcell emits a *EventFocus with Focused=true (sent by tmux /
// iTerm2 via DECSET 1004 on pane/window regain), lazyScreen.PollEvent must
// invoke onFocusGained. The App wires that to forceRedraw, recovering from
// multiplexer drift accumulated while unfocused that the OnBranchChange
// callback set cannot cover (no layout shift happened on our side).
func TestLazyScreen_FocusGainedFiresCallback(t *testing.T) {
	ls := &lazyScreen{Screen: &pollEventScreen{ev: tcell.NewEventFocus(true)}}
	called := 0
	ls.onFocusGained = func() { called++ }
	got := ls.PollEvent()
	testutil.Equal(t, called, 1)
	// The focus event itself must still be returned — lazyScreen doesn't
	// swallow events, it only observes them. tview ignores focus events
	// today but is given the chance to evolve.
	if _, ok := got.(*tcell.EventFocus); !ok {
		t.Fatalf("expected *EventFocus passed through, got %T", got)
	}
}

// TestLazyScreen_FocusLostDoesNotFireCallback ensures only the regain edge
// triggers the callback. A focus-lost event from the multiplexer means we
// have nothing to clear — by definition no one is looking at our pane —
// so firing forceRedraw here would be wasted work.
func TestLazyScreen_FocusLostDoesNotFireCallback(t *testing.T) {
	ls := &lazyScreen{Screen: &pollEventScreen{ev: tcell.NewEventFocus(false)}}
	called := 0
	ls.onFocusGained = func() { called++ }
	ls.PollEvent()
	testutil.Equal(t, called, 0)
}

// TestLazyScreen_NonFocusEventPassesThrough guards against the focus-event
// interception leaking into any other event type. Key events, paste events,
// resize events — all must traverse PollEvent untouched.
func TestLazyScreen_NonFocusEventPassesThrough(t *testing.T) {
	keyEv := tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone)
	ls := &lazyScreen{Screen: &pollEventScreen{ev: keyEv}}
	called := 0
	ls.onFocusGained = func() { called++ }
	got := ls.PollEvent()
	testutil.Equal(t, called, 0)
	if got != keyEv {
		t.Fatalf("expected key event passed through, got %v", got)
	}
}

// TestLazyScreen_FocusGainedWithNilCallbackDoesNotPanic guards the
// nil-callback path. lazyScreen must be safe to use without onFocusGained
// wired — production wires it in Run() but tests and the smoke harness
// may construct lazyScreen without setting the callback.
func TestLazyScreen_FocusGainedWithNilCallbackDoesNotPanic(t *testing.T) {
	ls := &lazyScreen{Screen: &pollEventScreen{ev: tcell.NewEventFocus(true)}}
	// onFocusGained left nil.
	ls.PollEvent() // must not panic
}
