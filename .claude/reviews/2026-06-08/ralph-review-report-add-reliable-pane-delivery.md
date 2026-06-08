# Ralph review report

## Summary

- **Loops completed:** 1 of 3 (clean exit — all auto-fixes applied in one pass)
- **Confidence tier:** Spec
- **Active change:** add-reliable-pane-delivery
- **Delta files reviewed:** specs/reliable-pane-delivery, specs/daemon-focus-tracking, specs/rest-api, specs/task-messaging
- **Diff scope:** 32 files, 2656 lines vs drn/master
- **Pre-ralph SHA:** ade6332e82bd4b7b5f4373e83a8fe633df4cf1ec (use `git diff ade6332e...HEAD` to see ralph's changes)

## Auto-fixed

### Loop 1

1. **`app.go` F4 (critical):** Task switch in agent view (`navigateAgentTask` → `onTaskSelect`) never cleared the prior task's focus. After a task-to-task switch, both the old and new task remained permanently marked as focused, blocking reliable delivery to both indefinitely. Fixed by capturing `prevTaskID` before the `agentState.Reset` and calling `SetFocused(prevTaskID, false)` when switching.

2. **`api/notify.go` F2:** Misleading comment + incorrect state logic. `ReliableNotify` never submits inline, so the `""` branch (mapped to "submitted") was unreachable at the default deadline but semantically wrong. Simplified to always return `StatePending` after registration (which is always true at REST handler time).

3. **`api/notify.go` F3:** Bare string literals `"submitted"` / `"pending"` replaced with `notify.StateSubmitted` / `notify.StatePending` constants.

4. **`model/event.go`:** Comment said `session.focus` events are "Not persisted; informational only" — wrong. `events.Emit` routes to `db.InsertEvent` like all other event types; they ARE persisted. Comment corrected.

5. **`mcp/messaging.go` toolTaskAsk:** No `Cancel` call after `WaitForReply` returned a reply. If the session was not idle at nudge time, the "you have a question" PTY notification would fire after the question was already answered. Added `nudger.Cancel(recipient.ID, msg.ID)` on the reply path.

6. **`notify/service_test.go`:** 4 missing test scenarios:
   - `TestNotifier_FocusLifts_PendingDeliverySubmits` — covers the named delta scenario "Human leaves pane, pending delivery submits"
   - `TestNotifier_WriteInputCtrlUFailure_DeliveryRemainesPending` — Ctrl+U failure leaves delivery pending for retry
   - `TestNotifier_CancelActiveDelivery_PromotesQueued` — cancel active → queued delivery promoted and submits
   - `TestNotifier_CancelQueuedDelivery` — cancel from the queue (not just active) works correctly

## Spec drift

1. **Re-post of pending deliveryID returns no-op cancel, not shared cancel authority** (`service.go:64-73`). The spec says "the re-post SHALL return the existing cancel func." The code returns `func() {}` — the second caller cannot cancel the delivery. Low urgency: `nudger` never re-posts the same ID, and the REST handler checks state before calling `ReliableNotify`. Deferred for now.

2. **`deadline_ms` minimum of 1,000 ms not enforced** (`notify.go:73-80`). Spec says minimum 1,000 ms; code only rejects `< 0`. A 1 ms deadline is accepted and will immediately expire before the first reconcile tick (5s). The delta spec should either be updated to say minimum is `> 0` (map to default), or the handler should enforce the 1,000 ms floor. Delta update needed.

3. **TUI exit scenario** (`app.go`): The spec scenario "TUI exit clears focus" has no test and no explicit deferred cleanup. Existing key-binding paths don't reach `app.Run()` return while in `modeAgent`, so this is safe in practice. No test yet.

## Questions for you

1. **`delivered="nudged"` semantics** (`messaging.go:234`): `runnerNudger.Nudge` always returns nil when a notifier is wired, regardless of whether the session is live. So messages to offline tasks report `delivered=nudged` even though delivery is pending. "Nudged" currently means "registered with the notifier," not "PTY write occurred." Is this the intended meaning, or should it be "queued" for pending-with-session-absent and "nudged" only for cases where delivery is confirmed? Low urgency — cosmetic tool output.

2. **Daemon restart loses all pending deliveries**: The `Notifier` is in-memory. If the daemon restarts after a client POSTs to `/notify` and gets `state: "pending"`, the delivery is lost silently. Callers (like hera) would need to re-post on reconnect. Is this loss acceptable by design, or should it be documented in `misc.md`?

3. **`TestHandleNotify_RepostSubmitted_Returns200` never reaches the 200 path**: The test admits it cannot exercise the idempotency path without a real session. The spec's idempotency requirement (re-post of submitted deliveryID → 200) is untested. Fix requires either a test-session or a `ForceMarkSubmitted` test helper on `Notifier`.

4. **TOCTOU: stray Ctrl+U if Cancel races Reconcile**: Very narrow window. `processOne` passes the `cancelCh` check, then `Cancel()` fires and removes the delivery from pending. `processOne` writes Ctrl+U + text + CR to the PTY anyway (it already passed all gates). A stray Ctrl+U + one PTY line arrives despite the cancel. `removeAndAdvance` sees the delivery is gone from pending (already removed by `Cancel`) and is a no-op. No double-submit, no correctness issue beyond an extra line. Acceptable?

## Skipped

- Em-dash in `nudgeLineFormat` comment ("plain ASCII" is wrong — U+2014 is UTF-8, not ASCII, but not an escape byte) — cosmetic comment inaccuracy
- `string(state)` redundant cast removed inline with F2/F3 fix
- `subKeys` FIFO eviction memory pattern (backing array bounded at ~2x cap, negligible)
- Reconcile single-goroutine contract not documented (safe by convention)
- In-process reconcile goroutine has no stop channel (benign — process exits immediately after `app.Run()`)
- `SetFocused` goroutine doesn't select on `c.closed` (bounded by 2s `rpcTimeout`)

## Ralph's changes

- Commits: 1 (post this report)
- Files modified: 6 (service_test.go, api/notify.go, model/event.go, mcp/messaging.go, tui/app.go, service_test.go)
- Tests: PASS (89.4% filtered coverage, +0.1% from baseline)
- Coverage gate: PASS (88% floor)
