package notify

import (
	"sync"
	"time"

	"github.com/drn/argus/internal/uxlog"
)

// Notifier is the reliable pane-delivery service. It accepts text deliveries
// keyed by (taskID, deliveryID), deduplicates, gates on idle+unfocused, and
// submits exactly once via Ctrl+U + text + CR.
//
// One Notifier is created per daemon. The TUI may also create one for
// in-process mode. Reconcile must be called periodically (the idleWatcher
// 5-second tick is the intended driver).
type Notifier struct {
	mu      sync.Mutex
	pending map[string]*delivery         // taskID → active delivery (one per task)
	queue   map[string][]*delivery       // taskID → queued deliveries (second and beyond)
	cancels map[string]map[string]func() // taskID → deliveryID → cancel func (live deliveries only)
	subKeys map[string][]string          // taskID → ordered submitted deliveryID list (FIFO eviction)
	subSet  map[string]map[string]bool   // taskID → submitted deliveryID set
	runner  RunnerIface
	focus   FocusReader
}

// New creates a Notifier. runner and focus must be non-nil.
func New(runner RunnerIface, focus FocusReader) *Notifier {
	return &Notifier{
		pending: make(map[string]*delivery),
		queue:   make(map[string][]*delivery),
		cancels: make(map[string]map[string]func()),
		subKeys: make(map[string][]string),
		subSet:  make(map[string]map[string]bool),
		runner:  runner,
		focus:   focus,
	}
}

// ReliableNotify registers a delivery of text to taskID. Returns a cancel func
// the caller invokes to abandon the delivery. The cancel func is safe to call
// after submission (it becomes a no-op).
//
// Dedup rules:
//   - If deliveryID was already submitted for taskID → returns a no-op cancel immediately.
//   - If deliveryID is already pending for taskID → returns the existing cancel.
//   - Otherwise registers a new delivery.
func (n *Notifier) ReliableNotify(taskID, text, deliveryID string, opts NotifyOpts) func() {
	deadlineMS := opts.DeadlineMS
	if deadlineMS <= 0 {
		deadlineMS = defaultDeadlineMS
	}
	deadline := time.Now().Add(time.Duration(deadlineMS) * time.Millisecond)

	n.mu.Lock()
	defer n.mu.Unlock()

	// Already submitted? Return no-op.
	if n.isSubmitted(taskID, deliveryID) {
		return func() {}
	}

	// Already pending or queued? Return the shared cancel func so every caller
	// that posted the same deliveryID can cancel the same delivery.
	if existing := n.storedCancel(taskID, deliveryID); existing != nil {
		return existing
	}

	d := &delivery{
		taskID:     taskID,
		text:       text,
		deliveryID: deliveryID,
		deadline:   deadline,
		cancelCh:   make(chan struct{}),
	}
	cancelFn := makeCancelFn(d.cancelCh)
	n.storeCancel(taskID, deliveryID, cancelFn)

	if n.pending[taskID] == nil {
		n.pending[taskID] = d
	} else {
		n.queue[taskID] = append(n.queue[taskID], d)
	}

	return cancelFn
}

// makeCancelFn returns a cancel func that closes ch exactly once using
// a sync.Once so it is safe to call multiple times.
func makeCancelFn(ch chan struct{}) func() {
	var once sync.Once
	return func() { once.Do(func() { close(ch) }) }
}

// Cancel abandons a pending delivery by its (taskID, deliveryID). Immediately
// removes the delivery from the pending set. Safe to call after submission
// (no-op) or for unknown IDs (no-op).
func (n *Notifier) Cancel(taskID, deliveryID string) {
	n.mu.Lock()
	defer n.mu.Unlock()

	// Check and cancel active pending.
	if p := n.pending[taskID]; p != nil && p.deliveryID == deliveryID {
		select {
		case <-p.cancelCh:
		default:
			close(p.cancelCh)
		}
		delete(n.pending, taskID)
		n.dropCancel(taskID, deliveryID)
		// Promote next queued.
		if q := n.queue[taskID]; len(q) > 0 {
			n.pending[taskID] = q[0]
			n.queue[taskID] = q[1:]
			if len(n.queue[taskID]) == 0 {
				delete(n.queue, taskID)
			}
		}
		return
	}
	// Check queued.
	queue := n.queue[taskID]
	for i, q := range queue {
		if q.deliveryID == deliveryID {
			select {
			case <-q.cancelCh:
			default:
				close(q.cancelCh)
			}
			n.queue[taskID] = append(queue[:i], queue[i+1:]...)
			if len(n.queue[taskID]) == 0 {
				delete(n.queue, taskID)
			}
			n.dropCancel(taskID, deliveryID)
			return
		}
	}
}

// Reconcile processes all pending deliveries. It should be called on each
// idleWatcher tick (typically every 5 seconds).
func (n *Notifier) Reconcile(now time.Time) {
	n.mu.Lock()
	// Build a snapshot of (taskID, delivery) pairs to process outside the lock.
	type work struct {
		taskID string
		d      *delivery
	}
	var items []work
	for taskID, d := range n.pending {
		items = append(items, work{taskID, d})
	}
	n.mu.Unlock()

	for _, w := range items {
		n.processOne(w.taskID, w.d, now)
	}
}

// processOne attempts to submit one delivery. Called without the lock held;
// re-locks internally for state mutations.
func (n *Notifier) processOne(taskID string, d *delivery, now time.Time) {
	// Check cancelled.
	select {
	case <-d.cancelCh:
		n.removeAndAdvance(taskID, d.deliveryID, false)
		uxlog.Log("[notify] delivery cancelled task=%s id=%s", taskID, d.deliveryID)
		return
	default:
	}

	// Check deadline.
	if now.After(d.deadline) {
		n.removeAndAdvance(taskID, d.deliveryID, false)
		uxlog.Log("[notify] delivery deadline exceeded task=%s id=%s", taskID, d.deliveryID)
		return
	}

	// Check session. The runner returns nil when no live session exists.
	sess := n.runner.Get(taskID)
	if sess == nil {
		uxlog.Log("[notify] delivery skip: no session task=%s id=%s", taskID, d.deliveryID)
		return
	}

	// Check idle.
	if !sess.IsIdle() {
		uxlog.Log("[notify] delivery skip: session busy task=%s id=%s", taskID, d.deliveryID)
		return
	}

	// Check focus.
	if n.focus.IsFocused(taskID) {
		uxlog.Log("[notify] delivery skip: human focused task=%s id=%s", taskID, d.deliveryID)
		return
	}

	// All gates passed: submit.
	// Three separate WriteInputSystem calls, with a brief pause before the third
	// (system path: advances the work cycle but not the user-input timestamp, so
	// the delivery never clears a needs-input "(?)" flag — BUG-034):
	//   1. Ctrl+U – kill any stale partial input
	//   2. text   – prime the input buffer (no trailing CR)
	//      (submitCRDelay pause here — ensures CR arrives as a distinct keypress)
	//   3. \r     – submit; must be its own call, never appended to text
	// The glued ctrl+u+text+CR sequence leaves the line un-submitted in some
	// shell/agent configurations; the separated form is empirically reliable.
	if _, err := sess.WriteInputSystem([]byte("\x15")); err != nil {
		uxlog.Log("[notify] delivery ctrl+u failed task=%s id=%s err=%v", taskID, d.deliveryID, err)
		return
	}
	if _, err := sess.WriteInputSystem([]byte(d.text)); err != nil {
		uxlog.Log("[notify] delivery text write failed task=%s id=%s err=%v", taskID, d.deliveryID, err)
		return
	}
	// processOne runs without n.mu held; this sleep is safe.
	time.Sleep(submitCRDelay)
	if _, err := sess.WriteInputSystem([]byte("\r")); err != nil {
		uxlog.Log("[notify] delivery enter write failed task=%s id=%s err=%v", taskID, d.deliveryID, err)
		// Text is now in the buffer without a CR. The next Reconcile will
		// ctrl+U (clearing the stale text) then retry the full sequence.
		return
	}

	uxlog.Log("[notify] delivery submitted task=%s id=%s", taskID, d.deliveryID)
	n.removeAndAdvance(taskID, d.deliveryID, true)
}

// removeAndAdvance removes the named delivery from pending (marking it
// submitted if submitted=true), then promotes the next queued delivery.
func (n *Notifier) removeAndAdvance(taskID, deliveryID string, submitted bool) {
	n.mu.Lock()
	defer n.mu.Unlock()

	// Verify it's still the active one.
	p := n.pending[taskID]
	if p == nil || p.deliveryID != deliveryID {
		return
	}
	delete(n.pending, taskID)
	n.dropCancel(taskID, deliveryID)

	if submitted {
		n.markSubmitted(taskID, deliveryID)
	}

	// Promote next queued delivery.
	if q := n.queue[taskID]; len(q) > 0 {
		n.pending[taskID] = q[0]
		n.queue[taskID] = q[1:]
		if len(n.queue[taskID]) == 0 {
			delete(n.queue, taskID)
		}
	}
}

// storeCancel records the cancel func for a live (pending/queued) delivery.
// Caller must hold n.mu.
func (n *Notifier) storeCancel(taskID, deliveryID string, fn func()) {
	if n.cancels[taskID] == nil {
		n.cancels[taskID] = make(map[string]func())
	}
	n.cancels[taskID][deliveryID] = fn
}

// storedCancel returns the cancel func for a pending/queued delivery, or nil
// if no live delivery for that (taskID, deliveryID) pair exists.
// Caller must hold n.mu.
func (n *Notifier) storedCancel(taskID, deliveryID string) func() {
	if m := n.cancels[taskID]; m != nil {
		return m[deliveryID]
	}
	return nil
}

// dropCancel removes the stored cancel func for a delivery that has been
// submitted or cancelled. Caller must hold n.mu.
func (n *Notifier) dropCancel(taskID, deliveryID string) {
	if m := n.cancels[taskID]; m != nil {
		delete(m, deliveryID)
		if len(m) == 0 {
			delete(n.cancels, taskID)
		}
	}
}

// isSubmitted returns whether deliveryID was already submitted for taskID.
// Caller must hold n.mu.
func (n *Notifier) isSubmitted(taskID, deliveryID string) bool {
	s, ok := n.subSet[taskID]
	if !ok {
		return false
	}
	return s[deliveryID]
}

// markSubmitted records deliveryID as submitted for taskID, evicting the
// oldest entry if the per-task cap is exceeded.
// Caller must hold n.mu.
func (n *Notifier) markSubmitted(taskID, deliveryID string) {
	if n.subSet[taskID] == nil {
		n.subSet[taskID] = make(map[string]bool)
	}
	if n.subSet[taskID][deliveryID] {
		return // already recorded
	}
	n.subSet[taskID][deliveryID] = true
	n.subKeys[taskID] = append(n.subKeys[taskID], deliveryID)
	// Evict oldest if over cap.
	for len(n.subKeys[taskID]) > maxSubmittedPerTask {
		oldest := n.subKeys[taskID][0]
		n.subKeys[taskID] = n.subKeys[taskID][1:]
		delete(n.subSet[taskID], oldest)
	}
}

// SessionExists returns true when the runner reports a live session for taskID.
// Used by callers that want to distinguish "delivery registered, session live"
// (likely to submit soon) from "delivery registered, no session yet" (queued
// until the session starts).
func (n *Notifier) SessionExists(taskID string) bool {
	return n.runner.Get(taskID) != nil
}

// DeliveryState returns the current state of a delivery. "submitted" means
// already completed, "pending" means currently queued, "" means unknown.
func (n *Notifier) DeliveryState(taskID, deliveryID string) DeliveryState {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.isSubmitted(taskID, deliveryID) {
		return StateSubmitted
	}
	if p := n.pending[taskID]; p != nil && p.deliveryID == deliveryID {
		return StatePending
	}
	for _, q := range n.queue[taskID] {
		if q.deliveryID == deliveryID {
			return StatePending
		}
	}
	return ""
}
