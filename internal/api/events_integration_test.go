package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/drn/argus/internal/events"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// TestEventsIntegration_DBMutationsEmitEvents wires the server as the
// global events.Sink (the same plumbing the daemon installs at boot) and
// asserts that db mutations and orch operations show up on /api/events/stream.
// One test is intentionally broad — the plugin contract is "events from
// these sites land in the stream"; granular unit tests for each Emit call
// would multiply faster than they'd catch.
func TestEventsIntegration_DBMutationsEmitEvents(t *testing.T) {
	srv, _ := testServer(t)
	hs := httptest.NewServer(authMiddleware(srv.token, srv.db, srv.push, srv.routes(), "/"))
	t.Cleanup(hs.Close)

	prev := events.SetSink(srv)
	t.Cleanup(func() { events.SetSink(prev) })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	req, _ := http.NewRequestWithContext(ctx, "GET", hs.URL+"/api/events/stream?since=0", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	testutil.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	// Settling window: subscribe FIRST, then emit. Without the sleep the
	// db.Add below could race the SSE handler's subscribe call.
	time.Sleep(100 * time.Millisecond)

	// 1. Create task — expect task.created.
	task := &model.Task{
		ID:      "t-evt-1",
		Name:    "first",
		Project: "proj",
		Status:  model.StatusPending,
	}
	testutil.NoError(t, srv.db.Add(task))

	// 2. Update status — expect task.status_changed.
	task.Status = model.StatusInProgress
	testutil.NoError(t, srv.db.Update(task))

	// 3. Update to complete — expect task.status_changed AND task.completed.
	task.Status = model.StatusComplete
	testutil.NoError(t, srv.db.Update(task))

	// 4. Rename — expect task.renamed.
	testutil.NoError(t, srv.db.Rename(task.ID, "renamed"))

	// 5. Archive — expect task.archived.
	testutil.NoError(t, srv.db.SetArchived(task.ID, true))

	gotTypes := map[string]bool{}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(gotTypes) < 5 {
		evType, data, err := readSSEEvent(scanner)
		if err != nil {
			break
		}
		gotTypes[evType] = true
		var ev model.Event
		_ = json.Unmarshal([]byte(data), &ev) //nolint:errcheck
	}

	for _, want := range []string{
		model.EventTypeTaskCreated,
		model.EventTypeTaskStatusChanged,
		model.EventTypeTaskCompleted,
		model.EventTypeTaskRenamed,
		model.EventTypeTaskArchived,
	} {
		if !gotTypes[want] {
			t.Errorf("expected event %q on stream, got types %v", want, gotTypes)
		}
	}
}

// TestEventsIntegration_MessageFlowEmits exercises the messaging emission
// sites (db.InsertMessage / db.AckMessages).
func TestEventsIntegration_MessageFlowEmits(t *testing.T) {
	srv, _ := testServer(t)
	prev := events.SetSink(srv)
	t.Cleanup(func() { events.SetSink(prev) })

	// Seed both ends of the message so InsertMessage's self-send check
	// passes and AckMessages has a row to flip.
	testutil.NoError(t, srv.db.Add(&model.Task{ID: "sender", Status: model.StatusInProgress}))
	testutil.NoError(t, srv.db.Add(&model.Task{ID: "receiver", Status: model.StatusInProgress}))

	ch, unsub := srv.eventBus.subscribe()
	t.Cleanup(unsub)

	msg, err := srv.db.InsertMessage(&model.TaskMessage{
		From: "sender",
		To:   "receiver",
		Kind: model.KindNote,
		Body: "hello",
	})
	testutil.NoError(t, err)

	n, err := srv.db.AckMessages("receiver", []string{msg.ID})
	testutil.NoError(t, err)
	testutil.Equal(t, n, 1)

	gotTypes := map[string]bool{}
	deadline := time.After(2 * time.Second)
	for len(gotTypes) < 2 {
		select {
		case ev := <-ch:
			gotTypes[ev.Type] = true
		case <-deadline:
			t.Fatalf("timeout, got %v", gotTypes)
		}
	}

	if !gotTypes[model.EventTypeMessageSent] {
		t.Errorf("expected message.sent, got %v", gotTypes)
	}
	if !gotTypes[model.EventTypeMessageAcked] {
		t.Errorf("expected message.acked, got %v", gotTypes)
	}
}
