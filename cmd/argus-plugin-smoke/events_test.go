package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// emittingSSEServer pretends to be /api/events/stream. The handler holds the
// response writer in scope for its lifetime so Emit can push frames; tests
// drive it by calling Emit after waiting on ready.
type emittingSSEServer struct {
	t      *testing.T
	server *httptest.Server
	ch     chan model.Event
	ready  chan struct{}
}

func newEmittingSSEServer(t *testing.T) *emittingSSEServer {
	t.Helper()
	s := &emittingSSEServer{
		t:     t,
		ch:    make(chan model.Event, 16),
		ready: make(chan struct{}),
	}
	s.server = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

func (s *emittingSSEServer) handle(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") == "" {
		http.Error(w, "auth required", http.StatusUnauthorized)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "no flusher", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	close(s.ready)
	for {
		select {
		case ev := <-s.ch:
			body, _ := json.Marshal(ev)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, body)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *emittingSSEServer) Emit(ev model.Event) {
	s.ch <- ev
}

func (s *emittingSSEServer) URL() string {
	return s.server.URL
}

func (s *emittingSSEServer) Close() {
	s.server.Close()
}

func TestStartEventStream_RejectsBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no auth", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	s := &smoke{
		baseURL:    srv.URL,
		scopeToken: "x",
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
	_, err := s.startEventStream(0)
	if err == nil || !strings.Contains(err.Error(), "SSE status 403") {
		t.Fatalf("expected SSE status 403 error, got %v", err)
	}
}

func TestEventSub_ReceivesAndDecodes(t *testing.T) {
	srv := newEmittingSSEServer(t)
	t.Cleanup(srv.Close)

	s := &smoke{
		baseURL:    srv.URL(),
		scopeToken: "x",
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
	sub, err := s.startEventStream(0)
	testutil.NoError(t, err)
	t.Cleanup(sub.Close)
	<-srv.ready

	srv.Emit(model.Event{ID: 1, Type: model.EventTypeTaskCreated, TaskID: "t1", At: time.Now()})
	srv.Emit(model.Event{ID: 2, Type: model.EventTypeTaskStatusChanged, TaskID: "t1", At: time.Now()})

	ev, err := sub.WaitFor(2*time.Second, func(e model.Event) bool {
		return e.Type == model.EventTypeTaskStatusChanged
	})
	testutil.NoError(t, err)
	testutil.Equal(t, ev.ID, int64(2))
	testutil.Equal(t, ev.TaskID, "t1")

	snap := sub.Snapshot()
	testutil.Equal(t, len(snap), 2)
	testutil.Equal(t, snap[0].Type, model.EventTypeTaskCreated)
}

func TestEventSub_WaitForTimeout(t *testing.T) {
	srv := newEmittingSSEServer(t)
	t.Cleanup(srv.Close)

	s := &smoke{
		baseURL:    srv.URL(),
		scopeToken: "x",
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
	sub, err := s.startEventStream(0)
	testutil.NoError(t, err)
	t.Cleanup(sub.Close)
	<-srv.ready

	_, err = sub.WaitFor(50*time.Millisecond, func(e model.Event) bool { return false })
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestEventSub_IgnoresKeepaliveComments(t *testing.T) {
	// Mimic an SSE server that emits a `: ping` keepalive comment, then a
	// real event. We do it by hand since the emittingSSEServer doesn't write
	// keepalives.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()
		_, _ = fmt.Fprintf(w, ": ping\n\n")
		flusher.Flush()
		ev := model.Event{ID: 7, Type: model.EventTypeSessionStarted, TaskID: "abc", At: time.Now()}
		body, _ := json.Marshal(ev)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, body)
		flusher.Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	s := &smoke{
		baseURL:    srv.URL,
		scopeToken: "x",
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
	sub, err := s.startEventStream(0)
	testutil.NoError(t, err)
	t.Cleanup(sub.Close)

	ev, err := sub.WaitFor(2*time.Second, func(e model.Event) bool { return e.ID == 7 })
	testutil.NoError(t, err)
	testutil.Equal(t, ev.Type, model.EventTypeSessionStarted)
}
