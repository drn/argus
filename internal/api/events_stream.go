package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/drn/argus/internal/model"
)

// eventBus broadcasts model.Events to attached SSE subscribers. Each
// subscriber owns a buffered channel; producers drop on full so a slow
// reader cannot stall the daemon. Used by /api/events/stream and fed by
// Server.Emit (which is wired into the global events.Sink at daemon boot).
type eventBus struct {
	mu   sync.Mutex
	subs map[chan model.Event]struct{}
}

func newEventBus() *eventBus {
	return &eventBus{subs: make(map[chan model.Event]struct{})}
}

// subscriberBufferSize is the per-subscriber backlog. SSE bursts during
// replay are bounded by the events ring cap (MaxEventsRetained); 256 keeps
// the steady state generous without committing megabytes of memory per
// connection. Overflow drops events for that subscriber — they reconnect
// with a stale cursor and the resync mechanism rescues them.
const subscriberBufferSize = 256

func (b *eventBus) subscribe() (chan model.Event, func()) {
	ch := make(chan model.Event, subscriberBufferSize)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		if _, ok := b.subs[ch]; ok {
			delete(b.subs, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
}

func (b *eventBus) publish(ev model.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- ev:
		default:
			// Subscriber too slow — drop and let the reconnect resync.
		}
	}
}

func (b *eventBus) subscriberCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}

// Emit persists the event to the ring and broadcasts it to live SSE
// subscribers. Implements events.Sink — installed via events.SetSink during
// daemon boot.
//
// Persistence happens synchronously so the broadcast order matches the
// commit order. Any error from the DB layer is logged but does not panic —
// emission sites cannot recover from "events table is broken" and we'd
// rather lose visibility than wedge the daemon.
func (s *Server) Emit(ev model.Event) {
	saved, err := s.db.InsertEvent(&ev)
	if err != nil {
		slog.Error("api: persist event failed", "type", ev.Type, "err", err)
		return
	}
	s.eventBus.publish(*saved)
}

// emitForTest is the test-only variant of Emit that returns the persisted
// event. Production code goes through Emit (the events.Sink contract) which
// discards the ID. Tests use this hook to capture the assigned ID for
// downstream cursor assertions.
func (s *Server) emitForTest(ev model.Event) model.Event {
	saved, err := s.db.InsertEvent(&ev)
	if err != nil {
		return ev
	}
	s.eventBus.publish(*saved)
	return *saved
}

// handleEventsStream serves GET /api/events/stream as an SSE channel.
//
// Cursor semantics (matches the plan): `since` is exclusive. The handler
// first checks whether the cursor predates the oldest retained event — if so
// it emits a synthetic `resync` event before any replay, telling the client
// it missed history and should snapshot daemon state.
//
// Live-vs-replay fencing: we subscribe BEFORE snapshotting the latest ID,
// then replay [since+1, replayEnd] from the DB and drop any live event with
// id <= replayEnd. Without that fence, an event committed between the
// snapshot and the subscribe could be delivered twice, or one committed
// during replay could land out of order. Subscribing first costs nothing —
// the subscriber channel just buffers the eclipse interval.
func (s *Server) handleEventsStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming not supported"})
		return
	}

	var since int64
	if v := r.URL.Query().Get("since"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			since = n
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	// Subscribe FIRST so no live event slips between the snapshot below
	// and the subscription registration.
	ch, unsub := s.eventBus.subscribe()
	defer unsub()

	// Resync detection: if the caller's cursor points at an event older
	// than the oldest surviving row, history has rotated out from under
	// them. Emit a synthetic resync event before any replay so the client
	// knows to snapshot daemon state.
	if since > 0 {
		oldest, err := s.db.OldestEventID()
		if err == nil && oldest > 0 && since < oldest {
			writeSSEEvent(w, flusher, model.Event{
				Type: model.EventTypeResync,
				At:   time.Now(),
				Payload: json.RawMessage(fmt.Sprintf(
					`{"reason":"cursor_older_than_ring","cursor":%d,"oldest":%d}`,
					since, oldest,
				)),
			})
		}
	}

	// Snapshot replay boundary so live events that committed after this
	// point can be deduped (skipped if their id is <= replayEnd).
	replayEnd, err := s.db.LatestEventID()
	if err != nil {
		slog.Error("api: events stream latest id failed", "err", err)
		// Continue anyway — better to serve live than to fail the request.
	}
	replayed, err := s.db.EventsSince(since, 0)
	if err != nil {
		slog.Error("api: events stream replay failed", "err", err)
	}
	for _, ev := range replayed {
		writeSSEEvent(w, flusher, *ev)
	}

	keepalive := time.NewTicker(30 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if ev.ID <= replayEnd {
				// Already covered by the replay above — skip the dupe.
				continue
			}
			writeSSEEvent(w, flusher, ev)
		case <-keepalive.C:
			fmt.Fprintf(w, ": ping\n\n") //nolint:errcheck
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// writeSSEEvent encodes a single model.Event as an SSE block:
//
//	event: <type>
//	data:  <json>
//	<blank line>
//
// Using the typed `event:` field lets clients dispatch with
// addEventListener("task.status_changed", ...) instead of parsing every
// data: line. Errors writing to the response are dropped — the client has
// disconnected and r.Context().Done() will fire shortly.
func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, ev model.Event) {
	body, err := json.Marshal(ev)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, body) //nolint:errcheck
	flusher.Flush()
}
