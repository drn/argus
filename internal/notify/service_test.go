package notify

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drn/argus/internal/app/agentview"
	"github.com/drn/argus/internal/testutil"
)

// --- fakes ---

// fakeRunner implements RunnerIface for tests.
type fakeRunner struct {
	mu       sync.Mutex
	sessions map[string]*fakeSession
}

func newFakeRunner() *fakeRunner { return &fakeRunner{sessions: make(map[string]*fakeSession)} }

func (r *fakeRunner) Get(taskID string) SessionHandleIface {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.sessions[taskID]
	if s == nil {
		return nil
	}
	return s
}

func (r *fakeRunner) addSession(taskID string, idle bool) *fakeSession {
	s := &fakeSession{idle: idle, writes: [][]byte{}, ackCRAt: 1, ctrlUClears: true}
	r.mu.Lock()
	r.sessions[taskID] = s
	r.mu.Unlock()
	return s
}

// fakeSession implements SessionHandleIface for tests.
type fakeSession struct {
	mu      sync.Mutex
	idle    bool
	tail    []byte
	writes  [][]byte
	origins []agentview.InputOrigin
	total   uint64
	crCount int
	// ackCRAt is the 1-based CR write that produces PTY output. Zero means
	// every CR is swallowed, simulating the live paste-batch failure.
	ackCRAt int
	// writeErr is returned from WriteInput when set.
	writeErr    error
	ctrlUClears bool
}

func (s *fakeSession) IsIdle() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.idle
}

func (s *fakeSession) RecentOutputTail(n int) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n >= len(s.tail) {
		return append([]byte(nil), s.tail...)
	}
	return append([]byte(nil), s.tail[len(s.tail)-n:]...)
}

func (s *fakeSession) TotalWritten() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.total
}

func (s *fakeSession) PTYSize() (cols, rows int) { return 80, 24 }

func (s *fakeSession) WriteInput(p []byte, origin agentview.InputOrigin) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writeErr != nil {
		return 0, s.writeErr
	}
	cp := make([]byte, len(p))
	copy(cp, p)
	s.writes = append(s.writes, cp)
	s.origins = append(s.origins, origin)
	if string(p) == "\r" {
		s.crCount++
		if s.ackCRAt > 0 && s.crCount >= s.ackCRAt {
			s.total++
			if bytes.Contains(s.tail, []byte("❯")) {
				s.tail = composerFrame("")
			}
		}
	} else if string(p) == "\x15" {
		if s.ctrlUClears && bytes.Contains(s.tail, []byte("❯")) {
			s.tail = composerFrame("")
		}
	} else if string(p) != "\x15" {
		// Claude Code redraws its composer after consuming injected text.
		s.total++
		if bytes.Contains(s.tail, []byte("❯")) {
			s.tail = composerFrame(string(p))
		}
	}
	return len(p), nil
}

func composerFrame(draft string) []byte {
	return []byte("\x1b[?1049h\x1b[2J\x1b[7;1H❯\u00a0" + draft)
}

// allOrigins returns a copy of every origin recorded by WriteInput calls, in
// order.
func (s *fakeSession) allOrigins() []agentview.InputOrigin {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]agentview.InputOrigin, len(s.origins))
	copy(out, s.origins)
	return out
}

func (s *fakeSession) allWrites() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]byte, len(s.writes))
	copy(out, s.writes)
	return out
}

// unused by tests but keep time import used:
var _ = time.Now

// fakeNoFocus implements FocusReader – never focused.
type fakeNoFocus struct{}

func (fakeNoFocus) IsFocused(string) bool { return false }

// fakeFocused implements FocusReader – always focused.
type fakeFocused struct{}

func (fakeFocused) IsFocused(string) bool { return true }

// --- helpers ---

func newTestNotifier(runner RunnerIface, focus FocusReader) *Notifier {
	n := New(runner, focus)
	// Unit tests model recipient output synchronously. Keep the production
	// polling functions covered separately without adding seconds to every
	// delivery assertion.
	n.waitForSettled = func(sess SessionHandleIface, baseline uint64, _, _ time.Duration) bool {
		return sess.TotalWritten() > baseline
	}
	n.waitForAdvance = func(sess SessionHandleIface, baseline uint64, _ time.Duration) bool {
		return sess.TotalWritten() > baseline
	}
	n.waitForClear = func(SessionHandleIface, time.Duration) bool { return true }
	n.waitForConsume = func(SessionHandleIface, string, time.Duration) bool { return true }
	return n
}

// --- tests ---

func TestNotifier_ImmediateSubmit(t *testing.T) {
	r := newFakeRunner()
	sess := r.addSession("t1", true) // idle = true
	n := newTestNotifier(r, fakeNoFocus{})

	cancel := n.ReliableNotify("t1", "hello", "d1", NotifyOpts{})
	defer cancel()

	n.Reconcile(time.Now())

	writes := sess.allWrites()
	testutil.Equal(t, len(writes), 3)
	testutil.Equal(t, string(writes[0]), "\x15")
	testutil.Equal(t, string(writes[1]), "hello")
	testutil.Equal(t, string(writes[2]), "\r")
}

// TestNotifier_DeliveryUsesOriginSystem pins the origin-preservation
// contract: reliable-notify delivery must always write with
// agentview.OriginSystem, never agentview.OriginUser, so a delivered
// hera/task message can never masquerade as the user answering a prompt
// (BUG-034).
func TestNotifier_DeliveryUsesOriginSystem(t *testing.T) {
	r := newFakeRunner()
	sess := r.addSession("t1", true) // idle = true
	n := newTestNotifier(r, fakeNoFocus{})

	cancel := n.ReliableNotify("t1", "hello", "d1", NotifyOpts{})
	defer cancel()

	n.Reconcile(time.Now())

	origins := sess.allOrigins()
	testutil.Equal(t, len(origins), 3)
	for _, o := range origins {
		testutil.Equal(t, o, agentview.OriginSystem)
	}
}

func TestNotifier_DeferredWhenBusy(t *testing.T) {
	r := newFakeRunner()
	sess := r.addSession("t1", false) // busy
	n := newTestNotifier(r, fakeNoFocus{})

	cancel := n.ReliableNotify("t1", "hello", "d1", NotifyOpts{})
	defer cancel()

	n.Reconcile(time.Now())
	testutil.Equal(t, len(sess.allWrites()), 0)

	// Now make idle and reconcile again.
	sess.mu.Lock()
	sess.idle = true
	sess.mu.Unlock()
	n.Reconcile(time.Now())
	testutil.Equal(t, len(sess.allWrites()), 3)
}

func TestNotifier_SubmitsWhenPrimaryScreenBecomesContentIdle(t *testing.T) {
	r := newFakeRunner()
	sess := r.addSession("t1", false)
	// No supported composer marker: exercise the conservative content-idle
	// fallback rather than the content-aware fast path.
	sess.tail = []byte("Completed analysis\n\x1b[2K\r✻ Waited for 3s")
	n := newTestNotifier(r, fakeNoFocus{})

	cancel := n.ReliableNotify("t1", "hello", "d1", NotifyOpts{})
	defer cancel()

	t0 := time.Now()
	n.Reconcile(t0)
	testutil.Equal(t, len(sess.allWrites()), 0)

	sess.mu.Lock()
	sess.tail = []byte("Completed analysis\n\x1b[2K\r✶ Waited for 8s")
	sess.mu.Unlock()
	n.Reconcile(t0.Add(4 * time.Second))
	testutil.Equal(t, len(sess.allWrites()), 3)
}

func TestNotifier_DeferredWhenFocused(t *testing.T) {
	r := newFakeRunner()
	sess := r.addSession("t1", true) // idle
	n := newTestNotifier(r, fakeFocused{})

	cancel := n.ReliableNotify("t1", "hello", "d1", NotifyOpts{})
	defer cancel()

	n.Reconcile(time.Now())
	// Focused: no submit.
	testutil.Equal(t, len(sess.allWrites()), 0)
}

func TestNotifier_EmptyComposerBypassesBusyAndFocusGates(t *testing.T) {
	r := newFakeRunner()
	sess := r.addSession("t1", false)
	sess.tail = []byte("\x1b[?1049h\x1b[2J\x1b[7;1H❯\u00a0\x1b[7;3H")
	n := newTestNotifier(r, fakeFocused{})

	n.ReliableNotify("t1", "[hera from coord] msg #1 — hello", "d1", NotifyOpts{})
	n.Reconcile(time.Now())

	writes := sess.allWrites()
	testutil.Equal(t, len(writes), 3)
	testutil.Equal(t, string(writes[0]), "\x15")
	testutil.Equal(t, string(writes[1]), "[hera from coord] msg #1 — hello")
}

func TestNotifier_ChangingComposerDefersUntilStableThenAnnotates(t *testing.T) {
	var logBuf bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(original) })

	r := newFakeRunner()
	sess := r.addSession("t1", true)
	sess.tail = []byte("\x1b[?1049h\x1b[2J\x1b[7;1H❯\u00a0I want\x1b[7;9H")
	n := newTestNotifier(r, fakeNoFocus{})
	n.ReliableNotify("t1", "[hera from coord] msg #2 — review", "d1", NotifyOpts{})

	t0 := time.Now()
	n.Reconcile(t0)
	testutil.Equal(t, len(sess.allWrites()), 0)

	// Forward progress proves active typing and restarts the stability window.
	sess.mu.Lock()
	sess.tail = []byte("\x1b[?1049h\x1b[2J\x1b[7;1H❯\u00a0I want you to\x1b[7;16H")
	sess.mu.Unlock()
	n.Reconcile(t0.Add(draftStabilityWindow))
	testutil.Equal(t, len(sess.allWrites()), 0)

	// The unchanged next snapshot is abandoned input: preserve it, append the
	// warning + notice, and submit without Ctrl+U.
	n.Reconcile(t0.Add(2 * draftStabilityWindow))
	writes := sess.allWrites()
	testutil.Equal(t, len(writes), 2)
	if strings.Contains(string(writes[0]), "\x15") {
		t.Fatal("stable abandoned input must not be cleared")
	}
	testutil.Contains(t, string(writes[0]), "preceding input was left unsubmitted")
	testutil.Contains(t, string(writes[0]), "Do not act on it")
	testutil.Contains(t, string(writes[0]), "[hera from coord] msg #2 — review")
	testutil.Equal(t, string(writes[1]), "\r")
	testutil.Contains(t, logBuf.String(), "delivery composer content stable")
	if strings.Contains(logBuf.String(), "I want you to") {
		t.Fatalf("daemon-visible logs leaked composer text:\n%s", logBuf.String())
	}
}

func TestNotifier_NoticeOnlyComposerIsClearedImmediately(t *testing.T) {
	r := newFakeRunner()
	sess := r.addSession("t1", false)
	sess.tail = []byte("\x1b[?1049h\x1b[2J\x1b[7;1H❯\u00a0[hera from coord] msg #1 — stale\x1b[7;40H")
	n := newTestNotifier(r, fakeFocused{})

	n.ReliableNotify("t1", "[hera from coord] msg #2 — current", "d2", NotifyOpts{})
	n.Reconcile(time.Now())

	writes := sess.allWrites()
	testutil.Equal(t, len(writes), 3)
	testutil.Equal(t, string(writes[0]), "\x15")
	testutil.Equal(t, string(writes[1]), "[hera from coord] msg #2 — current")
}

func TestNotifier_StaleNoticeClearUnconfirmedPreservesWithAnnotation(t *testing.T) {
	r := newFakeRunner()
	sess := r.addSession("t1", false)
	sess.tail = []byte("\x1b[?1049h\x1b[2J\x1b[7;1H❯\u00a0[hera from coord] msg #1 — stale\x1b[7;40H")
	sess.ctrlUClears = false
	n := newTestNotifier(r, fakeFocused{})
	n.waitForClear = func(SessionHandleIface, time.Duration) bool { return false }

	n.ReliableNotify("t1", "[hera from coord] msg #2 — current", "d2", NotifyOpts{})
	n.Reconcile(time.Now())

	writes := sess.allWrites()
	testutil.Equal(t, len(writes), 3)
	testutil.Equal(t, string(writes[0]), "\x15")
	testutil.Contains(t, string(writes[1]), abandonedDraftAnnotation)
	testutil.Contains(t, string(writes[1]), "[hera from coord] msg #2 — current")
	testutil.Equal(t, string(writes[2]), "\r")
}

func TestNotifier_BusyOutputWithoutComposerChangeRemainsPending(t *testing.T) {
	r := newFakeRunner()
	sess := r.addSession("t1", false)
	// The empty, identifiable composer remains unchanged while a busy recipient
	// streams output independently of the injected text and CR.
	sess.tail = []byte("\x1b[?1049h\x1b[2J\x1b[7;1H❯\u00a0\x1b[7;3H")
	sess.ackCRAt = 0
	n := newTestNotifier(r, fakeFocused{})
	n.waitForAdvance = func(SessionHandleIface, uint64, time.Duration) bool { return true }
	n.waitForConsume = func(SessionHandleIface, string, time.Duration) bool {
		sess.mu.Lock()
		sess.total++ // recipient keeps streaming unrelated output
		sess.mu.Unlock()
		return false
	}

	n.ReliableNotify("t1", "[hera from coord] msg #2 — current", "d2", NotifyOpts{})
	n.Reconcile(time.Now())

	writes := sess.allWrites()
	testutil.Equal(t, len(writes), 5) // Ctrl+U, text, then bounded CR retries.
	for _, write := range writes[2:] {
		testutil.Equal(t, string(write), "\r")
	}
	testutil.Equal(t, n.DeliveryState("t1", "d2"), StatePending)
}

func TestWaitForComposerConsume_RejectsBusyOutputWithoutDraftChange(t *testing.T) {
	sess := &fakeSession{
		tail: []byte("\x1b[?1049h\x1b[2J\x1b[7;1H❯\u00a0[hera from coord] msg #2 — current\x1b[7;42H"),
	}
	n := New(newFakeRunner(), fakeNoFocus{})
	draft, known := n.composerDraft(sess)
	testutil.Equal(t, known, true)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 4 {
			time.Sleep(5 * time.Millisecond)
			sess.mu.Lock()
			sess.total++ // recipient is independently streaming output
			sess.mu.Unlock()
		}
	}()

	testutil.Equal(t, n.waitForComposerConsume(sess, draft, 40*time.Millisecond), false)
	<-done
}

func TestNotifier_CancelBeforeSubmit(t *testing.T) {
	r := newFakeRunner()
	sess := r.addSession("t1", true)
	n := newTestNotifier(r, fakeNoFocus{})

	cancel := n.ReliableNotify("t1", "hello", "d1", NotifyOpts{})
	cancel() // cancel before reconcile

	n.Reconcile(time.Now())
	testutil.Equal(t, len(sess.allWrites()), 0)
}

func TestNotifier_DeadlineEvictsDelivery(t *testing.T) {
	r := newFakeRunner()
	sess := r.addSession("t1", false) // busy so it won't submit immediately
	n := newTestNotifier(r, fakeNoFocus{})

	// Use a past deadline.
	_ = n.ReliableNotify("t1", "hello", "d1", NotifyOpts{DeadlineMS: 1})
	time.Sleep(5 * time.Millisecond)

	n.Reconcile(time.Now())
	testutil.Equal(t, len(sess.allWrites()), 0)
	// State should be empty (evicted).
	testutil.Equal(t, n.DeliveryState("t1", "d1"), "")
}

func TestNotifier_DeduplicatePending_SharedCancel(t *testing.T) {
	r := newFakeRunner()
	sess := r.addSession("t1", false) // busy
	n := newTestNotifier(r, fakeNoFocus{})

	cancel1 := n.ReliableNotify("t1", "hello", "d1", NotifyOpts{})
	cancel2 := n.ReliableNotify("t1", "hello", "d1", NotifyOpts{}) // same ID

	// Both cancel funcs cancel the same delivery.
	cancel2() // cancel via the SECOND func
	n.Reconcile(time.Now())
	// No submit — the shared delivery was cancelled.
	testutil.Equal(t, len(sess.allWrites()), 0)
	testutil.Equal(t, n.DeliveryState("t1", "d1"), DeliveryState(""))

	// Calling cancel1 after the delivery is gone is a no-op.
	cancel1()

	// A fresh delivery with a new ID still works.
	_ = n.ReliableNotify("t1", "hello", "d2", NotifyOpts{})
	sess.mu.Lock()
	sess.idle = true
	sess.mu.Unlock()
	n.Reconcile(time.Now())
	testutil.Equal(t, len(sess.allWrites()), 3)
}

func TestNotifier_DeduplicateSubmitted(t *testing.T) {
	r := newFakeRunner()
	sess := r.addSession("t1", true)
	n := newTestNotifier(r, fakeNoFocus{})

	cancel1 := n.ReliableNotify("t1", "hello", "d1", NotifyOpts{})
	defer cancel1()
	n.Reconcile(time.Now())
	testutil.Equal(t, len(sess.allWrites()), 3)

	// Re-post the same deliveryID.
	cancel2 := n.ReliableNotify("t1", "hello", "d1", NotifyOpts{})
	defer cancel2()
	n.Reconcile(time.Now())
	// Still only 3 writes (no second submission).
	testutil.Equal(t, len(sess.allWrites()), 3)
}

func TestNotifier_NoSession_DeferDelivery(t *testing.T) {
	r := newFakeRunner() // no session added
	n := newTestNotifier(r, fakeNoFocus{})

	cancel := n.ReliableNotify("t1", "hello", "d1", NotifyOpts{})
	defer cancel()

	n.Reconcile(time.Now())
	// Delivery should still be pending (no error, no submit).
	testutil.Equal(t, n.DeliveryState("t1", "d1"), StatePending)
}

func TestNotifier_Cancel_RemovesDelivery(t *testing.T) {
	r := newFakeRunner()
	r.addSession("t1", false) // busy
	n := newTestNotifier(r, fakeNoFocus{})

	n.ReliableNotify("t1", "hello", "d1", NotifyOpts{})
	n.Cancel("t1", "d1")

	testutil.Equal(t, n.DeliveryState("t1", "d1"), "")
}

func TestNotifier_Cancel_UnknownIsNoOp(t *testing.T) {
	r := newFakeRunner()
	n := newTestNotifier(r, fakeNoFocus{})
	// Should not panic.
	n.Cancel("nonexistent", "d1")
}

func TestNotifier_SerializeConcurrentDeliveries(t *testing.T) {
	r := newFakeRunner()
	sess := r.addSession("t1", true)
	n := newTestNotifier(r, fakeNoFocus{})

	// Register two deliveries for the same task.
	cancel1 := n.ReliableNotify("t1", "first", "d1", NotifyOpts{})
	cancel2 := n.ReliableNotify("t1", "second", "d2", NotifyOpts{})
	defer cancel1()
	defer cancel2()

	// First reconcile submits d1.
	n.Reconcile(time.Now())
	writes1 := sess.allWrites()
	testutil.Equal(t, len(writes1), 3) // ctrl+u + "first" + CR

	// Second reconcile submits d2.
	n.Reconcile(time.Now())
	writes2 := sess.allWrites()
	testutil.Equal(t, len(writes2), 6) // three more writes for "second"
	testutil.Equal(t, string(writes2[3]), "\x15")
	testutil.Equal(t, string(writes2[4]), "second")
	testutil.Equal(t, string(writes2[5]), "\r")
}

func TestNotifier_SessionExists(t *testing.T) {
	r := newFakeRunner()
	n := newTestNotifier(r, fakeNoFocus{})
	testutil.Equal(t, n.SessionExists("t1"), false)

	r.addSession("t1", true)
	testutil.Equal(t, n.SessionExists("t1"), true)
}

func TestNotifier_DeliveryState_ReturnsCorrectValues(t *testing.T) {
	r := newFakeRunner()
	r.addSession("t1", false)
	n := newTestNotifier(r, fakeNoFocus{})

	testutil.Equal(t, n.DeliveryState("t1", "d1"), "")

	n.ReliableNotify("t1", "hello", "d1", NotifyOpts{})
	testutil.Equal(t, n.DeliveryState("t1", "d1"), StatePending)
}

func TestNotifier_ReconcileSkipsNonIdleSessions(t *testing.T) {
	r := newFakeRunner()
	sess := r.addSession("t1", false) // busy
	n := newTestNotifier(r, fakeNoFocus{})

	n.ReliableNotify("t1", "hello", "d1", NotifyOpts{})
	n.Reconcile(time.Now())

	testutil.Equal(t, len(sess.allWrites()), 0)
	testutil.Equal(t, n.DeliveryState("t1", "d1"), StatePending)
}

func TestNotifier_PreClear_WritesCtrlU(t *testing.T) {
	r := newFakeRunner()
	sess := r.addSession("t1", true)
	n := newTestNotifier(r, fakeNoFocus{})

	n.ReliableNotify("t1", "text", "d1", NotifyOpts{})
	n.Reconcile(time.Now())

	writes := sess.allWrites()
	testutil.Equal(t, len(writes), 3)
	testutil.Equal(t, writes[0][0], byte(0x15)) // Ctrl+U
	testutil.Equal(t, string(writes[1]), "text")
	testutil.Equal(t, string(writes[2]), "\r")
}

// TestNotifier_SubmitThreeOrderedWrites verifies that submit issues exactly three
// WriteInput calls in order: Ctrl+U (0x15), then the text WITHOUT a trailing CR,
// then a standalone CR (0x0D). The gap between writes 2 and 3 exists in production
// but is not observable here because fakeSession.WriteInput is synchronous.
//
// NOTE: whether the CR is interpreted as "Enter" vs "paste continuation" depends
// on the target shell/agent's line-discipline and cannot be verified with this
// byte-level fake. Real-TUI submit correctness must be verified empirically after
// deploy by confirming the message appears in the agent's conversation.
func TestNotifier_SubmitThreeOrderedWrites(t *testing.T) {
	r := newFakeRunner()
	sess := r.addSession("t1", true) // idle
	n := newTestNotifier(r, fakeNoFocus{})

	n.ReliableNotify("t1", "the message", "d1", NotifyOpts{})
	n.Reconcile(time.Now())

	writes := sess.allWrites()
	testutil.Equal(t, len(writes), 3)
	// Write 1: Ctrl+U line-kill.
	testutil.Equal(t, string(writes[0]), "\x15")
	// Write 2: text without trailing CR.
	testutil.Equal(t, string(writes[1]), "the message")
	// Write 3: standalone CR — not appended to write 2.
	testutil.Equal(t, string(writes[2]), "\r")
}

func TestNotifier_RetriesCRWhenRecipientSwallowsFirstSubmit(t *testing.T) {
	r := newFakeRunner()
	sess := r.addSession("t1", true)
	sess.ackCRAt = 2
	n := newTestNotifier(r, fakeNoFocus{})

	n.ReliableNotify("t1", "slow recipient", "d1", NotifyOpts{})
	n.Reconcile(time.Now())

	writes := sess.allWrites()
	testutil.Equal(t, len(writes), 4)
	testutil.Equal(t, string(writes[0]), "\x15")
	testutil.Equal(t, string(writes[1]), "slow recipient")
	testutil.Equal(t, string(writes[2]), "\r")
	testutil.Equal(t, string(writes[3]), "\r")
	testutil.Equal(t, n.DeliveryState("t1", "d1"), StateSubmitted)
}

func TestNotifier_UnacknowledgedCRRemainsPending(t *testing.T) {
	r := newFakeRunner()
	sess := r.addSession("t1", true)
	sess.ackCRAt = 0
	n := newTestNotifier(r, fakeNoFocus{})

	n.ReliableNotify("t1", "never consumed", "d1", NotifyOpts{})
	n.Reconcile(time.Now())

	writes := sess.allWrites()
	testutil.Equal(t, len(writes), 5)
	testutil.Equal(t, string(writes[0]), "\x15")
	testutil.Equal(t, string(writes[1]), "never consumed")
	for _, write := range writes[2:] {
		testutil.Equal(t, string(write), "\r")
	}
	testutil.Equal(t, n.DeliveryState("t1", "d1"), StatePending)
}

func TestNotifier_LogsSubmitRetriesAndSuccessToStructuredLogger(t *testing.T) {
	var buf bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(original) })

	r := newFakeRunner()
	sess := r.addSession("task-log", true)
	sess.ackCRAt = 2
	n := newTestNotifier(r, fakeNoFocus{})

	n.ReliableNotify("task-log", "hello", "delivery-log", NotifyOpts{})
	n.Reconcile(time.Now())

	output := buf.String()
	for _, want := range []string{
		"[notify] delivery enter unacknowledged",
		"[notify] delivery submitted",
		"task=task-log",
		"delivery=delivery-log",
		"attempt=2",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("structured log missing %q:\n%s", want, output)
		}
	}
}

func TestNotifier_LogsWriteFailureToStructuredLogger(t *testing.T) {
	var buf bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(original) })

	r := newFakeRunner()
	sess := r.addSession("task-fail", true)
	sess.writeErr = fmt.Errorf("pty closed")
	n := newTestNotifier(r, fakeNoFocus{})

	n.ReliableNotify("task-fail", "hello", "delivery-fail", NotifyOpts{})
	n.Reconcile(time.Now())

	output := buf.String()
	for _, want := range []string{
		"[notify] delivery write failed",
		"task=task-fail",
		"delivery=delivery-fail",
		"phase=ctrl+u",
		`error="pty closed"`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("structured log missing %q:\n%s", want, output)
		}
	}
}

func TestWaitForOutputSettled_WaitsForLateRecipientActivity(t *testing.T) {
	sess := &fakeSession{}
	baseline := sess.TotalWritten()

	go func() {
		time.Sleep(20 * time.Millisecond)
		sess.mu.Lock()
		sess.total++
		sess.mu.Unlock()
	}()

	started := time.Now()
	settled := waitForOutputSettled(sess, baseline, 200*time.Millisecond, 15*time.Millisecond)
	testutil.Equal(t, settled, true)
	if elapsed := time.Since(started); elapsed < 30*time.Millisecond {
		t.Fatalf("settled before late recipient activity became quiet: %s", elapsed)
	}
}

func TestWaitForOutputAdvance_DetectsLateSubmitAcknowledgment(t *testing.T) {
	sess := &fakeSession{}
	baseline := sess.TotalWritten()

	go func() {
		time.Sleep(20 * time.Millisecond)
		sess.mu.Lock()
		sess.total++
		sess.mu.Unlock()
	}()

	testutil.Equal(t, waitForOutputAdvance(sess, baseline, 200*time.Millisecond), true)
}

func TestNotifier_ConcurrentReconcileDoesNotDuplicateInFlightDelivery(t *testing.T) {
	r := newFakeRunner()
	sess := r.addSession("t1", true)
	n := newTestNotifier(r, fakeNoFocus{})

	settling := make(chan struct{})
	release := make(chan struct{})
	n.waitForSettled = func(SessionHandleIface, uint64, time.Duration, time.Duration) bool {
		close(settling)
		<-release
		return true
	}

	n.ReliableNotify("t1", "one delivery", "d1", NotifyOpts{})
	done := make(chan struct{})
	go func() {
		n.Reconcile(time.Now())
		close(done)
	}()
	<-settling

	// A daemon tick racing an inline REST reconcile must see the submit claim
	// and return without a second Ctrl+U/text sequence.
	n.Reconcile(time.Now())
	testutil.Equal(t, len(sess.allWrites()), 2)

	close(release)
	<-done
	testutil.Equal(t, len(sess.allWrites()), 3)
	testutil.Equal(t, n.DeliveryState("t1", "d1"), StateSubmitted)
}

func TestNotifier_FocusLifts_PendingDeliverySubmits(t *testing.T) {
	r := newFakeRunner()
	sess := r.addSession("t1", true) // idle
	ft := NewFocusTracker(nil)
	ft.SetFocused("t1", true) // human focused initially
	n := newTestNotifier(r, ft)

	n.ReliableNotify("t1", "hello", "d1", NotifyOpts{})
	n.Reconcile(time.Now())
	testutil.Equal(t, len(sess.allWrites()), 0) // blocked by focus

	ft.SetFocused("t1", false) // human leaves
	n.Reconcile(time.Now())
	testutil.Equal(t, len(sess.allWrites()), 3) // ctrl+u + text + CR
}

func TestNotifier_WriteInputCtrlUFailure_DeliveryRemainesPending(t *testing.T) {
	r := newFakeRunner()
	sess := r.addSession("t1", true)
	sess.writeErr = fmt.Errorf("write error")
	n := newTestNotifier(r, fakeNoFocus{})

	n.ReliableNotify("t1", "hello", "d1", NotifyOpts{})
	n.Reconcile(time.Now())

	// No writes because ctrl+u failed.
	testutil.Equal(t, len(sess.allWrites()), 0)
	// Delivery should still be pending for retry.
	testutil.Equal(t, n.DeliveryState("t1", "d1"), StatePending)
}

func TestNotifier_CancelActiveDelivery_PromotesQueued(t *testing.T) {
	r := newFakeRunner()
	sess := r.addSession("t1", false) // busy — no immediate submit
	n := newTestNotifier(r, fakeNoFocus{})

	// Register d1 (active), d2 (queued).
	n.ReliableNotify("t1", "first", "d1", NotifyOpts{})
	n.ReliableNotify("t1", "second", "d2", NotifyOpts{})

	// Cancel d1; d2 should be promoted to active.
	n.Cancel("t1", "d1")
	testutil.Equal(t, n.DeliveryState("t1", "d1"), DeliveryState(""))
	testutil.Equal(t, n.DeliveryState("t1", "d2"), StatePending)

	// Make session idle; reconcile should submit d2.
	sess.mu.Lock()
	sess.idle = true
	sess.mu.Unlock()
	n.Reconcile(time.Now())
	testutil.Equal(t, len(sess.allWrites()), 3)
	testutil.Equal(t, string(sess.allWrites()[1]), "second")
	testutil.Equal(t, string(sess.allWrites()[2]), "\r")
}

func TestNotifier_CancelQueuedDelivery(t *testing.T) {
	r := newFakeRunner()
	r.addSession("t1", false) // busy
	n := newTestNotifier(r, fakeNoFocus{})

	// d1 active, d2 queued.
	n.ReliableNotify("t1", "first", "d1", NotifyOpts{})
	n.ReliableNotify("t1", "second", "d2", NotifyOpts{})

	// Cancel the queued one — d1 stays active, d2 gone.
	n.Cancel("t1", "d2")
	testutil.Equal(t, n.DeliveryState("t1", "d1"), StatePending)
	testutil.Equal(t, n.DeliveryState("t1", "d2"), DeliveryState(""))
}
