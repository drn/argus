package notify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/app/agentview"
	"github.com/drn/argus/internal/uxlog"
)

// Notifier is the reliable pane-delivery service. It accepts text deliveries
// keyed by (taskID, deliveryID), deduplicates, protects actively changing
// composer input, and submits exactly once via text + acknowledged CR.
//
// One Notifier is created per daemon. The TUI may also create one for
// in-process mode. Reconcile must be called periodically (the idleWatcher
// 5-second tick is the intended driver).
type Notifier struct {
	mu       sync.Mutex
	pending  map[string]*delivery         // taskID → active delivery (one per task)
	queue    map[string][]*delivery       // taskID → queued deliveries (second and beyond)
	cancels  map[string]map[string]func() // taskID → deliveryID → cancel func (live deliveries only)
	inFlight map[string]string            // taskID → deliveryID currently processing
	subKeys  map[string][]string          // taskID → ordered submitted deliveryID list (FIFO eviction)
	subSet   map[string]map[string]bool   // taskID → submitted deliveryID set
	runner   RunnerIface
	focus    FocusReader
	idle     agent.ContentIdleTracker
	screenMu sync.Mutex
	screen   agent.ScreenRenderer

	// Output and composer wait seams keep timing-dependent delivery behavior
	// deterministic in tests. Production uses the polling helpers below.
	waitForSettled func(SessionHandleIface, uint64, time.Duration, time.Duration) bool
	waitForAdvance func(SessionHandleIface, uint64, time.Duration) bool
	waitForClear   func(SessionHandleIface, time.Duration) bool
	waitForConsume func(SessionHandleIface, string, time.Duration) bool
}

// New creates a Notifier. runner and focus must be non-nil.
func New(runner RunnerIface, focus FocusReader) *Notifier {
	n := &Notifier{
		pending:        make(map[string]*delivery),
		queue:          make(map[string][]*delivery),
		cancels:        make(map[string]map[string]func()),
		inFlight:       make(map[string]string),
		subKeys:        make(map[string][]string),
		subSet:         make(map[string]map[string]bool),
		runner:         runner,
		focus:          focus,
		waitForSettled: waitForOutputSettled,
		waitForAdvance: waitForOutputAdvance,
	}
	n.waitForClear = n.waitForComposerClear
	n.waitForConsume = n.waitForComposerConsume
	return n
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
		} else {
			n.idle.Forget(taskID)
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
		if n.inFlight[taskID] != "" {
			continue
		}
		n.inFlight[taskID] = d.deliveryID
		items = append(items, work{taskID, d})
	}
	n.mu.Unlock()

	for _, w := range items {
		func() {
			defer n.finishProcessing(w.taskID, w.d.deliveryID)
			n.processOne(w.taskID, w.d, now)
		}()
	}
}

// finishProcessing releases the per-task submit claim. Reconcile can be
// invoked both by the daemon tick and inline REST requests, so this guard is
// what keeps their longer acknowledgment waits from submitting one delivery
// concurrently.
func (n *Notifier) finishProcessing(taskID, deliveryID string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.inFlight[taskID] == deliveryID {
		delete(n.inFlight, taskID)
	}
}

// processOne attempts to submit one delivery. Called without the lock held;
// re-locks internally for state mutations.
func (n *Notifier) processOne(taskID string, d *delivery, now time.Time) {
	// Check cancelled.
	select {
	case <-d.cancelCh:
		n.removeAndAdvance(taskID, d.deliveryID, false)
		logDelivery(slog.LevelInfo, "delivery cancelled", taskID, d.deliveryID)
		return
	default:
	}

	// Check deadline.
	if now.After(d.deadline) {
		n.removeAndAdvance(taskID, d.deliveryID, false)
		logDelivery(slog.LevelWarn, "delivery deadline exceeded", taskID, d.deliveryID)
		return
	}

	// Check session. The runner returns nil when no live session exists.
	sess := n.runner.Get(taskID)
	if sess == nil {
		logDelivery(slog.LevelDebug, "delivery skip: no session", taskID, d.deliveryID)
		return
	}

	clearDraft, verifyClear, composerKnown, payload, safe := n.deliveryInput(taskID, d, sess, now)
	if !safe {
		return
	}

	// All gates passed: submit. Every write uses system origin: it advances the
	// work cycle but not the user-input timestamp, so delivery never clears a
	// needs-input "(?)" flag (BUG-034).
	//
	// The text and CR must remain separate, but separation alone is not enough:
	// a loaded recipient may take longer than the former fixed 50ms delay to
	// consume the text. Observe composer output settling before Enter, then
	// require fresh output after Enter. A swallowed Enter is retried without
	// rewriting the text; unacknowledged delivery stays pending.
	//   1. optional Ctrl+U – clear an empty or notice-only composer
	//   2. text – notice, or annotation + notice after abandoned input
	//   3. wait for recipient output to settle
	//   4. \r – submit; retry standalone CR until acknowledged
	if clearDraft {
		if _, err := sess.WriteInput([]byte("\x15"), agentview.OriginSystem); err != nil {
			logDelivery(slog.LevelWarn, "delivery write failed", taskID, d.deliveryID, "phase", "ctrl+u", "error", err)
			return
		}
		if verifyClear && !n.waitForClear(sess, submitAckTimeouts[0]) {
			// A successful PTY write says nothing about editor semantics. Preserve
			// an uncleared notice rather than gluing the replacement onto it.
			logDelivery(slog.LevelWarn, "delivery stale notice clear unconfirmed: preserving", taskID, d.deliveryID)
			payload = "\n\n" + abandonedDraftAnnotation + "\n" + d.text
		}
	}
	textBaseline := sess.TotalWritten()
	if _, err := sess.WriteInput([]byte(payload), agentview.OriginSystem); err != nil {
		logDelivery(slog.LevelWarn, "delivery write failed", taskID, d.deliveryID, "phase", "text", "error", err)
		return
	}
	if !n.waitForSettled(sess, textBaseline, textSettleTimeout, textQuietWindow) {
		logDelivery(slog.LevelWarn, "delivery text settle unconfirmed", taskID, d.deliveryID)
	}

	// An identifiable composer needs a visible snapshot of the injected text
	// before its disappearance can acknowledge CR. In particular, a stale empty
	// frame plus unrelated streaming output cannot prove the input was consumed.
	submittedDraft := ""
	composerAckable := !composerKnown
	if composerKnown {
		if draft, known := n.composerDraft(sess); known && composerContainsText(draft, d.text) {
			submittedDraft = draft
			composerAckable = true
		} else {
			logDelivery(slog.LevelWarn, "delivery injected composer unobserved", taskID, d.deliveryID)
		}
	}

	for i, timeout := range submitAckTimeouts {
		if n.deliveryStopped(taskID, d) {
			return
		}

		attempt := i + 1
		d.submitAttempts++
		totalAttempts := d.submitAttempts
		baseline := sess.TotalWritten()
		if _, err := sess.WriteInput([]byte("\r"), agentview.OriginSystem); err != nil {
			logDelivery(slog.LevelWarn, "delivery write failed", taskID, d.deliveryID,
				"phase", "enter", "attempt", attempt, "total_attempts", totalAttempts, "error", err)
			if totalAttempts >= maxTotalSubmitAttempts {
				n.removeAndAdvance(taskID, d.deliveryID, false)
				logDelivery(slog.LevelError, "delivery abandoned: total enter attempts exceeded", taskID, d.deliveryID,
					"total_attempts", totalAttempts, "max_total_attempts", maxTotalSubmitAttempts)
			}
			return
		}
		acknowledged := false
		if composerKnown && composerAckable {
			acknowledged = n.waitForConsume(sess, submittedDraft, timeout)
		} else {
			// A rendered composer is authoritative only after it reflected the
			// injected draft. Unknown and known-but-unobservable composers both
			// need the conservative raw-output fallback.
			acknowledged = n.waitForAdvance(sess, baseline, timeout)
		}
		if acknowledged {
			logDelivery(slog.LevelInfo, "delivery submitted", taskID, d.deliveryID, "attempt", attempt)
			n.removeAndAdvance(taskID, d.deliveryID, true)
			return
		}

		logDelivery(slog.LevelWarn, "delivery enter unacknowledged", taskID, d.deliveryID,
			"attempt", attempt, "total_attempts", totalAttempts, "wait", timeout)
		if totalAttempts >= maxTotalSubmitAttempts {
			n.removeAndAdvance(taskID, d.deliveryID, false)
			logDelivery(slog.LevelError, "delivery abandoned: total enter attempts exceeded", taskID, d.deliveryID,
				"total_attempts", totalAttempts, "max_total_attempts", maxTotalSubmitAttempts)
			return
		}
	}

	logDelivery(slog.LevelWarn, "delivery remains pending after submit retries", taskID, d.deliveryID,
		"attempts", len(submitAckTimeouts))
}

// deliveryInput decides whether this reconcile cycle may write and constructs
// the exact text to inject. Recognizable composer content is authoritative;
// idle/focus are retained only when the terminal layout is unknown.
func (n *Notifier) deliveryInput(taskID string, d *delivery, sess SessionHandleIface, now time.Time) (clear bool, verifyClear bool, composerKnown bool, payload string, safe bool) {
	draft, known := n.composerDraft(sess)
	if !known {
		logDelivery(slog.LevelDebug, "delivery composer unknown: using idle/focus fallback", taskID, d.deliveryID)
		if !n.idle.IsIdle(taskID, sess, now) {
			logDelivery(slog.LevelDebug, "delivery skip: session busy", taskID, d.deliveryID)
			return false, false, false, "", false
		}
		if n.focus.IsFocused(taskID) {
			logDelivery(slog.LevelDebug, "delivery skip: human focused", taskID, d.deliveryID)
			return false, false, false, "", false
		}
		return true, false, false, d.text, true
	}

	draft = strings.TrimSpace(draft)
	if draft == "" {
		d.observedDraft = ""
		d.draftObservedAt = time.Time{}
		logDelivery(slog.LevelDebug, "delivery composer empty", taskID, d.deliveryID)
		return true, false, true, d.text, true
	}
	if injectedNoticeDraft(draft) {
		d.observedDraft = ""
		d.draftObservedAt = time.Time{}
		logDelivery(slog.LevelInfo, "delivery composer contains stale notice", taskID, d.deliveryID)
		return true, true, true, d.text, true
	}

	if draft != d.observedDraft {
		d.observedDraft = draft
		d.draftObservedAt = now
		logDelivery(slog.LevelDebug, "delivery composer non-notice content changed", taskID, d.deliveryID)
		return false, false, true, "", false
	}
	if now.Sub(d.draftObservedAt) < draftStabilityWindow {
		logDelivery(slog.LevelDebug, "delivery composer non-notice content awaiting stability", taskID, d.deliveryID)
		return false, false, true, "", false
	}

	logDelivery(slog.LevelInfo, "delivery composer content stable: preserving with annotation", taskID, d.deliveryID)
	payload = "\n\n" + abandonedDraftAnnotation + "\n" + d.text
	return false, false, true, payload, true
}

func (n *Notifier) composerDraft(sess SessionHandleIface) (string, bool) {
	cols, rows := sess.PTYSize()
	tail := agent.SubstantiveTail(sess.RecentOutputTail, 64*1024, agent.NeedsInputMaxExpandBytes)
	n.screenMu.Lock()
	draft, known := n.screen.InputDraft(tail, cols, rows)
	n.screenMu.Unlock()
	return strings.TrimSpace(draft), known
}

// waitForComposerClear waits for the rendered composer to confirm that Ctrl+U
// discarded the prior notice. A write syscall alone cannot establish that.
func (n *Notifier) waitForComposerClear(sess SessionHandleIface, timeout time.Duration) bool {
	return waitForComposer(sess, timeout, n.composerDraft, func(draft string) bool { return draft == "" })
}

// waitForComposerConsume waits until a previously observed injected composer
// draft disappears or materially changes after CR. Unrelated PTY output is
// deliberately not an acknowledgment.
func (n *Notifier) waitForComposerConsume(sess SessionHandleIface, submittedDraft string, timeout time.Duration) bool {
	return waitForComposer(sess, timeout, n.composerDraft, func(draft string) bool {
		return draft == "" || draft != submittedDraft
	})
}

func waitForComposer(sess SessionHandleIface, timeout time.Duration, draftOf func(SessionHandleIface) (string, bool), accepted func(string) bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if draft, known := draftOf(sess); known && accepted(draft) {
			return true
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false
		}
		if remaining < outputPollInterval {
			time.Sleep(remaining)
		} else {
			time.Sleep(outputPollInterval)
		}
	}
}

func injectedNoticeDraft(draft string) bool {
	draft = strings.TrimSpace(draft)
	if strings.HasPrefix(draft, "[hera from ") || strings.HasPrefix(draft, "[argus]") {
		return true
	}

	// A failed submission can leave the preserved abandoned draft plus this
	// notifier's annotation and notice in the composer. Treat that whole payload
	// as stale on the next pass so a retry clears it instead of appending another
	// annotation indefinitely. Normalize soft-wrap whitespace first.
	normalized := compactWhitespace(draft)
	annotation := compactWhitespace(abandonedDraftAnnotation)
	annotationAt := strings.Index(normalized, annotation)
	if annotationAt < 0 {
		return false
	}
	afterAnnotation := normalized[annotationAt+len(annotation):]
	return strings.Contains(afterAnnotation, "[herafrom") || strings.Contains(afterAnnotation, "[argus]")
}

// composerContainsText compares logical composer content rather than terminal
// rows. InputDraft inserts newlines at visual soft-wrap boundaries while a
// notifier payload is a single logical line.
func composerContainsText(draft, text string) bool {
	return strings.Contains(compactWhitespace(draft), compactWhitespace(text))
}

func compactWhitespace(s string) string {
	return strings.Join(strings.Fields(s), "")
}

// deliveryStopped observes cancellation and the wall-clock deadline during a
// multi-attempt submit. It mirrors processOne's entry checks so a long wait
// cannot submit after the caller has abandoned the delivery.
func (n *Notifier) deliveryStopped(taskID string, d *delivery) bool {
	select {
	case <-d.cancelCh:
		n.removeAndAdvance(taskID, d.deliveryID, false)
		logDelivery(slog.LevelInfo, "delivery cancelled", taskID, d.deliveryID)
		return true
	default:
	}
	if time.Now().After(d.deadline) {
		n.removeAndAdvance(taskID, d.deliveryID, false)
		logDelivery(slog.LevelWarn, "delivery deadline exceeded", taskID, d.deliveryID)
		return true
	}
	return false
}

// waitForOutputAdvance waits for the session's monotonic PTY output counter to
// move beyond baseline. It performs only lock-free counter reads.
func waitForOutputAdvance(sess SessionHandleIface, baseline uint64, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if sess.TotalWritten() > baseline {
			return true
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false
		}
		if remaining < outputPollInterval {
			time.Sleep(remaining)
		} else {
			time.Sleep(outputPollInterval)
		}
	}
}

// waitForOutputSettled waits until output first advances beyond baseline and
// then remains unchanged for quietWindow. This makes the text→CR gap adaptive
// to recipient redraw latency rather than starting a blind timer at WriteInput.
func waitForOutputSettled(sess SessionHandleIface, baseline uint64, timeout, quietWindow time.Duration) bool {
	deadline := time.Now().Add(timeout)
	last := sess.TotalWritten()
	sawActivity := last > baseline
	quietSince := time.Now()

	for {
		now := time.Now()
		current := sess.TotalWritten()
		if current != last {
			last = current
			sawActivity = current > baseline
			quietSince = now
		}
		if sawActivity && now.Sub(quietSince) >= quietWindow {
			return true
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false
		}
		if remaining < outputPollInterval {
			time.Sleep(remaining)
		} else {
			time.Sleep(outputPollInterval)
		}
	}
}

// logDelivery keeps the existing TUI ux.log trail and also emits through the
// process default slog handler. runDaemon wires slog to daemon.log, so notify
// failures are diagnosable even when no TUI process initialized uxlog.
func logDelivery(level slog.Level, message, taskID, deliveryID string, attrs ...any) {
	uxlog.Log("[notify] %s task=%s id=%s%s", message, taskID, deliveryID, formatLogAttrs(attrs))
	base := []any{"task", taskID, "delivery", deliveryID}
	slog.Log(context.Background(), level, "[notify] "+message, append(base, attrs...)...)
}

func formatLogAttrs(attrs []any) string {
	if len(attrs) == 0 {
		return ""
	}
	formatted := ""
	for i := 0; i+1 < len(attrs); i += 2 {
		formatted += fmt.Sprintf(" %v=%v", attrs[i], attrs[i+1])
	}
	if len(attrs)%2 != 0 {
		formatted += fmt.Sprintf(" %v", attrs[len(attrs)-1])
	}
	return formatted
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
	} else {
		n.idle.Forget(taskID)
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
