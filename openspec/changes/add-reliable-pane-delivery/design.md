# Design: reliable pane delivery

## Context

Two code paths today write unsolicited text into a session PTY:

1. **Native task bus** – `internal/daemon/nudge.go` + `internal/mcp/messaging.go`. Writes `"\n[argus] new message…\n"` then stops. The `\n` is a linefeed (0x0a), not a carriage return (0x0d), so the shell input line is not submitted. Delivery is fire-and-forget with no retry.

2. **hera plugin** – calls `POST /api/tasks/{id}/input` with its pointer text, then tries to inject a CR if it sees a `session.idle` event. That event can be missed (transition-only, not a level signal), so an idle recipient can sit forever with un-submitted text in its input buffer.

Both paths share a PTY and can race: if hera injects a CR while the native bus has just appended a `\n`, both lines land simultaneously, which is either a double-submit or a garbled submission depending on line ordering.

**Root cause:** PTY injection is not gated on idleness, and no arbiter prevents simultaneous injection.

## Goals

- Deliver `(text, deliveryID)` to a task PTY and auto-submit it exactly once.
- Gate submission on `session.IsIdle()` (output quiescence ≥ 3s) AND no human focused on the pane.
- Retry until the gate is clear, bounded by a deadline.
- Deduplicate by deliveryID so re-posting an in-flight or completed delivery is a no-op.
- Serialize all auto-submit CRs per task so no concurrent injections interleave.
- Expose the service to plugin callers over REST.
- Migrate the native task bus to use the same service.

## Non-Goals

- Confirming the agent actually processed the submitted text (out of scope – ephemeral PTY).
- Retry after process restart (delivery is best-effort across PTY lifetime; a restart clears pending deliveries naturally).
- Throttling: the idleWatcher already runs at 5s; the reconciler runs at that cadence.

## Decisions

### Core service: `internal/notify/`

New package `internal/notify/` (separate from `internal/daemon/` to keep daemon lean and allow the TUI to reference the focus tracker without importing the daemon).

```
internal/notify/
  service.go       – Notifier type, ReliableNotify, reconcile
  focus.go         – FocusTracker
  types.go         – delivery state enum, opts
  service_test.go
  focus_test.go
```

`Notifier` holds:
- `mu sync.Mutex` protecting the maps below
- `pending map[string]*delivery` keyed by taskID → deliveryID → delivery struct (one outstanding per task at a time; a second call for a different deliveryID queues or replaces, see dedup rule)
- `runner RunnerIface` for session lookup
- `focus FocusReader` for the focus gate

**Wait – one outstanding per task?** Yes. Auto-submit CRs are rare (one at a time per task) and ordering between concurrent callers is not useful. If a deliveryID is already pending for a task, a new call with the same ID returns the existing cancel func (idempotent). A call with a different ID is enqueued and runs after the in-flight one submits or expires. This keeps the single-writer invariant: only one `WriteInput(CR)` per task.

`ReliableNotify(taskID, text, deliveryID string, opts NotifyOpts) func()`:
- Returns a cancel func. Caller invokes it when the delivery should be abandoned (e.g. message acked).
- If deliveryID already submitted → returns a no-op cancel immediately.
- If deliveryID already pending → returns its existing cancel (idempotent).
- Otherwise: registers a `delivery{taskID, text, deliveryID, deadline, cancelCh}` and returns a cancel func that closes cancelCh.

`reconcile(now time.Time)` (called from idleWatcher tick AND on focus change):
- Iterates pending deliveries.
- For each: if deadline exceeded → cancel and remove.
- If `sess = runner.Get(taskID)` → nil → skip (session not live; retry on next tick).
- If `!sess.IsIdle()` → skip (agent still outputting; retry next tick).
- If `focus.IsFocused(taskID)` → skip (human is typing; leave text for them).
- Otherwise → **submit**:
  1. Lock the per-task submit serializer.
  2. `sess.WriteInput([]byte("\x15"))` – Ctrl+U clears any stale partial input line.
  3. `sess.WriteInput([]byte(text + "\r"))` – inject text and carriage return (shell submit).
  4. Mark deliveryID submitted; remove from pending; unlock.
- Log via uxlog on every submit and every skip-with-reason.

**Ctrl+U pre-clear rationale:** A previous failed injection or a typing-in-progress that the focus gate didn't catch could have left partial text in the shell input. Ctrl+U (`\x15`) is the POSIX "kill line" signal – the shell discards everything before the cursor. If the line is empty, it's a no-op. This makes submission safe against stale partials without needing to know what's in the buffer.

**CR not LF:** Shell input lines are submitted by carriage return (0x0d). Linefeed (0x0a) on its own is not submitted in a normal interactive shell. The current nudge sends `\n` (linefeed only), which is why text sits unsubmitted. This change always uses `\r`.

### Daemon-level focus tracker

`FocusTracker` (in `internal/notify/focus.go`) is a thin, concurrency-safe registry:

```go
type FocusTracker struct {
    mu      sync.Mutex
    focused map[string]bool // taskID → focused
}

func (f *FocusTracker) SetFocused(taskID string, focused bool)
func (f *FocusTracker) IsFocused(taskID string) bool
```

**TUI wiring** (`internal/tui/app.go`): the App holds a `*notify.FocusTracker` (injected at construction from the daemon, or a local one in in-process mode). It calls `tracker.SetFocused(taskID, true)` when `mode` transitions to `modeAgent` with a non-empty `agentState.TaskID`, and `SetFocused(taskID, false)` on exit (mode change away from modeAgent, or agent view cleared). Both TUI focus and plugin-view input forwarding go through the TUI's input path, so a single `SetFocused` call in the mode-switch handler covers both.

**API wiring**: `api.Server` holds the same `*notify.FocusTracker` pointer. The reconciler in the notifier calls `IsFocused` without network I/O.

**Optional event**: On every `SetFocused` transition, emit a `session.focus` event (type `task.viewed` or a new `session.focus`) onto the events bus. This is a quality-of-life signal for plugins that want UI-presence awareness. Not load-bearing for this feature.

### REST surface (hera contract)

**Plugin-callable.** Any non-revoked token (master, device, or plugin-scoped) may call these endpoints. They mutate only per-task state.

```
POST /api/tasks/{id}/notify
```

Request body:
```json
{
  "text": "...",
  "submit": true,
  "delivery_id": "<caller-assigned UUID/nanoid>",
  "deadline_ms": 300000
}
```

Response (202 Accepted):
```json
{
  "delivery_id": "...",
  "state": "submitted" | "pending"
}
```

- `submit: true` is required (false would be raw-inject-no-CR, not implemented in v1; reject with 400).
- `delivery_id` is required; max 128 bytes; alphanumeric + `-_` only.
- `deadline_ms` defaults to 300,000 ms (5 minutes).
- Returns `"state": "submitted"` if the session was idle and focused-clear at request time and the delivery completed inline. Returns `"state": "pending"` if queued for retry.
- If `delivery_id` is already submitted → 200 `{"state": "submitted"}` (idempotent).

```
DELETE /api/tasks/{id}/notify/{delivery_id}
```

Response (200):
```json
{
  "delivery_id": "...",
  "cancelled": true | false
}
```

- `cancelled: true` if the delivery was pending and is now removed; `false` if it was already submitted or not found (idempotent).

**Back-compat:** Raw `POST /api/tasks/{id}/input` is unchanged.

### Native task bus migration

`runnerNudger.Nudge(targetTaskID, line)` in `internal/daemon/nudge.go` currently writes `line` (which contains `\n` at both ends) directly to the PTY. After this change:
- `Nudge` calls `notifier.ReliableNotify(targetTaskID, line, deliveryID, opts)` where `line` is the existing nudge text (stripped of outer newlines – the pre-clear Ctrl+U + carriage return handles whitespace), and `deliveryID` is the message ID.
- `Nudge` stores the returned cancel func keyed by deliveryID in a map on `runnerNudger`.

`toolTaskMessageSend` in `internal/mcp/messaging.go` calls `nudger.Nudge(...)` as before (API unchanged). The Nudger interface gains a `Cancel(targetTaskID, deliveryID string)` method.

`toolTaskMessageAck` calls `nudger.Cancel(caller.ID, msgID)` for each acked message ID (after `AckMessages` returns). This is best-effort: if the delivery already submitted before ack arrived, Cancel is a no-op.

**Security contract preserved:** The sanitized nudge line (`nudgeLineFormat`, digit-only IDs + typed enum) is what gets submitted. No user-controllable strings enter the PTY through this path.

### Wiring summary

```
internal/notify/
  Notifier ← runner (agent.Runner/iface)
           ← focus (FocusTracker)

daemon.go
  creates Notifier (with runner + focusTracker)
  passes Notifier to API server + nudger
  idleWatcher tick → notifier.Reconcile(now)

api/server.go
  holds *notify.Notifier, *notify.FocusTracker
  routes: POST/DELETE /api/tasks/{id}/notify[/{delivery_id}]

tui/app.go
  holds *notify.FocusTracker (injected)
  mode transitions → tracker.SetFocused(...)

daemon/nudge.go
  runnerNudger holds *notify.Notifier
  Nudge → notifier.ReliableNotify
  Cancel → notifier.Cancel

mcp/messaging.go
  AckMessages → nudger.Cancel per acked ID
```

### Idle-gate: direct IsIdle, not event-consuming

The reconciler calls `sess.IsIdle()` directly (polling the `lastOutput` timestamp, `internal/agent/session.go:249`). It does NOT subscribe to `session.idle` events. This is intentional: `session.idle` is transition-only (fires once on busy→idle), so a listener that misses the event sits blocked forever. Direct polling at the 5s tick is guaranteed to detect idle within one tick after the threshold.

### Focus-gate: why it's mandatory (/doitright)

Without the focus gate, a human who has just pressed Enter on a half-typed command would see the auto-submit CR land immediately after, garbling or double-submitting. The focus gate makes auto-submit safe rather than a gamble. It also correctly handles the hera use case: if the recipient is currently typing a reply, hera's notification sits pending and arrives later when the human moves away, rather than interrupting mid-keystroke.

The FocusTracker is daemon-level (not TUI-local) so the REST path and the reconciler both see the same state.

### Delivery dedup and single-writer invariant

- Dedup: `submitted` deliveryIDs are kept in a `submitted map[string]struct{}` (per task, capped at 1000 entries; LRU eviction). Re-posting a submitted ID is a no-op.
- Single-writer: the `Notifier.mu` + per-task submit serializer ensure only one goroutine writes to the PTY for auto-submit purposes at any moment.

## hera contract (explicit, stable for cross-repo implementation)

hera will:

1. **Deliver a hera message to a recipient task:**
   ```
   POST /api/tasks/{recipient_task_id}/notify
   {
     "text": "<pointer text>",
     "submit": true,
     "delivery_id": "<hera-message-id>",
     "deadline_ms": 300000
   }
   ```
   Response: `{"delivery_id": "...", "state": "submitted" | "pending"}`

2. **Cancel when the recipient reads the message (hera_inbox marks it read):**
   ```
   DELETE /api/tasks/{recipient_task_id}/notify/{hera_message_id}
   ```
   Response: `{"delivery_id": "...", "cancelled": true | false}`

3. **Remove its existing idle-gating doorbell loop** – no more subscribing to `session.idle` events for this purpose; argus owns the gate.

4. **Remove its raw `POST /api/tasks/{id}/input` call with CR** – replaced by the notify endpoint.

Auth: any valid token (plugin-scoped or master) is accepted.

Delivery_id namespace: hera uses its own message IDs as delivery_ids; they are globally unique enough by construction. argus does not interpret them beyond dedup.

## Alternatives considered

- **Subscribe to session.idle events in hera:** The current approach. It misses the event when the transition happens before hera subscribes, or when hera's event stream reconnects. Rejected in favor of argus owning the gate.
- **Retry from hera on a poll loop:** Moves complexity to the caller; every plugin would re-implement the same retry/gate logic. Rejected.
- **Inject at WriteInput level in the daemon (not a separate package):** Would couple focus tracking tightly to the daemon and make it inaccessible to the TUI. Rejected in favor of `internal/notify/` as a shared package.
- **Gate on session.needs_input instead of idle:** needs_input is a subset of idle (idle + specific prompt pattern). A generic text delivery should gate on idle (output quiet) not on a specific UI shape the agent may or may not show. Rejected.

## Risks / Trade-offs

- **Ctrl+U side-effect:** If the user is NOT focused (focus gate passed) but had partial text from a previous session that they typed before leaving the pane, Ctrl+U discards it. This is the correct behavior: the user left the pane, so the partial text is stale.
- **Race on focus change:** A human could press a key in the 5-10ms between the reconciler checking `IsFocused` and writing to the PTY. This is an unavoidable TOCTOU window; the consequence is one unintended CR lands in the user's input. Acceptable – it's the same risk any shell automation has. The 3s idle threshold ensures the agent is genuinely quiet before we touch the PTY.
- **deliveryID namespace collision:** Different callers use different ID spaces (message IDs from the DB are 10-digit numerics; hera uses its own IDs). No cross-namespace collision unless both callers happen to generate the same string, which is cosmetically wrong but not harmful (one gets a no-op cancel). Not a correctness concern.
- **Submitted-ID LRU cap (1000):** Entries for IDs submitted more than 1000 submissions ago are evicted. Re-posting an evicted ID would inject twice. In practice a task never accumulates 1000 deliveries, so this is theoretical.
