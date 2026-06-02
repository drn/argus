// Package events is the daemon-wide event bus for the plugin substrate (PR 2
// of the plugin plan). Emission sites call Emit; the daemon installs a Sink
// at boot that persists each event to the ring buffer in internal/db and
// fans it out to SSE subscribers in internal/api.
//
// When no Sink is registered (test binaries, plugin-free builds), Emit is a
// no-op. This is intentional: every call site can fire unconditionally and
// the cost is one atomic load.
package events

import (
	"encoding/json"
	"sync/atomic"
	"time"

	"github.com/drn/argus/internal/model"
)

// Sink consumes an event from any emission site. The daemon's implementation
// (see internal/api) writes to the events ring and broadcasts to attached
// SSE clients. Tests install recording sinks; production-free builds leave
// the default nil sink in place.
//
// Emit is called from arbitrary goroutines, so implementations must be
// goroutine-safe and must not block on slow consumers — broadcast queues
// should use buffered channels with drop-on-full semantics.
type Sink interface {
	Emit(ev model.Event)
}

// current holds the registered sink. atomic.Pointer keeps SetSink lock-free
// against the read in Emit; reads dominate the access pattern (one load per
// emission) and writes happen exactly once per daemon boot.
var current atomic.Pointer[Sink]

// SetSink installs the global sink. Passing nil clears it so subsequent Emit
// calls become no-ops — used by tests that want to isolate themselves from
// the daemon's real bus, and by the daemon's shutdown path. Returns the
// previously installed sink so callers can save-and-restore around a
// localised override.
func SetSink(s Sink) Sink {
	var prev Sink
	if p := current.Load(); p != nil {
		prev = *p
	}
	if s == nil {
		current.Store(nil)
		return prev
	}
	current.Store(&s)
	return prev
}

// Emit constructs an Event and forwards it to the registered Sink. When no
// Sink is registered the call is a silent no-op.
//
// payload is JSON-marshaled at the call site. nil omits the field. A
// marshal failure (e.g. a channel value) is swallowed — the event still
// fires but with a nil payload. This is deliberate: an emission site does
// not want to know about a downstream encoding bug, and the alternative
// (panicking or returning an error) would push that error onto every caller
// for no actionable reason.
func Emit(typ, taskID string, payload any) {
	p := current.Load()
	if p == nil {
		return
	}
	sink := *p
	ev := model.Event{
		Type:   typ,
		At:     time.Now(),
		TaskID: taskID,
	}
	if payload != nil {
		if b, err := json.Marshal(payload); err == nil {
			ev.Payload = b
		}
	}
	sink.Emit(ev)
}
