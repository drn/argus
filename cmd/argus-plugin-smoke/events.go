package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/drn/argus/internal/model"
)

// eventSub is a live subscription to /api/events/stream. Events are decoded
// off the SSE stream by a background goroutine into a slice each phase can
// poll for assertions.
type eventSub struct {
	cancel context.CancelFunc
	done   chan struct{}

	mu     sync.Mutex
	events []model.Event
	err    error
}

// startEventStream opens an SSE connection at /api/events/stream?since=<since>
// using the scope token, and returns a handle once the HTTP response is
// confirmed 200. Cancel via Close() — the background reader drains until then.
func (s *smoke) startEventStream(since int64) (*eventSub, error) {
	ctx, cancel := context.WithCancel(context.Background())
	url := fmt.Sprintf("%s/api/events/stream?since=%d", s.baseURL, since)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.scopeToken)
	req.Header.Set("Accept", "text/event-stream")

	// Don't reuse s.httpClient — its 30s timeout would kill an SSE stream
	// mid-flight. Context cancellation handles termination here.
	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		cancel()
		return nil, fmt.Errorf("SSE status %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	e := &eventSub{
		cancel: cancel,
		done:   make(chan struct{}),
	}
	go e.readLoop(resp)
	return e, nil
}

// readLoop parses SSE blocks (`event: <type>` / `data: <json>` / blank-line
// terminated) into model.Event values. SSE comments (`: keepalive`) are
// ignored; we trust the `data:` JSON's own `type` field rather than parse
// the `event:` line, since they're guaranteed equal by the server.
func (e *eventSub) readLoop(resp *http.Response) {
	defer close(e.done)
	defer resp.Body.Close() //nolint:errcheck

	sc := bufio.NewScanner(resp.Body)
	// SSE frames can exceed bufio's 64 KiB default — bump to 1 MiB so a
	// big-payload event (e.g. forks with metadata) doesn't trip ErrTooLong.
	sc.Buffer(make([]byte, 4096), 1<<20)

	var dataLine string
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "data:"):
			dataLine = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		case line == "" && dataLine != "":
			var ev model.Event
			if err := json.Unmarshal([]byte(dataLine), &ev); err == nil {
				e.mu.Lock()
				e.events = append(e.events, ev)
				e.mu.Unlock()
			}
			dataLine = ""
		}
		// `event:` lines are redundant (the JSON includes Type) and SSE
		// comment lines (": ping") are ignored.
	}
	e.mu.Lock()
	e.err = sc.Err()
	e.mu.Unlock()
}

// WaitFor polls every 10ms for the first event satisfying match, up to
// timeout. Returns the matching event or an error.
func (e *eventSub) WaitFor(timeout time.Duration, match func(model.Event) bool) (model.Event, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		e.mu.Lock()
		for _, ev := range e.events {
			if match(ev) {
				e.mu.Unlock()
				return ev, nil
			}
		}
		e.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	return model.Event{}, fmt.Errorf("no event matched within %v", timeout)
}

// Snapshot returns a copy of every event received so far.
func (e *eventSub) Snapshot() []model.Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]model.Event, len(e.events))
	copy(out, e.events)
	return out
}

// Close cancels the underlying request and blocks until the reader returns.
func (e *eventSub) Close() {
	e.cancel()
	<-e.done
}
