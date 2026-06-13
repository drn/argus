package hera

import (
	"sync"
	"time"
)

// DefaultRefreshDebounce coalesces refresh requests into at most one rebuild
// per window. Mirrors Hera's DefaultRailDebounce (100ms).
const DefaultRefreshDebounce = 100 * time.Millisecond

// Refresher coalesces rail-rebuild requests into at most one rebuild per
// debounce window. It is the Argus-native analog of Hera's RailRefresher, but
// goroutine- and timer-free: Schedule() is driven by the existing app tick
// (and tab-entry), so the rebuild runs on the tview thread with no background
// goroutine to race the UI. This keeps it deterministic to unit-test with an
// injected clock.
//
// Contract: Schedule() and Flush() must be called on the tview thread (they
// invoke the rebuild callback, which touches widget state). The internal mutex
// guards the pending/lastRun bookkeeping only — the rebuild runs outside it.
type Refresher struct {
	debounce time.Duration
	rebuild  func()

	mu      sync.Mutex
	now     func() time.Time
	pending bool
	lastRun time.Time
	hasRun  bool
}

// NewRefresher builds a refresher that runs rebuild at most once per debounce
// window. A non-positive debounce defaults to DefaultRefreshDebounce.
func NewRefresher(debounce time.Duration, rebuild func()) *Refresher {
	if debounce <= 0 {
		debounce = DefaultRefreshDebounce
	}
	return &Refresher{debounce: debounce, rebuild: rebuild, now: time.Now}
}

// SetNow overrides the clock (test seam).
func (r *Refresher) SetNow(fn func() time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.now = fn
}

// Schedule requests a rebuild. It fires immediately when at least one debounce
// window has elapsed since the last rebuild (or this is the first request);
// otherwise the request is held pending and a later Schedule past the window
// fires it. Returns true if it rebuilt this call. Bursts within one window
// collapse to a single rebuild.
func (r *Refresher) Schedule() bool {
	r.mu.Lock()
	r.pending = true
	due := r.dueLocked()
	if due {
		r.markRunLocked()
	}
	r.mu.Unlock()
	if due {
		r.rebuild()
	}
	return due
}

// Flush forces a rebuild now if one is pending, ignoring the debounce window.
// Used on tab entry so the rail is fresh the instant the Hera tab opens.
// Returns true if it rebuilt.
func (r *Refresher) Flush() bool {
	r.mu.Lock()
	if !r.pending {
		r.mu.Unlock()
		return false
	}
	r.markRunLocked()
	r.mu.Unlock()
	r.rebuild()
	return true
}

// dueLocked reports whether the debounce window has elapsed (caller holds mu).
func (r *Refresher) dueLocked() bool {
	if !r.hasRun {
		return true
	}
	return r.now().Sub(r.lastRun) >= r.debounce
}

func (r *Refresher) markRunLocked() {
	r.pending = false
	r.lastRun = r.now()
	r.hasRun = true
}
