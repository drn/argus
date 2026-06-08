package notify

import (
	"fmt"
	"sync"
	"testing"
	"time"

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
	s := &fakeSession{idle: idle, writes: [][]byte{}}
	r.mu.Lock()
	r.sessions[taskID] = s
	r.mu.Unlock()
	return s
}

// fakeSession implements SessionHandleIface for tests.
type fakeSession struct {
	mu     sync.Mutex
	idle   bool
	writes [][]byte
	// writeErr is returned from WriteInput when set.
	writeErr error
}

func (s *fakeSession) IsIdle() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.idle
}

func (s *fakeSession) WriteInput(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writeErr != nil {
		return 0, s.writeErr
	}
	cp := make([]byte, len(p))
	copy(cp, p)
	s.writes = append(s.writes, cp)
	return len(p), nil
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
	return New(runner, focus)
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
