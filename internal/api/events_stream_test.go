package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// sseTestServer wraps an httptest.Server backed by the api routes so SSE
// tests can read a real socket. Cleanup is registered with the test.
func sseTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	srv, _ := testServer(t)
	hs := httptest.NewServer(authMiddleware(srv.token, srv.db, srv.push, srv.routes(), "/"))
	t.Cleanup(hs.Close)
	return srv, hs
}

// readSSEEvent reads one SSE event (event:/data:/blank-line block) from r.
// Returns (event, data, err). Empty event field means "data only" (default
// "message" event in SSE parlance). Comment lines starting with ":" are
// skipped (used by the SSE keepalive).
func readSSEEvent(scanner *bufio.Scanner) (string, string, error) {
	var event, data string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if event == "" && data == "" {
				continue
			}
			return event, data, nil
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			continue
		}
	}
	return event, data, io.EOF
}

func TestEventsStream_ReplaysSince(t *testing.T) {
	srv, hs := sseTestServer(t)

	// Seed three events directly via the server's sink path so they get
	// real IDs and end up in the persistence ring.
	srv.emitForTest(model.Event{Type: model.EventTypeTaskCreated, TaskID: "a"})
	srv.emitForTest(model.Event{Type: model.EventTypeTaskRenamed, TaskID: "a", Payload: json.RawMessage(`{"to":"name"}`)})
	srv.emitForTest(model.Event{Type: model.EventTypeTaskCompleted, TaskID: "a"})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	req, _ := http.NewRequestWithContext(ctx, "GET", hs.URL+"/api/events/stream?since=0", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	testutil.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })

	testutil.Equal(t, resp.StatusCode, http.StatusOK)
	testutil.Equal(t, resp.Header.Get("Content-Type"), "text/event-stream")

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for i, want := range []string{
		model.EventTypeTaskCreated,
		model.EventTypeTaskRenamed,
		model.EventTypeTaskCompleted,
	} {
		evType, data, err := readSSEEvent(scanner)
		testutil.NoError(t, err)
		testutil.Equal(t, evType, want)
		var ev model.Event
		testutil.NoError(t, json.Unmarshal([]byte(data), &ev))
		if ev.ID <= 0 {
			t.Errorf("event %d: expected positive id", i)
		}
	}
}

func TestEventsStream_StrictCursorInequality(t *testing.T) {
	srv, hs := sseTestServer(t)
	srv.emitForTest(model.Event{Type: "a", TaskID: "1"})
	mid := srv.emitForTest(model.Event{Type: "b", TaskID: "2"})
	srv.emitForTest(model.Event{Type: "c", TaskID: "3"})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET",
		fmt.Sprintf("%s/api/events/stream?since=%d", hs.URL, mid.ID), nil)
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	testutil.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	evType, _, err := readSSEEvent(scanner)
	testutil.NoError(t, err)
	testutil.Equal(t, evType, "c") // only the event newer than mid.ID
}

func TestEventsStream_ResyncOnStaleCursor(t *testing.T) {
	srv, hs := sseTestServer(t)

	// Force a tiny cap so eviction happens after one extra insert.
	old := db.SetEventsCapForTest(5)
	t.Cleanup(func() { db.SetEventsCapForTest(old) })

	first := srv.emitForTest(model.Event{Type: "a", TaskID: "1"})
	for i := range 6 {
		srv.emitForTest(model.Event{Type: "x", TaskID: fmt.Sprintf("t-%d", i)})
	}

	// `first` is now evicted. Connect with since pointing at first.ID — older
	// than every retained row — and expect a resync as the very first event.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET",
		fmt.Sprintf("%s/api/events/stream?since=%d", hs.URL, first.ID), nil)
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	testutil.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	evType, _, err := readSSEEvent(scanner)
	testutil.NoError(t, err)
	testutil.Equal(t, evType, model.EventTypeResync)
}

func TestEventsStream_LiveAfterReplay(t *testing.T) {
	srv, hs := sseTestServer(t)

	first := srv.emitForTest(model.Event{Type: "a", TaskID: "1"})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET",
		fmt.Sprintf("%s/api/events/stream?since=%d", hs.URL, first.ID), nil)
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	testutil.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	// Give the subscription a moment to attach, then emit a live event.
	// readSSEEvent will block until the live event lands.
	time.Sleep(100 * time.Millisecond)
	srv.emitForTest(model.Event{Type: model.EventTypeMessageSent, TaskID: "1"})

	evType, _, err := readSSEEvent(scanner)
	testutil.NoError(t, err)
	testutil.Equal(t, evType, model.EventTypeMessageSent)
}

func TestEventsStream_ClientDisconnectCleansUp(t *testing.T) {
	srv, hs := sseTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	req, _ := http.NewRequestWithContext(ctx, "GET", hs.URL+"/api/events/stream?since=0", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	testutil.NoError(t, err)
	// Read at least the first byte (status code already arrived; SSE keeps
	// the body open). Then cancel the context — the handler should clean up.
	cancel()
	resp.Body.Close()

	// Subscriber count drops to zero after the handler exits. Allow a short
	// settling window since the cleanup runs after the goroutine notices ctx.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if srv.eventBus.subscriberCount() == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("expected subscriber count to drop to 0, got %d", srv.eventBus.subscriberCount())
}

func TestEventsStream_InvalidSinceFallsBackToZero(t *testing.T) {
	srv, hs := sseTestServer(t)
	srv.emitForTest(model.Event{Type: "a", TaskID: "1"})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET",
		hs.URL+"/api/events/stream?since=not-a-number", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	testutil.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	evType, _, err := readSSEEvent(scanner)
	testutil.NoError(t, err)
	testutil.Equal(t, evType, "a")
}

// TestServer_EmitDBErrorIsLogged covers the InsertEvent failure branch.
// We trigger it by closing the DB out from under the server; subsequent
// Emit must not panic and must not publish.
func TestServer_EmitDBErrorIsLogged(t *testing.T) {
	srv, d := testServer(t)
	ch, unsub := srv.eventBus.subscribe()
	t.Cleanup(unsub)

	testutil.NoError(t, d.Close())

	// Must not panic.
	srv.Emit(model.Event{Type: "boom", TaskID: "x"})

	select {
	case ev := <-ch:
		t.Fatalf("expected no publish on insert failure, got %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestServer_EmitForTestDBErrorReturnsInput(t *testing.T) {
	srv, d := testServer(t)
	testutil.NoError(t, d.Close())
	in := model.Event{Type: "noop", TaskID: "x"}
	out := srv.emitForTest(in)
	// On failure the returned event has no ID assigned.
	testutil.Equal(t, out.ID, int64(0))
	testutil.Equal(t, out.Type, "noop")
}

func TestServer_EmitPersistsAndPublishes(t *testing.T) {
	srv, _ := testServer(t)
	ch, unsub := srv.eventBus.subscribe()
	t.Cleanup(unsub)

	// Use the Sink-contract entry point (Emit) rather than the test-only
	// helper so this test pins the public surface installed via
	// events.SetSink at daemon boot.
	go func() {
		srv.Emit(model.Event{Type: "x", TaskID: "task-1"})
	}()

	select {
	case ev := <-ch:
		testutil.Equal(t, ev.Type, "x")
		testutil.Equal(t, ev.TaskID, "task-1")
		if ev.ID <= 0 {
			t.Errorf("expected positive id, got %d", ev.ID)
		}
		// Round-trip — DB should hold the row too.
		got, err := srv.db.EventsSince(0, 0)
		testutil.NoError(t, err)
		testutil.Equal(t, len(got), 1)
		testutil.Equal(t, got[0].ID, ev.ID)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for emit")
	}
}
