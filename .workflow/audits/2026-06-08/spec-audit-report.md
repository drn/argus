# Pre-archive spec audit: add-reliable-pane-delivery

**Date:** 2026-06-08
**Change:** `add-reliable-pane-delivery`
**Auditor:** Claude Code (automated pre-archive gate)

---

## Summary

| Metric | Count |
|--------|-------|
| Total scenarios | 42 |
| COVERED | 33 |
| TEST-GAP | 6 |
| UNIMPLEMENTED | 1 |
| SPEC-DRIFT | 3 |

**Verdict:** Not archive-ready. One [UNIMPLEMENTED] scenario and three [SPEC-DRIFT] items must be resolved first. Six [TEST-GAP] items are lower priority but should be addressed.

---

## Capability 1: reliable-pane-delivery

### Requirement: Reliable inject-and-submit

| Scenario | Status | Code | Test |
|----------|--------|------|------|
| Immediate submit when already safe | COVERED | `service.go:processOne` | `TestNotifier_ImmediateSubmit` |
| Deferred submit while session is busy | COVERED | `service.go:processOne` — early return when `!sess.IsIdle()` | `TestNotifier_DeferredWhenBusy` |
| Deferred submit while human is focused | COVERED | `service.go:processOne` — early return when `focus.IsFocused()` | `TestNotifier_DeferredWhenFocused` |
| Human leaves pane, pending delivery submits | COVERED | Same `processOne` path via next `Reconcile` call | `TestNotifier_FocusLifts_PendingDeliverySubmits` |
| Cancel before submit abandons delivery | COVERED | `service.go:processOne` — select on `cancelCh` | `TestNotifier_CancelBeforeSubmit` |

### Requirement: Exactly-once deduplication by deliveryID

| Scenario | Status | Code | Test |
|----------|--------|------|------|
| Re-post of pending deliveryID returns same cancel | COVERED | `service.go:ReliableNotify` — checks pending/queued first | `TestNotifier_DeduplicatePending` |
| Re-post of submitted deliveryID is a no-op | COVERED | `service.go:ReliableNotify` — `isSubmitted()` guard | `TestNotifier_DeduplicateSubmitted` |

### Requirement: Pre-clear before inject

| Scenario | Status | Code | Test |
|----------|--------|------|------|
| Clean input line yields normal submission | COVERED | `service.go:processOne` — `WriteInput("\x15")` then `text+"\r"` | `TestNotifier_PreClear_WritesCtrlU` |
| Stale partial input is discarded | COVERED | Same `processOne` path | `TestNotifier_PreClear_WritesCtrlU` (same bytes path) |

### Requirement: Deadline backstop

| Scenario | Status | Code | Test |
|----------|--------|------|------|
| Delivery abandoned at deadline | COVERED | `service.go:processOne` — `now.After(d.deadline)` check | `TestNotifier_DeadlineEvictsDelivery` |
| Delivery submitted before deadline | COVERED | Normal submit path in `processOne` | `TestNotifier_ImmediateSubmit` (implicitly — no deadline interference) |

### Requirement: Per-task submit serialization

| Scenario | Status | Code | Test |
|----------|--------|------|------|
| Concurrent deliveries for the same task are serialized | COVERED | `service.go` — single `pending` slot per task; second delivery goes to `queue` | `TestNotifier_SerializeConcurrentDeliveries` |

### Requirement: Reconciler driven by idle-watcher tick

| Scenario | Status | Code | Test |
|----------|--------|------|------|
| Reconcile processes all pending deliveries | COVERED | `service.go:Reconcile` → `processOne` | `TestNotifier_ImmediateSubmit` (calls Reconcile directly) |
| Reconcile skips deliveries for sessions not yet idle | COVERED | `service.go:processOne` — `!sess.IsIdle()` early return | `TestNotifier_ReconcileSkipsNonIdleSessions` |

### Requirement: No PTY write when session is absent

| Scenario | Status | Code | Test |
|----------|--------|------|------|
| Missing session defers delivery | COVERED | `service.go:processOne` — `sess == nil` early return (no removal) | `TestNotifier_NoSession_DeferDelivery` |

---

## Capability 2: daemon-focus-tracking

### Requirement: Focus registration and query

| Scenario | Status | Code | Test |
|----------|--------|------|------|
| Task starts unfocused | COVERED | `focus.go:IsFocused` — returns false for absent key | `TestFocusTracker_InitiallyUnfocused` |
| Focus is registered | COVERED | `focus.go:SetFocused` — sets map entry | `TestFocusTracker_SetAndGet` |
| Focus is cleared | COVERED | `focus.go:SetFocused` — deletes map entry | `TestFocusTracker_SetAndGet` |
| Concurrent reads and writes are safe | COVERED | `focus.go` — `sync.Mutex` guards both methods | `TestFocusTracker_Concurrent` |

### Requirement: TUI wires focus on agent-view enter and leave

| Scenario | Status | Code | Test |
|----------|--------|------|------|
| Entering agent view registers focus | COVERED | `app.go:enterPendingAgentView` (line 2628) and `app.go:onTaskSelect` (line 2674) call `focusTracker.SetFocused(task.ID, true)` | **TEST-GAP** – `TestSmoke_AgentViewEnterExit` does not set a `FocusTracker` on the app; no test verifies `SetFocused(true)` fires on enter |
| Leaving agent view clears focus | COVERED | `app.go:exitAgentView` (line 4575) calls `focusTracker.SetFocused(exitTaskID, false)` | **TEST-GAP** – same test gap as above; no test verifies `SetFocused(false)` fires on exit |
| TUI exit clears focus | COVERED (functionally) | Both `tapp.Stop()` callsites (Ctrl+C at line 1800, 'q' at line 1911) are gated so they only fire in `modeTaskList`, never in `modeAgent`. `modeAgent` → task list always routes through `exitAgentView()`. Therefore exiting the TUI from agent view is impossible without first calling `exitAgentView()` which clears focus. | **TEST-GAP** – no explicit test verifies that if a `FocusTracker` is set and the app exits while in `modeTaskList` (after leaving agent view), focus was already cleared by `exitAgentView`. The functional guarantee is there but untested. |

### Requirement: Focus transition emits an event (optional, best-effort)

| Scenario | Status | Code | Test |
|----------|--------|------|------|
| Focus-gained event fires on enter | COVERED | `daemon.go:524` — `NewFocusTracker` callback emits `EventTypeSessionFocus` with `focused:true` | `TestFocusTracker_TransitionCallback_FocusGained` (covers the callback mechanism; integration with event bus is daemon-wiring only) |
| Focus-lost event fires on leave | COVERED | Same callback path | `TestFocusTracker_TransitionCallback_FocusLost` |
| No event on no-op transition | COVERED | `focus.go:SetFocused` — `changed := prior != focused` guards callback | `TestFocusTracker_NoCallbackOnNoOp` |

---

## Capability 3: rest-api

### Requirement: Reliable pane-delivery endpoint (POST /api/tasks/{id}/notify)

| Scenario | Status | Code | Test |
|----------|--------|------|------|
| Delivery registered and pending | COVERED | `notify.go:handleNotify` — always returns 202 + `"pending"` after `ReliableNotify` | `TestHandleNotify_ValidRequest_Returns202Pending` |
| Delivery submitted inline (session already idle and unfocused) | **UNIMPLEMENTED** | The spec says: if session is idle and unfocused at request time, return `{"state":"submitted"}`. The code never calls `Reconcile` inline inside `handleNotify` — it always returns `"pending"` (line 95). A caller that posts to an already-idle, unfocused task will always get `"pending"` back even though the delivery may submit on the next 5-second tick. | No test exists because the code path does not exist. |
| Re-post of submitted delivery_id is idempotent (200) | **TEST-GAP** | Code at `notify.go:83-86` correctly returns 200 + `"submitted"` when `DeliveryState == StateSubmitted`. However, `TestHandleNotify_RepostSubmitted_Returns200` cancels the delivery without submitting it and re-posts — it tests the cancelled-then-repost path, not the actually-submitted path. The correct scenario (delivery submitted via a real idle session + Reconcile, then re-posted) is not exercised. | `TestHandleNotify_RepostSubmitted_Returns200` — DOES NOT actually test the scenario it claims. See note. |
| Missing or invalid fields rejected | COVERED | `notify.go:53-76` — validates text, submit, delivery_id | `TestHandleNotify_MissingText_Returns400`, `TestHandleNotify_SubmitNotTrue_Returns400`, `TestHandleNotify_MissingDeliveryID_Returns400` |
| Delivery_id format rejected | COVERED | `notify.go:69-72` — `reDeliveryID.MatchString` | `TestHandleNotify_BadDeliveryIDFormat_Returns400` |
| Task not found | COVERED | `notify.go:41-44` | `TestHandleNotify_TaskNotFound` |
| Any authenticated token accepted | COVERED (partial) | No `requireMaster()` gate on this endpoint. Device token tested. Plugin-scoped token NOT explicitly tested for this endpoint. | `TestHandleNotify_DeviceTokenAccepted` tests device token. **TEST-GAP**: plugin-scoped token is not tested for this endpoint (routes.go comment at line 140 notes it should work, but no test). |

**Additional note on deadline_ms validation:** The spec requires `minimum 1,000` for `deadline_ms`, but the code accepts any value `>= 0` (treating 0 as "use default"). Values 1–999 are accepted without error even though the spec calls them out of range. This is a minor spec/code discrepancy — see [SPEC-DRIFT] section below.

### Requirement: Cancel pane-delivery endpoint (DELETE /api/tasks/{id}/notify/{delivery_id})

| Scenario | Status | Code | Test |
|----------|--------|------|------|
| Pending delivery cancelled | COVERED | `notify.go:handleCancelNotify` — checks `prior == StatePending` before `Cancel()` | `TestHandleCancelNotify_PendingDelivery_ReturnsCancelledTrue` |
| Already-submitted delivery cancel is a no-op | **TEST-GAP** | Code path exists: `prior != StatePending` → `Cancelled: false`. But test `TestHandleCancelNotify_UnknownDeliveryID_ReturnsCancelledFalse` only covers the "never registered" case, not the "already submitted" case. | No test for already-submitted cancel. |
| Unknown delivery_id is a no-op | COVERED | Same code path as above | `TestHandleCancelNotify_UnknownDeliveryID_ReturnsCancelledFalse` |
| Any authenticated token accepted | COVERED (partial) | Device token tested. Plugin-scoped token NOT tested for this endpoint. | `TestHandleCancelNotify_DeviceTokenAccepted`. **TEST-GAP**: plugin-scoped token not tested. |

---

## Capability 4: task-messaging

### Requirement: Message delivery via reliable notify

| Scenario | Status | Code | Test |
|----------|--------|------|------|
| Message send registers a reliable delivery | COVERED | `messaging.go:toolTaskMessageSend` calls `nudger.Nudge()` → `runnerNudger.Nudge()` → `notifier.ReliableNotify()`; returns `delivered=nudged` or `delivered=queued` | `TestNudge_WithNotifier_RegistersDelivery` |
| No session – delivery queued in notifier | COVERED | `runnerNudger.Nudge()` calls `notifier.ReliableNotify()` regardless of session existence; notifier registers as pending | `TestNudge_WithNotifier_RegistersDelivery` (no session added → pending) |
| Delivery does not gate the message commit | COVERED | `messaging.go:230-240` — `InsertMessage` completes before `nudger.Nudge()` is called; nudge errors are ignored | Covered structurally — no dedicated test, but `TestNudge_WithNotifier_RegistersDelivery` + the order of calls in `toolTaskMessageSend` confirm this. |

### Requirement: Ack cancels the pending delivery

| Scenario | Status | Code | Test |
|----------|--------|------|------|
| Ack cancels pending delivery | COVERED | `messaging.go:toolTaskMessageAck` calls `nudger.Cancel()` for each acked message ID | `TestNudge_Cancel_CallsNotifierCancel` |
| Ack of already-submitted delivery is a no-op | COVERED | `runnerNudger.Cancel()` calls `notifier.Cancel()` which is a no-op when delivery is not pending | `TestNudge_Cancel_CallsNotifierCancel` + `TestNotifier_Cancel_UnknownIsNoOp` (covers downstream) |
| Ack of message without a delivery is a no-op | COVERED | `nudger.Cancel()` on an unregistered ID is a no-op in `notifier.Cancel()` | `TestNotifier_Cancel_UnknownIsNoOp` |

---

## SPEC-DRIFT: code behaviors not covered by any delta scenario

### SPEC-DRIFT-1: minimum deadline_ms validation not enforced

- **What the spec says:** `deadline_ms` minimum 1,000 ms; maximum 3,600,000 ms.
- **What the code does:** accepts any value `>= 0` (line 73 of `notify.go`). Values 1–999 are silently accepted and treated as legitimate short deadlines by the notifier.
- **Impact:** Low — a very short deadline causes rapid eviction with no PTY write. Not a security issue. But it diverges from the spec contract.
- **Files:** `/internal/api/notify.go:73`

### SPEC-DRIFT-2: in-process mode Reconcile runs in main.go background goroutine, not tied to idleWatcher

- **What the spec says:** "`Reconcile(now time.Time)` SHALL be called by the daemon's idle-watcher tick (5-second interval)."
- **What the code does:** In daemon mode, `push.go:idleWatcherTick` correctly calls `notifier.Reconcile()` (line 373). In in-process mode (no daemon), `cmd/argus/main.go` spins up a standalone 5-second ticker goroutine calling `n.Reconcile()` independently of the idle watcher. This is functionally correct but architecturally diverges — the spec says the idle-watcher tick is the driver, not a separate goroutine.
- **Impact:** Low — behavior is identical. But the separation means any future change to idle-watcher period won't automatically affect the in-process reconciler.
- **Files:** `/cmd/argus/main.go:237-247`

### SPEC-DRIFT-3: `TestHandleNotify_RepostSubmitted_Returns200` does not test what its name says

- **What the spec says:** Re-posting a previously submitted `delivery_id` SHALL return 200 with `state: "submitted"`.
- **What the test does:** Cancels (not submits) the delivery, then re-posts. This exercises the "cancelled, then re-registered" path (returns 202), not the "submitted, then idempotent re-post" path (returns 200). The code for the 200 path exists and is correct, but there is no test covering it with an actually-submitted delivery. This overlaps with the TEST-GAP for "Re-post of submitted delivery_id is idempotent."
- **Files:** `/internal/api/notify_test.go:124-146`

---

## Items requiring action before archive

### UNIMPLEMENTED (must fix)

1. **REST API – Delivery submitted inline:** `handleNotify` should attempt `Reconcile` inline (or check if the session is already idle+unfocused after registering) and return `{"state":"submitted"}` + 202 when the delivery is submitted immediately, matching the spec scenario "Delivery submitted inline (session already idle and unfocused)." Currently always returns `"pending"`.

### TEST-GAP (should fix before archive)

1. **Smoke test for FocusTracker wiring on agent-view enter/exit** – `TestSmoke_AgentViewEnterExit` should call `app.SetFocusTracker(ft)` with a fake tracker and assert `ft.IsFocused(taskID)` is true after Enter and false after Ctrl+Q/Ctrl+D.
2. **REST: re-post of truly-submitted delivery_id returns 200** – fix `TestHandleNotify_RepostSubmitted_Returns200` to use a live idle session (or call `notifier.Reconcile` after registering) to actually get the delivery into submitted state, then re-post and assert 200 + `"submitted"`.
3. **REST: already-submitted delivery cancel returns `cancelled:false`** – add a test for `DELETE /notify/{id}` where the delivery was already submitted.
4. **REST: plugin-scoped token accepted on POST notify** – add test parallel to `TestHandleNotify_DeviceTokenAccepted` using a plugin-scoped token.
5. **REST: plugin-scoped token accepted on DELETE notify** – same for cancel endpoint.
6. **TUI smoke: focus cleared on exit** – smoke test that installs a FocusTracker, enters agent view, then exits the TUI and verifies focus was cleared.
