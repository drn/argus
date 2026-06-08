# Tasks: add-reliable-pane-delivery

**Design doc:** openspec/changes/add-reliable-pane-delivery/design.md

## 1. Tests (Red)

- [ ] 1.1 Write failing tests for `FocusTracker` (`internal/notify/focus_test.go`): initial state unfocused, SetFocused true/false, IsFocused reflects transitions, concurrent reads+writes are race-free (`go test -race`).
- [ ] 1.2 Write failing tests for `FocusTracker` event emission: focus-gained event fires on false→true, focus-lost fires on true→false, no event on no-op same-state call.
- [ ] 1.3 Write failing tests for `Notifier.ReliableNotify` core path (`internal/notify/service_test.go`): immediate submit when idle+unfocused, deferred when busy, deferred when focused, cancel before submit, deadline evicts pending delivery.
- [ ] 1.4 Write failing tests for dedup: re-post of pending deliveryID returns same cancel (no duplicate), re-post of submitted deliveryID is no-op cancel.
- [ ] 1.5 Write failing tests for pre-clear: PTY receives Ctrl+U then text+CR on submit (assert write sequence).
- [ ] 1.6 Write failing tests for per-task serialization: two concurrent deliveries for same task – second does not write until first completes.
- [ ] 1.7 Write failing tests for `Reconcile`: processes all pending with idle+unfocused sessions, skips pending for non-idle sessions, skips pending for nil sessions, skips pending for focused sessions, evicts past-deadline deliveries.
- [ ] 1.8 Write failing tests for REST `POST /api/tasks/{id}/notify`: 202+pending, 202+submitted (inline), 200+submitted (re-post idempotent), 400 missing fields, 400 bad delivery_id format, 404 unknown task, accepted by device and scope tokens.
- [ ] 1.9 Write failing tests for REST `DELETE /api/tasks/{id}/notify/{delivery_id}`: cancelled:true for pending, cancelled:false for submitted, cancelled:false for unknown, accepted by device and scope tokens.
- [ ] 1.10 Write failing tests for `runnerNudger.Nudge` migration: Nudge calls `ReliableNotify` (not bare WriteInput), Cancel calls `notifier.Cancel`.
- [ ] 1.11 Write failing tests for messaging ack migration: `toolTaskMessageAck` calls nudger.Cancel for each acked message ID after AckMessages.
- [ ] 1.12 Write failing tests for TUI focus wiring (smoke): App mode transition to modeAgent calls SetFocused(taskID, true); mode transition away calls SetFocused(taskID, false).
- [ ] 1.13 Confirm every `it should X` / Scenario in delta specs has a failing test (Prove-It Pattern).

## 2. FocusTracker

**Depends on:** Stage 1

- [ ] 2.1 Add `internal/notify/focus.go`: `FocusTracker` struct with `sync.Mutex`, `focused map[string]bool`, `SetFocused(taskID string, focused bool)`, `IsFocused(taskID string) bool`.
- [ ] 2.2 Add optional `EventEmitter` hook to `FocusTracker` (defaults to `events.Emit`); on state change emit `session.focus` event with `{task_id, focused}` payload. Wire `events.EventTypeSessionFocus` (new constant) in `internal/model/events.go`.
- [ ] 2.3 Add `FocusReader` interface (just `IsFocused(string) bool`) so `Notifier` depends on the interface, not the concrete type (test-injectable).

## 3. Notifier (internal/notify/service.go)

**Depends on:** Stage 2

- [ ] 3.1 Define `delivery` struct: `taskID, text, deliveryID string`, `deadline time.Time`, `cancelCh chan struct{}`.
- [ ] 3.2 Define `Notifier` struct: `mu sync.Mutex`, `pending map[string]*delivery` (one per task), `submitted map[string]map[string]struct{}` (per-task submitted set, capped at 1000 with FIFO eviction), `runner RunnerIface` (use existing `agent.SessionProvider` or define minimal `RunnerIface` in package), `focus FocusReader`.
- [ ] 3.3 Implement `New(runner RunnerIface, focus FocusReader) *Notifier`.
- [ ] 3.4 Implement `ReliableNotify(taskID, text, deliveryID string, opts NotifyOpts) func()` per spec.
- [ ] 3.5 Implement `Cancel(taskID, deliveryID string)` – removes from pending if present; idempotent.
- [ ] 3.6 Implement `Reconcile(now time.Time)`: iterates pending, applies deadline/nil-session/busy/focused gates, submits via `doSubmit(delivery)`.
- [ ] 3.7 Implement `doSubmit(d *delivery)`: writes `\x15` (Ctrl+U) then `d.text + "\r"` to `sess.WriteInput`; marks deliveryID submitted; logs via uxlog.
- [ ] 3.8 Define `NotifyOpts` struct: `DeadlineMS int64` (0 = default 300,000).

## 4. Daemon wiring

**Depends on:** Stage 3

- [ ] 4.1 In `internal/daemon/daemon.go`: create `notify.FocusTracker` and `notify.Notifier` (passing runner + tracker); store both on `Daemon` struct.
- [ ] 4.2 Pass the `Notifier` to the API server: add `SetNotifier(n *notify.Notifier)` on `api.Server` (mirrors `SetClipboard`, `SetScheduler` pattern).
- [ ] 4.3 Pass the `FocusTracker` to the API server: add `SetFocusTracker(f *notify.FocusTracker)` on `api.Server`.
- [ ] 4.4 In `idleWatcherTick`: call `s.notifier.Reconcile(now)` after the existing push + needsInput processing (guard nil check).
- [ ] 4.5 Wire `FocusTracker` to TUI: add `SetFocusTracker(f *notify.FocusTracker)` on `tui.App`; App stores the pointer for use in mode transitions.

## 5. TUI focus wiring

**Depends on:** Stage 4

- [ ] 5.1 In `internal/tui/app.go` mode-switch paths: call `a.focusTracker.SetFocused(taskID, true)` when entering `modeAgent` (in `enterAgentView` or equivalent); call `SetFocused(taskID, false)` when leaving (in `exitAgentView` / mode changes away from modeAgent). Guard nil focusTracker (in-process mode without daemon wiring).
- [ ] 5.2 In `tui.App.Stop()` / cleanup: call `SetFocused(currentTaskID, false)` if currently in modeAgent.

## 6. nudge.go migration

**Depends on:** Stage 3

- [ ] 6.1 Extend `MessageNudger` interface in `internal/mcp/messaging.go`: add `Cancel(targetTaskID, deliveryID string) error` method.
- [ ] 6.2 Rewrite `runnerNudger` in `internal/daemon/nudge.go`: hold `*notify.Notifier`; `Nudge(targetTaskID, line string)` calls `notifier.ReliableNotify(targetTaskID, text, deliveryID, opts)` where text = stripped nudge line, deliveryID = passed message ID (update Nudge signature to accept deliveryID); store returned cancel func. `Cancel(targetTaskID, deliveryID string)` calls `notifier.Cancel` and removes from cancel map.
- [ ] 6.3 Update all callers of `nudger.Nudge` in `internal/mcp/messaging.go` to pass the message ID as the deliveryID argument.

## 7. messaging.go ack migration

**Depends on:** Stage 6

- [ ] 7.1 In `toolTaskMessageAck` (`internal/mcp/messaging.go`): after `AckMessages` returns successfully, call `s.nudger.Cancel(caller.ID, msgID)` for each acked message ID. Guard nil nudger.
- [ ] 7.2 Update `toolTaskAsk` similarly: when a question is acked (WaitForReply returns), cancel the delivery for that question ID.

## 8. REST endpoints

**Depends on:** Stage 3, 4

- [ ] 8.1 Add `handleNotify(w http.ResponseWriter, r *http.Request)` in `internal/api/handlers.go`: parse+validate body, call `s.notifier.ReliableNotify`, return 202/200 per spec.
- [ ] 8.2 Add `handleCancelNotify(w http.ResponseWriter, r *http.Request)` in `internal/api/handlers.go`: call `s.notifier.Cancel`, return 200 per spec.
- [ ] 8.3 Register routes in `internal/api/routes.go`: `POST /api/tasks/{id}/notify` → `handleNotify`; `DELETE /api/tasks/{id}/notify/{delivery_id}` → `handleCancelNotify`. Both outside `requireMaster`.
- [ ] 8.4 Guard nil notifier in both handlers (503 if not wired).

## 9. Docs + gate

**Depends on:** Stages 5, 7, 8

- [ ] 9.1 Add `context/knowledge/gotchas/messaging.md` entries: reliable-notify single-writer invariant, Ctrl+U pre-clear rationale, deliveryID namespace, 5-min default deadline; update bullet count in `context/knowledge/index.md`.
- [ ] 9.2 Add `context/knowledge/gotchas/misc.md` entry: hera contract (POST /notify + DELETE /notify/:id as the stable plugin-callable surface for PTY delivery).
- [ ] 9.3 Run `make pre-pr` and get a clean pass (build → vet → fmt-check → lint-pr → vuln → test-cover-gate).
