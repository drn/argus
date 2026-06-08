package daemon

import (
	"errors"
	"testing"
	"time"

	"github.com/drn/argus/internal/notify"
	"github.com/drn/argus/internal/testutil"
)

// noFocus implements notify.FocusReader — always unfocused.
type noFocus struct{}

func (noFocus) IsFocused(string) bool { return false }

// fakeNudgeRunner is a simple notify.RunnerIface backed by a map.
type fakeNudgeRunner struct {
	sessions map[string]*fakeNudgeSession
}

func (r *fakeNudgeRunner) Get(taskID string) notify.SessionHandleIface {
	if r.sessions == nil {
		return nil
	}
	return r.sessions[taskID]
}

func (r *fakeNudgeRunner) addSession(taskID string, idle bool) *fakeNudgeSession {
	if r.sessions == nil {
		r.sessions = make(map[string]*fakeNudgeSession)
	}
	s := &fakeNudgeSession{idle: idle}
	r.sessions[taskID] = s
	return s
}

type fakeNudgeSession struct {
	idle   bool
	writes [][]byte
}

func (s *fakeNudgeSession) IsIdle() bool { return s.idle }
func (s *fakeNudgeSession) WriteInput(p []byte) (int, error) {
	cp := make([]byte, len(p))
	copy(cp, p)
	s.writes = append(s.writes, cp)
	return len(p), nil
}

// TestNudge_NoNotifierReturnsSentinel covers the nil-notifier branch.
func TestNudge_NoNotifierReturnsSentinel(t *testing.T) {
	n := runnerNudger{}
	err := n.Nudge("any-id", "msg-1", "line\n")
	testutil.ErrorIs(t, err, ErrNudgeNoSession)
}

// TestNudge_WithNotifier_NoSession_ReturnsQueuedSentinel checks that Nudge
// returns ErrNudgeNoSession when no session is live, so the MCP layer reports
// delivered="queued" accurately. The delivery is still registered.
func TestNudge_WithNotifier_NoSession_ReturnsQueuedSentinel(t *testing.T) {
	r := &fakeNudgeRunner{} // no sessions
	notifier := notify.New(r, noFocus{})
	n := runnerNudger{notifier: notifier}

	err := n.Nudge("task-1", "msg-42", "text\n")
	testutil.ErrorIs(t, err, ErrNudgeNoSession) // reports queued

	// Delivery is still registered for when a session appears.
	testutil.Equal(t, notifier.DeliveryState("task-1", "msg-42"), notify.StatePending)
}

// TestNudge_WithNotifier_LiveSession_ReturnsNil checks that Nudge returns nil
// when a session is live, so the MCP layer reports delivered="nudged".
func TestNudge_WithNotifier_LiveSession_ReturnsNil(t *testing.T) {
	r := &fakeNudgeRunner{}
	r.addSession("task-1", false) // live but busy
	notifier := notify.New(r, noFocus{})
	n := runnerNudger{notifier: notifier}

	err := n.Nudge("task-1", "msg-42", "text\n")
	testutil.NoError(t, err) // session exists → "nudged"
}

// TestNudge_Cancel_CallsNotifierCancel checks that Cancel removes the pending delivery.
func TestNudge_Cancel_CallsNotifierCancel(t *testing.T) {
	r := &fakeNudgeRunner{}
	notifier := notify.New(r, noFocus{})
	n := runnerNudger{notifier: notifier}

	_ = n.Nudge("task-1", "msg-42", "text\n")
	err := n.Cancel("task-1", "msg-42")
	testutil.NoError(t, err)

	state := notifier.DeliveryState("task-1", "msg-42")
	testutil.Equal(t, state, notify.DeliveryState(""))
}

// TestNudge_NilNotifier_CancelIsNoOp verifies cancel with nil notifier does not panic.
func TestNudge_NilNotifier_CancelIsNoOp(t *testing.T) {
	n := runnerNudger{}
	err := n.Cancel("task-1", "msg-1")
	testutil.NoError(t, err)
}

// TestNudge_StripsOuterNewlines verifies that outer newlines are stripped from
// the line before passing it to ReliableNotify (the notifier adds its own CR).
func TestNudge_StripsOuterNewlines(t *testing.T) {
	r := &fakeNudgeRunner{}
	notifier := notify.New(r, noFocus{})
	n := runnerNudger{notifier: notifier}

	// Line with leading+trailing newlines as the old nudge format used.
	_ = n.Nudge("task-1", "msg-1", "\nhello-nudge\n")

	// Add a live idle session so Reconcile can submit.
	sess := r.addSession("task-1", true)
	notifier.Reconcile(time.Now())

	writes := sess.writes
	if len(writes) < 2 {
		t.Fatalf("expected at least 2 writes (ctrl+u + text), got %d; writes=%v", len(writes), writes)
	}
	// writes[0] = ctrl+u, writes[1] = text+CR
	text := string(writes[1])
	if len(text) > 0 && (text[0] == '\n' || text[0] == '\r') {
		t.Errorf("leading newline not stripped, got %q", text)
	}
	if len(text) > 1 && (text[len(text)-2] == '\n') {
		t.Errorf("trailing newline before CR not stripped, got %q", text)
	}
	if !errors.Is(nil, nil) {
		t.Error("sanity check failed") // never reached
	}
}
