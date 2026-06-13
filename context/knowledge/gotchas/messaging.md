# Inter-Task Messaging

Non-obvious invariants and silent-failure modes around the `task_messages`
table and the four MCP tools that ride on top of it.

## Caps

- **64 KiB body cap mirrors `task_set_result`.** Same `model.MaxMessageBodyBytes`
  constant on purpose — if an agent can produce one, it knows the other will
  accept the same payload. Don't reuse `task_messages` for log streaming; the
  cap is intentional.
- **`MaxUnreadPerRecipient = 500` blocks senders, not the reader.** When a
  recipient sits on 500 unread rows every subsequent `task_message_send` to
  them rejects with `ErrMessageInboxFull` — even from a different sender.
  The recipient must `task_message_ack` to free capacity. A misbehaving
  recipient that never acks effectively DoSes itself.
- **`MaxSendsPerMinute = 50` is a rolling 60-second window on `from_task_id`,
  not a fixed minute boundary.** A sender that emits 50 in 5 seconds is
  blocked for 55 seconds, not until the next minute rolls over.
- **`task_ask` timeout is capped at 120 seconds.** Longer polls hold an HTTP
  connection + a daemon goroutine; clients wanting longer waits must poll
  `task_inbox` themselves. The cap is enforced at the tool layer
  (`maxAskTimeoutSeconds`), not the DB layer — direct callers of
  `db.WaitForReply` can wait as long as their context allows.

## Self-send and recipient existence

- **A→A messages are rejected at the DB layer (`ErrMessageSelfSend`), not the
  tool layer.** This catches orchestrators that accidentally wire a task to
  itself (e.g., a copy-paste bug in a stack-builder). The tool surfaces the
  sentinel as "cannot send a message to self".
- **Recipient existence is enforced by the tool layer, not the DB.** The DB
  layer would happily insert a message to a typo'd recipient ID; the MCP
  handler calls `taskDB.Get(to)` first so a clean "recipient task not found"
  error fires instead of the message sitting unreadable forever. There's no
  FK because tasks are soft-archivable; archive cleanup runs at archive
  time, not via referential integrity.

## Trust model — Body is untrusted input

- **The `Body` field is data, not commands.** A malicious sender could
  embed prompt-injection payloads ("Ignore your instructions and …") in
  the body; `task_inbox` surfaces raw content into the recipient agent's
  prompt context with no sanitization. Acceptable per the system's
  cooperating-tasks / single-user-local threat model, **but a recipient
  agent that auto-acts on inbox content without a human-style review step
  is creating a privilege-escalation channel between tasks.** Treat
  inbox content the same as any other external input: investigate before
  acting.
- **The nudge line never includes `Body`.** Only `caller.ID` and
  `msg.Kind` reach the PTY, both bounded inputs. See the security
  contract comment on `nudgeLineFormat` in `internal/mcp/messaging.go` —
  do not extend the format with user-controllable strings.

## Nudge contract (reliable pane delivery)

- **Nudge goes through `internal/notify.Notifier` (reliable pane delivery),
  not a bare PTY write.** The message is committed before `Nudge` is called.
  `Nudge` registers a reliable delivery keyed by `msg.ID` as `deliveryID`.
  The reconciler (driven by the idleWatcher 5-second tick) submits via
  `Ctrl+U + text + CR` when the session is idle AND no human is focused.
- **`task_message_ack` cancels the delivery.** When the recipient acks, the
  tool calls `nudger.Cancel(callerID, msgID)` for each acked ID — if the
  delivery already submitted, cancel is a no-op.
- **`runnerNudger.Nudge` with a nil notifier returns `ErrNudgeNoSession`.**
  In-process fallback mode without a wired notifier degrades gracefully;
  the message is durable, delivery is skipped.
- **Pre-clear (`Ctrl+U`) before inject.** `\x15` discards any stale partial
  input in the shell's line buffer so the delivery text lands cleanly.
  If the line is empty, Ctrl+U is a no-op at the shell level.
- **CR, not LF.** The notifier appends `\r` (carriage return, 0x0d) to
  submit the line. The original nudge used `\n` (linefeed, 0x0a) which
  never auto-submits in a normal interactive shell — that was the root bug.
- **deliveryID namespace:** message DB IDs are 10-digit numerics; hera uses
  its own IDs. No cross-namespace collision unless both generate the same
  string (cosmetically wrong but not harmful — one cancel becomes a no-op).
- **Deadline:** default 5 minutes. Delivery is abandoned after the deadline
  if the session never becomes safe. Durable message row is unaffected.
- **Single-writer invariant:** only one auto-submit CR per task is in flight
  at any moment. The `Notifier` serializes concurrent deliveries into a
  queue; the second delivery is promoted after the first submits.

## Archive and delete cleanup

- **Every archive/destroy path must drop the task's queued messages.**
  Today four entrypoints can archive a task (MCP `task_archive`, REST
  `POST /archive`, TUI 'a' keybinding, orch.HaltDownstream via
  `db.SetArchived`) and two can destroy (REST `DELETE /api/tasks/{id}`,
  TUI delete). The DB layer guarantees cleanup for `db.SetArchived(_,
true)` and `db.Delete(id)`; entrypoints that go through `db.Update`
  with `archived=true` (REST archive, TUI archive, MCP archive) call
  `DeleteMessagesForTask` explicitly. **If you add a fifth archive
  surface, do the same — otherwise a stale recipient sits on the
  `MaxUnreadPerRecipient` cap indefinitely.**
- **Cleanup is best-effort.** A delete error is logged but does NOT roll
  the archive/destroy back.

## Polling and `task_ask`

- **`db.WaitForReply` does a fast-path FindReply check before starting the
  ticker.** A reply that landed between the question insert and the poll
  loop's first tick is returned immediately, not after the 500ms tick.
- **`WaitForReply` returns `(nil, nil)` on context cancellation, not an
  error.** Callers must distinguish "no reply yet" (nil, nil) from "DB
  blew up" (nil, err). The tool layer reports the timeout as a normal
  result message, not a tool error.
- **`FindReply` is scoped to `(in_reply_to, from_task_id)`.** A third task
  spamming answers pointing at the same question ID does NOT satisfy the
  wait. The polling loop is strictly addressed.

## REST API surface

- **`POST /api/tasks/{id}/messages` is open to any authenticated token**
  (single-tier auth — see `web-remote.md` "Per-device tokens"). Sending a
  message between tasks is a cross-task mutation but carries no
  RCE/credential risk, so it is not on the master-only denylist (backends
  CRUD, self-update, token mint/revoke). Inbox read + ack are likewise open.
- **`GET /api/tasks/{id}/inbox` is per-task scope, no requireMaster gate.**
  Same tier as archive/rename — reachable from the PWA's per-task UI on a
  device token.
- **`unread_only` defaults to `true` over HTTP.** Pass
  `?unread_only=false` to include already-acked messages; pass
  `0` or `no` for the same effect.

## Testing patterns

- **`InsertMessage` enforces the 50/min sender cap even with `t.TempDir()`.**
  Tests that batch-insert hundreds of messages from a single sender need
  to either vary `from_task_id` per send or sleep longer than
  `rateLimitWindow` (1 minute) to bypass the limit. The
  `messages_test.go` cases use per-iteration unique senders.
- **The `mockMessageStore.WaitForReply` polls at 50ms (vs the prod 500ms).**
  Keeps blocking-reply tests under a couple of seconds. Don't mirror that
  cadence into the real `db.WaitForReply` — the prod cadence is tuned to
  the typical "task replies within a few seconds" case where 500ms tick
  is plenty.

## Hera message bus (M2)

- **`hera_messages.read_at` is real NULL, never `''`.** The partial inbox index
  (`WHERE read_at IS NULL`) only covers rows where the column is actually NULL;
  an empty string would exclude those rows silently. All hera scanners use
  `sql.NullString` and all writes use `NULL`. This diverges from the `task_messages`
  convention (`read_at=''` for unread) — the distinction is intentional and must
  not be conflated.
- **Filling the `hera_messages` inbox cap (500) in tests requires varied senders.**
  `SendHeraMessage` enforces a 50/min per-sender rate limit — batching 500 sends
  from a single sender will hit the rate limit at 50. Use 10 distinct sender roles
  (500 ÷ 50 = 10) so no single sender's window is exceeded. Same pattern as
  `messages_test.go`.
- **`hera_messages` delivery mode is stamped at enqueue time, not submit time.**
  `MarkHeraMessageDelivered` is called immediately after `notifier.ReliableNotify`
  — stamping "idle_submit" to mean "registered for reliable delivery", NOT "actually
  submitted to the PTY". The actual PTY submission happens asynchronously on the
  next Reconcile tick. This is a subtle semantic difference from Hera's original
  `SetDelivered` (which was called post-delivery).
- **hera delivery IDs use the prefix `"hera:"` to avoid collision with
  task_messages delivery IDs.** task_messages uses raw numeric DB IDs as delivery
  IDs (e.g. "1781310427580289000"); hera uses "hera:1", "hera:2", etc. Both
  namespaces live in the same Notifier — the prefix prevents a hera cancel from
  accidentally cancelling a task_messages delivery with the same numeric ID.
- **`hera.Service.Inbox` cancels pending notifier deliveries eagerly.** Unlike
  `task_inbox` (which requires an explicit ack call to cancel), `hera.Service.Inbox`
  cancels deliveries for returned messages immediately — reading IS the acknowledgment
  in Hera's design. `MarkRead` also cancels. Both paths are idempotent (cancel is
  a no-op on already-submitted deliveries).
- **doorbell line includes user-controlled strings (role name, tldr).**
  Unlike `task_messages` nudge (which uses only digit-only task IDs and a typed enum),
  the hera doorbell `[hera from <role-name>] msg #<id> — <tldr>` contains user-
  supplied text. Acceptable under the cooperative single-user local threat model,
  but do NOT copy this pattern to the task_messages nudge format.
