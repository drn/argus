package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// mockMessageStore is an in-memory MessageStore for the messaging tool tests.
// Mirrors the *db.DB contract on the exact subset the tools rely on; full
// SQLite coverage lives in internal/db/messages_test.go.
type mockMessageStore struct {
	mu        sync.Mutex
	messages  []*model.TaskMessage
	failNext  error
	deletedID string
}

func (m *mockMessageStore) InsertMessage(msg *model.TaskMessage) (*model.TaskMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failNext != nil {
		err := m.failNext
		m.failNext = nil
		return nil, err
	}
	if err := msg.Validate(); err != nil {
		return nil, err
	}
	if msg.From == msg.To {
		return nil, db.ErrMessageSelfSend
	}
	if len(msg.Body) > model.MaxMessageBodyBytes {
		return nil, db.ErrMessageBodyTooLarge
	}
	if msg.ID == "" {
		msg.ID = fmt.Sprintf("m%d", len(m.messages)+1)
	}
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now()
	}
	m.messages = append(m.messages, msg)
	return msg, nil
}

func (m *mockMessageStore) Inbox(toID string, f db.InboxFilter) ([]*model.TaskMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*model.TaskMessage
	for _, msg := range m.messages {
		if msg.To != toID {
			continue
		}
		if f.UnreadOnly && !msg.ReadAt.IsZero() {
			continue
		}
		if f.Sender != "" && msg.From != f.Sender {
			continue
		}
		if !f.Since.IsZero() && !msg.CreatedAt.After(f.Since) {
			continue
		}
		out = append(out, msg)
	}
	return out, nil
}

func (m *mockMessageStore) AckMessages(toID string, ids []string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	n := 0
	for _, msg := range m.messages {
		if msg.To == toID && set[msg.ID] && msg.ReadAt.IsZero() {
			msg.ReadAt = time.Now()
			n++
		}
	}
	return n, nil
}

func (m *mockMessageStore) WaitForReply(ctx context.Context, questionID, fromID string) (*model.TaskMessage, error) {
	check := func() *model.TaskMessage {
		m.mu.Lock()
		defer m.mu.Unlock()
		for _, msg := range m.messages {
			if msg.InReplyTo == questionID && msg.From == fromID {
				return msg
			}
		}
		return nil
	}
	if r := check(); r != nil {
		return r, nil
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, nil
		case <-ticker.C:
			if r := check(); r != nil {
				return r, nil
			}
		}
	}
}

func (m *mockMessageStore) DeleteMessagesForTask(taskID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deletedID = taskID
	kept := m.messages[:0]
	deleted := 0
	for _, msg := range m.messages {
		if msg.From == taskID || msg.To == taskID {
			deleted++
			continue
		}
		kept = append(kept, msg)
	}
	m.messages = kept
	return deleted, nil
}

// mockNudger records the nudge calls a test produces, so we can assert
// whether the live-PTY notification path was exercised. Returns the seeded
// err to let a test simulate "session disappeared between snapshot and write".
type mockNudger struct {
	mu     sync.Mutex
	called []nudgeCall
	err    error
}

type nudgeCall struct {
	target string
	line   string
}

func (n *mockNudger) Nudge(target, line string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.called = append(n.called, nudgeCall{target, line})
	return n.err
}

func (n *mockNudger) Calls() []nudgeCall {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]nudgeCall(nil), n.called...)
}

// testServerWithMessaging wires a Server with task management AND the message
// store + nudger. Returns the mocks so tests can inspect state.
func testServerWithMessaging() (*Server, *mockTaskDB, *mockMessageStore, *mockNudger) {
	s, taskDB, _ := testServerWithTasks()
	store := &mockMessageStore{}
	nudger := &mockNudger{}
	s.SetMessageManager(store, nudger)
	return s, taskDB, store, nudger
}

// --- Tool surface gating ---

func TestToolsList_WithMessaging(t *testing.T) {
	s, _, _, _ := testServerWithMessaging()
	resp := doRequest(t, s, "tools/list", nil)
	testutil.NoError(t, respErr(resp))
	result, _ := json.Marshal(resp.Result)
	var list ToolsListResult
	json.Unmarshal(result, &list) //nolint:errcheck

	names := make(map[string]bool)
	for _, tool := range list.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"task_message_send", "task_inbox", "task_message_ack", "task_ask"} {
		if !names[want] {
			t.Errorf("missing messaging tool: %s", want)
		}
	}
}

func TestToolsList_WithoutMessaging(t *testing.T) {
	// Task mgmt wired but messaging not — messaging tools must NOT appear.
	s, _, _ := testServerWithTasks()
	resp := doRequest(t, s, "tools/list", nil)
	testutil.NoError(t, respErr(resp))
	result, _ := json.Marshal(resp.Result)
	var list ToolsListResult
	json.Unmarshal(result, &list) //nolint:errcheck

	for _, tool := range list.Tools {
		if strings.HasPrefix(tool.Name, "task_message_") || tool.Name == "task_inbox" || tool.Name == "task_ask" {
			t.Errorf("unexpected messaging tool exposed: %s", tool.Name)
		}
	}
}

// --- task_message_send ---

func TestToolMessageSend_HappyPath(t *testing.T) {
	s, _, store, nudger := testServerWithMessaging()
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "task_message_send",
		Arguments: json.RawMessage(`{"id":"abc123","to":"def456","body":"hello peer"}`),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("unexpected error: %v", cr.Content)
	}
	if len(store.messages) != 1 {
		t.Fatalf("expected 1 message stored, got %d", len(store.messages))
	}
	got := store.messages[0]
	testutil.Equal(t, got.From, "abc123")
	testutil.Equal(t, got.To, "def456")
	testutil.Equal(t, got.Kind, model.KindNote)
	testutil.Equal(t, got.Body, "hello peer")

	calls := nudger.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected one nudge, got %d", len(calls))
	}
	testutil.Equal(t, calls[0].target, "def456")
	testutil.Contains(t, calls[0].line, "[argus]")
	testutil.Contains(t, calls[0].line, "abc123")
}

func TestToolMessageSend_NudgeFailureStillSucceeds(t *testing.T) {
	s, _, store, nudger := testServerWithMessaging()
	nudger.err = errors.New("no live session")
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "task_message_send",
		Arguments: json.RawMessage(`{"id":"abc123","to":"def456","body":"x"}`),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("send should succeed even when nudge fails: %v", cr.Content)
	}
	if len(store.messages) != 1 {
		t.Fatalf("expected message to be stored despite nudge failure")
	}
	if !strings.Contains(textOf(cr), "queued") {
		t.Errorf("expected delivered=queued, got: %s", textOf(cr))
	}
}

func TestToolMessageSend_RejectsUnknownRecipient(t *testing.T) {
	s, _, _, _ := testServerWithMessaging()
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "task_message_send",
		Arguments: json.RawMessage(`{"id":"abc123","to":"does-not-exist","body":"x"}`),
	})
	cr := callResult(t, resp)
	if !cr.IsError {
		t.Fatal("expected error for unknown recipient")
	}
	testutil.Contains(t, textOf(cr), "recipient task not found")
}

func TestToolMessageSend_ValidationFailures(t *testing.T) {
	s, _, _, _ := testServerWithMessaging()
	cases := []struct {
		name string
		args string
		want string
	}{
		{"missing to", `{"id":"abc123","body":"x"}`, "to is required"},
		{"missing body", `{"id":"abc123","to":"def456"}`, "body is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := doRequest(t, s, "tools/call", ToolCallParams{
				Name:      "task_message_send",
				Arguments: json.RawMessage(tc.args),
			})
			cr := callResult(t, resp)
			if !cr.IsError {
				t.Fatal("expected error")
			}
			testutil.Contains(t, textOf(cr), tc.want)
		})
	}
}

func TestToolMessageSend_StoreErrorsTranslated(t *testing.T) {
	cases := []struct {
		name      string
		storeErr  error
		wantInMsg string
	}{
		{"body too large", db.ErrMessageBodyTooLarge, "exceeds"},
		{"self send", db.ErrMessageSelfSend, "self"},
		{"inbox full", db.ErrMessageInboxFull, "inbox is full"},
		{"rate limited", db.ErrMessageRateLimited, "rate limit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, store, _ := testServerWithMessaging()
			store.failNext = tc.storeErr
			resp := doRequest(t, s, "tools/call", ToolCallParams{
				Name:      "task_message_send",
				Arguments: json.RawMessage(`{"id":"abc123","to":"def456","body":"x"}`),
			})
			cr := callResult(t, resp)
			if !cr.IsError {
				t.Fatal("expected tool error")
			}
			testutil.Contains(t, textOf(cr), tc.wantInMsg)
		})
	}
}

func TestToolMessageSend_NotConfigured(t *testing.T) {
	s, _, _ := testServerWithTasks() // no SetMessageManager
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "task_message_send",
		Arguments: json.RawMessage(`{"id":"abc123","to":"def456","body":"x"}`),
	})
	cr := callResult(t, resp)
	if !cr.IsError {
		t.Fatal("expected error when messaging not configured")
	}
	testutil.Contains(t, textOf(cr), "not configured")
}

// --- task_inbox ---

func TestToolInbox_EmptyAndPopulated(t *testing.T) {
	s, _, store, _ := testServerWithMessaging()

	// Empty case
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "task_inbox",
		Arguments: json.RawMessage(`{"id":"abc123"}`),
	})
	cr := callResult(t, resp)
	testutil.Contains(t, textOf(cr), "Inbox empty")

	// Populate
	store.messages = append(store.messages, &model.TaskMessage{
		ID: "m1", From: "def456", To: "abc123", Kind: model.KindNote,
		Body: "hello", CreatedAt: time.Now(),
	})

	resp = doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "task_inbox",
		Arguments: json.RawMessage(`{"id":"abc123"}`),
	})
	cr = callResult(t, resp)
	testutil.Contains(t, textOf(cr), "m1")
	testutil.Contains(t, textOf(cr), "hello")
}

func TestToolInbox_RejectsBadSince(t *testing.T) {
	s, _, _, _ := testServerWithMessaging()
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "task_inbox",
		Arguments: json.RawMessage(`{"id":"abc123","since":"not-a-timestamp"}`),
	})
	cr := callResult(t, resp)
	if !cr.IsError {
		t.Fatal("expected error for bad since")
	}
	testutil.Contains(t, textOf(cr), "invalid since")
}

// --- task_message_ack ---

func TestToolMessageAck_HappyPath(t *testing.T) {
	s, _, store, _ := testServerWithMessaging()
	store.messages = append(store.messages,
		&model.TaskMessage{ID: "m1", From: "def456", To: "abc123", Kind: model.KindNote, Body: "x", CreatedAt: time.Now()},
		&model.TaskMessage{ID: "m2", From: "def456", To: "abc123", Kind: model.KindNote, Body: "y", CreatedAt: time.Now()},
	)
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "task_message_ack",
		Arguments: json.RawMessage(`{"id":"abc123","message_ids":["m1","m2","nonexistent"]}`),
	})
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("unexpected error: %v", cr.Content)
	}
	testutil.Contains(t, textOf(cr), "Acked 2")
}

func TestToolMessageAck_NoCallerResolve(t *testing.T) {
	s, _, _, _ := testServerWithMessaging()
	// No id or cwd → resolveTask errors → tool surfaces it.
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "task_message_ack",
		Arguments: json.RawMessage(`{"message_ids":["m1"]}`),
	})
	cr := callResult(t, resp)
	if !cr.IsError {
		t.Fatal("expected error for missing caller")
	}
}

func TestToolInbox_SinceSecondPrecision(t *testing.T) {
	// Cover the RFC3339 fallback parser branch by passing a second-precision
	// timestamp (no nanos). Without this the secondary parser isn't exercised.
	s, _, _, _ := testServerWithMessaging()
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "task_inbox",
		Arguments: json.RawMessage(`{"id":"abc123","since":"2026-01-01T00:00:00Z"}`),
	})
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("unexpected error: %v", cr.Content)
	}
}

func TestToolMessageAck_RejectsEmptyAndOversize(t *testing.T) {
	s, _, _, _ := testServerWithMessaging()

	t.Run("empty", func(t *testing.T) {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "task_message_ack",
			Arguments: json.RawMessage(`{"id":"abc123","message_ids":[]}`),
		})
		cr := callResult(t, resp)
		if !cr.IsError {
			t.Fatal("expected error for empty list")
		}
	})

	t.Run("oversize", func(t *testing.T) {
		ids := make([]string, maxAckIDsPerCall+1)
		for i := range ids {
			ids[i] = fmt.Sprintf("m%d", i)
		}
		args, _ := json.Marshal(map[string]any{"id": "abc123", "message_ids": ids})
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "task_message_ack",
			Arguments: args,
		})
		cr := callResult(t, resp)
		if !cr.IsError {
			t.Fatal("expected error for oversize list")
		}
		testutil.Contains(t, textOf(cr), "too many")
	})
}

// --- task_ask ---

func TestToolAsk_NonBlocking(t *testing.T) {
	s, _, store, _ := testServerWithMessaging()
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "task_ask",
		Arguments: json.RawMessage(`{"id":"abc123","to":"def456","body":"what?","timeout_seconds":0}`),
	})
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("unexpected error: %v", cr.Content)
	}
	testutil.Contains(t, textOf(cr), "Question sent")
	if len(store.messages) != 1 {
		t.Fatalf("expected 1 message stored, got %d", len(store.messages))
	}
	testutil.Equal(t, store.messages[0].Kind, model.KindQuestion)
}

func TestToolAsk_BlocksAndReturnsReply(t *testing.T) {
	if testing.Short() {
		t.Skip("blocking-poll test")
	}
	s, _, store, _ := testServerWithMessaging()

	// Spawn a goroutine that drops the answer into the store after a brief
	// delay so the polling loop has to wait at least one tick.
	go func() {
		time.Sleep(100 * time.Millisecond)
		store.mu.Lock()
		// Find the question, then queue an answer pointing at it.
		var qid string
		for _, m := range store.messages {
			if m.Kind == model.KindQuestion {
				qid = m.ID
				break
			}
		}
		store.mu.Unlock()
		if qid == "" {
			return
		}
		_, _ = store.InsertMessage(&model.TaskMessage{
			From: "def456", To: "abc123", Kind: model.KindAnswer,
			Body: "answer here", InReplyTo: qid,
		})
	}()

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "task_ask",
		Arguments: json.RawMessage(`{"id":"abc123","to":"def456","body":"what?","timeout_seconds":3}`),
	})
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("unexpected error: %v", cr.Content)
	}
	testutil.Contains(t, textOf(cr), "answer here")
}

func TestToolAsk_TimesOut(t *testing.T) {
	if testing.Short() {
		t.Skip("blocking-poll test")
	}
	s, _, _, _ := testServerWithMessaging()
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "task_ask",
		Arguments: json.RawMessage(`{"id":"abc123","to":"def456","body":"what?","timeout_seconds":1}`),
	})
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("unexpected error: %v", cr.Content)
	}
	testutil.Contains(t, textOf(cr), "No reply within")
}

func TestToolAsk_ValidationFailures(t *testing.T) {
	s, _, _, _ := testServerWithMessaging()
	cases := []struct {
		name string
		args string
		want string
	}{
		{"missing to", `{"id":"abc123","body":"x"}`, "to is required"},
		{"missing body", `{"id":"abc123","to":"def456"}`, "body is required"},
		{"negative timeout", `{"id":"abc123","to":"def456","body":"x","timeout_seconds":-1}`, "must be >= 0"},
		{"unknown recipient", `{"id":"abc123","to":"missing","body":"x"}`, "recipient task not found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := doRequest(t, s, "tools/call", ToolCallParams{
				Name:      "task_ask",
				Arguments: json.RawMessage(tc.args),
			})
			cr := callResult(t, resp)
			if !cr.IsError {
				t.Fatal("expected error")
			}
			testutil.Contains(t, textOf(cr), tc.want)
		})
	}
}

func TestToolAsk_StoreErrorTranslated(t *testing.T) {
	s, _, store, _ := testServerWithMessaging()
	store.failNext = db.ErrMessageInboxFull
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "task_ask",
		Arguments: json.RawMessage(`{"id":"abc123","to":"def456","body":"x","timeout_seconds":0}`),
	})
	cr := callResult(t, resp)
	if !cr.IsError {
		t.Fatal("expected tool error")
	}
	testutil.Contains(t, textOf(cr), "inbox is full")
}

func TestToolAsk_NotConfigured(t *testing.T) {
	s, _, _ := testServerWithTasks() // no SetMessageManager
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "task_ask",
		Arguments: json.RawMessage(`{"id":"abc123","to":"def456","body":"x"}`),
	})
	cr := callResult(t, resp)
	if !cr.IsError {
		t.Fatal("expected error when messaging not configured")
	}
	testutil.Contains(t, textOf(cr), "not configured")
}

func TestToolInbox_NotConfigured(t *testing.T) {
	s, _, _ := testServerWithTasks() // no SetMessageManager
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "task_inbox",
		Arguments: json.RawMessage(`{"id":"abc123"}`),
	})
	cr := callResult(t, resp)
	if !cr.IsError {
		t.Fatal("expected error when messaging not configured")
	}
	testutil.Contains(t, textOf(cr), "not configured")
}

func TestToolMessageAck_NotConfigured(t *testing.T) {
	s, _, _ := testServerWithTasks() // no SetMessageManager
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "task_message_ack",
		Arguments: json.RawMessage(`{"id":"abc123","message_ids":["m1"]}`),
	})
	cr := callResult(t, resp)
	if !cr.IsError {
		t.Fatal("expected error when messaging not configured")
	}
	testutil.Contains(t, textOf(cr), "not configured")
}

func TestToolInbox_AllFiltersApplied(t *testing.T) {
	s, _, store, _ := testServerWithMessaging()
	// Seed two messages so the limit + filters branches each run.
	store.messages = append(store.messages,
		&model.TaskMessage{ID: "m1", From: "def456", To: "abc123", Kind: model.KindNote, Body: "x", CreatedAt: time.Now().Add(-time.Hour)},
		&model.TaskMessage{ID: "m2", From: "def456", To: "abc123", Kind: model.KindNote, Body: "y", CreatedAt: time.Now()},
	)
	// Provide every filter so each parse branch fires.
	since := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	args := fmt.Sprintf(`{"id":"abc123","unread_only":false,"sender":"def456","since":"%s","limit":10}`, since)
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "task_inbox",
		Arguments: json.RawMessage(args),
	})
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("unexpected error: %v", cr.Content)
	}
	testutil.Contains(t, textOf(cr), "m1")
	testutil.Contains(t, textOf(cr), "m2")
}

func TestToolAsk_RejectsOversizeTimeout(t *testing.T) {
	s, _, _, _ := testServerWithMessaging()
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "task_ask",
		Arguments: json.RawMessage(`{"id":"abc123","to":"def456","body":"x","timeout_seconds":600}`),
	})
	cr := callResult(t, resp)
	if !cr.IsError {
		t.Fatal("expected error for oversize timeout")
	}
	testutil.Contains(t, textOf(cr), "exceeds")
}

// --- archive cleanup ---

func TestToolArchive_DeletesMessages(t *testing.T) {
	s, taskDB, store, _ := testServerWithMessaging()
	// Pre-seed a message addressed to abc123.
	store.messages = append(store.messages, &model.TaskMessage{
		ID: "m1", From: "def456", To: "abc123", Kind: model.KindNote, Body: "x", CreatedAt: time.Now(),
	})

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "task_archive",
		Arguments: json.RawMessage(`{"id":"abc123","archived":true}`),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("unexpected error: %v", cr.Content)
	}

	// Messages for the archived task should be gone.
	if len(store.messages) != 0 {
		t.Fatalf("expected messages to be cleared on archive, got %d", len(store.messages))
	}
	testutil.Equal(t, store.deletedID, "abc123")

	// Sanity: task is now archived.
	got, _ := taskDB.Get("abc123")
	if !got.Archived {
		t.Fatal("expected task to be archived")
	}
}

func TestToolArchive_NoMessageStoreNoOp(t *testing.T) {
	// Verify the archive cleanup path doesn't crash when messaging isn't wired.
	s, _, _ := testServerWithTasks()
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "task_archive",
		Arguments: json.RawMessage(`{"id":"abc123","archived":true}`),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("unexpected error: %v", cr.Content)
	}
}

// textOf extracts the first text content block from a ToolCallResult for
// substring assertions in tool-error tests.
func textOf(cr ToolCallResult) string {
	for _, c := range cr.Content {
		if c.Type == "text" {
			return c.Text
		}
	}
	return ""
}
