package notify

import "sync"

// FocusTracker is a concurrency-safe registry of which task pane a human is
// currently focused on. It is updated by the TUI on mode transitions and
// queried by the Notifier before any auto-submit.
type FocusTracker struct {
	mu      sync.Mutex
	focused map[string]bool
	// onTransition is called (outside the lock) when state changes from false→true
	// or true→false. Used to emit session.focus events. May be nil.
	onTransition func(taskID string, focused bool)
}

// NewFocusTracker creates an empty FocusTracker with an optional transition
// callback. Pass nil for cb if events are not needed.
func NewFocusTracker(cb func(taskID string, focused bool)) *FocusTracker {
	return &FocusTracker{
		focused:      make(map[string]bool),
		onTransition: cb,
	}
}

// SetFocused registers or clears focus for taskID. If the new state differs
// from the prior state, the transition callback fires (if set).
func (f *FocusTracker) SetFocused(taskID string, focused bool) {
	f.mu.Lock()
	prior := f.focused[taskID]
	if focused {
		f.focused[taskID] = true
	} else {
		delete(f.focused, taskID)
	}
	changed := prior != focused
	cb := f.onTransition
	f.mu.Unlock()

	if changed && cb != nil {
		cb(taskID, focused)
	}
}

// IsFocused returns true if the taskID is currently registered as focused.
func (f *FocusTracker) IsFocused(taskID string) bool {
	f.mu.Lock()
	v := f.focused[taskID]
	f.mu.Unlock()
	return v
}
