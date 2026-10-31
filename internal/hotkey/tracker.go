package hotkey

import (
	"sync"
	"time"
)

// Event is a hold-to-talk transition reported by a Tracker.
type Event int

const (
	// Began fires once the combo has been held alone for ArmDelay.
	Began Event = iota + 1
	// Ended fires when the combo is released after Began.
	Ended
	// Cancelled fires when a key or extra modifier is pressed while armed,
	// i.e. the combo was used as part of an ordinary shortcut.
	Cancelled
)

func (e Event) String() string {
	switch e {
	case Began:
		return "began"
	case Ended:
		return "ended"
	case Cancelled:
		return "cancelled"
	}
	return "unknown"
}

// DefaultArmDelay is how long the combo must be held alone before Began.
const DefaultArmDelay = 200 * time.Millisecond

type trackerState int

const (
	stateIdle trackerState = iota
	stateArmed
	stateActive
	// stateBlocked waits for the combo to be fully released after a cancel or
	// an end-with-extra-modifier, so it cannot re-arm mid-shortcut.
	stateBlocked
)

// Tracker is the pure hold-to-talk state machine. It is safe for concurrent
// use; onEvent must not call back into the Tracker's input methods.
type Tracker struct {
	combo    Combo
	armDelay time.Duration
	onEvent  func(Event)

	// inputMu serializes compute+emit so events from concurrent callers
	// (tap thread vs ticker) are delivered in state-transition order.
	inputMu sync.Mutex
	mu      sync.Mutex
	state   trackerState
	armedAt time.Time
}

// NewTracker returns a Tracker for combo using DefaultArmDelay. A nil onEvent
// discards events.
func NewTracker(combo Combo, onEvent func(Event)) *Tracker {
	return &Tracker{combo: combo, armDelay: DefaultArmDelay, onEvent: onEvent}
}

// SetArmDelay overrides the arm delay.
func (t *Tracker) SetArmDelay(d time.Duration) {
	t.mu.Lock()
	t.armDelay = d
	t.mu.Unlock()
}

// Armed reports whether the combo is held and waiting out the arm delay.
func (t *Tracker) Armed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state == stateArmed
}

// Active reports whether a hold is in progress (after Began, before Ended).
func (t *Tracker) Active() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state == stateActive
}

// FlagsChanged feeds a modifier-state change.
func (t *Tracker) FlagsChanged(flags uint64, now time.Time) {
	t.inputMu.Lock()
	defer t.inputMu.Unlock()
	t.mu.Lock()
	evs := t.promoteLocked(now, nil)
	switch t.state {
	case stateIdle:
		switch {
		case t.combo.Matches(flags):
			t.state = stateArmed
			t.armedAt = now
		case t.combo.Held(flags):
			t.state = stateBlocked
		}
	case stateArmed:
		switch {
		case t.combo.Matches(flags):
		case t.combo.Held(flags):
			t.state = stateBlocked
			evs = append(evs, Cancelled)
		default:
			t.state = stateIdle
		}
	case stateActive:
		switch {
		case t.combo.Matches(flags):
		case t.combo.Held(flags):
			t.state = stateBlocked
			evs = append(evs, Ended)
		default:
			t.state = stateIdle
			evs = append(evs, Ended)
		}
	case stateBlocked:
		if !t.combo.Held(flags) {
			t.state = stateIdle
		}
	}
	t.mu.Unlock()
	t.emit(evs)
}

// KeyDown feeds a non-modifier key press.
func (t *Tracker) KeyDown(now time.Time) {
	t.inputMu.Lock()
	defer t.inputMu.Unlock()
	t.mu.Lock()
	evs := t.promoteLocked(now, nil)
	if t.state == stateArmed {
		t.state = stateBlocked
		evs = append(evs, Cancelled)
	}
	t.mu.Unlock()
	t.emit(evs)
}

// Tick promotes an armed combo to Began once the arm delay has elapsed.
func (t *Tracker) Tick(now time.Time) {
	t.inputMu.Lock()
	defer t.inputMu.Unlock()
	t.mu.Lock()
	evs := t.promoteLocked(now, nil)
	t.mu.Unlock()
	t.emit(evs)
}

// promoteLocked fires a pending Began before any later input is applied, so
// event order does not depend on Tick cadence.
func (t *Tracker) promoteLocked(now time.Time, evs []Event) []Event {
	if t.state == stateArmed && now.Sub(t.armedAt) >= t.armDelay {
		t.state = stateActive
		evs = append(evs, Began)
	}
	return evs
}

func (t *Tracker) emit(evs []Event) {
	if t.onEvent == nil {
		return
	}
	for _, e := range evs {
		t.onEvent(e)
	}
}
