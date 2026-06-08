package notify

import (
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
	testutil.Equal(t, len(writes), 2)
	testutil.Equal(t, string(writes[0]), "\x15")
	testutil.Equal(t, string(writes[1]), "hello\r")
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
	testutil.Equal(t, len(sess.allWrites()), 2)
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

func TestNotifier_DeduplicatePending(t *testing.T) {
	r := newFakeRunner()
	sess := r.addSession("t1", false) // busy
	n := newTestNotifier(r, fakeNoFocus{})

	cancel1 := n.ReliableNotify("t1", "hello", "d1", NotifyOpts{})
	cancel2 := n.ReliableNotify("t1", "hello", "d1", NotifyOpts{}) // same ID
	defer cancel1()
	defer cancel2()

	// Should only be one pending delivery.
	testutil.Equal(t, n.DeliveryState("t1", "d1"), StatePending)

	// Make idle and submit.
	sess.mu.Lock()
	sess.idle = true
	sess.mu.Unlock()
	n.Reconcile(time.Now())

	// Exactly one submit (two writes: ctrl+u + text).
	testutil.Equal(t, len(sess.allWrites()), 2)
}

func TestNotifier_DeduplicateSubmitted(t *testing.T) {
	r := newFakeRunner()
	sess := r.addSession("t1", true)
	n := newTestNotifier(r, fakeNoFocus{})

	cancel1 := n.ReliableNotify("t1", "hello", "d1", NotifyOpts{})
	defer cancel1()
	n.Reconcile(time.Now())
	testutil.Equal(t, len(sess.allWrites()), 2)

	// Re-post the same deliveryID.
	cancel2 := n.ReliableNotify("t1", "hello", "d1", NotifyOpts{})
	defer cancel2()
	n.Reconcile(time.Now())
	// Still only 2 writes (no second submission).
	testutil.Equal(t, len(sess.allWrites()), 2)
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
	testutil.Equal(t, len(writes1), 2) // ctrl+u + "first\r"

	// Second reconcile submits d2.
	n.Reconcile(time.Now())
	writes2 := sess.allWrites()
	testutil.Equal(t, len(writes2), 4) // two more writes for "second"
	testutil.Equal(t, string(writes2[2]), "\x15")
	testutil.Equal(t, string(writes2[3]), "second\r")
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
	testutil.Equal(t, len(writes), 2)
	testutil.Equal(t, writes[0][0], byte(0x15)) // Ctrl+U
	testutil.Equal(t, string(writes[1]), "text\r")
}
