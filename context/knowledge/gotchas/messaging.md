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

## Nudge contract

- **Nudge is best-effort. The message is durable regardless.** If the
  recipient's session is paused, finished, or crashing during the write,
  `Nudge` returns `ErrNudgeNoSession` (or a write error) and the tool
  reports `delivered: queued` instead of `delivered: nudged`. The
  `task_messages` row is committed before the nudge is attempted.
- **Nudge writes a single literal line to the PTY** (`[argus] new message
from <id> (kind=<k>) — call task_inbox`). The receiving agent sees this
  as if a user typed it. If a specific backend mis-handles operator-style
  injected lines, switch to a sidecar-file delivery without breaking the
  message contract.
- **The nudge is sent inside the MCP request handler, not from a background
  goroutine.** Latency is a single PTY write (μs) — cheap. Means a slow PTY
  write would block the response; in practice it's instant. If this ever
  becomes a problem, move it to a goroutine but track which messages still
  need to be nudged (otherwise a daemon restart loses queued nudges, which
  is fine, but track it explicitly).

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

- **`POST /api/tasks/{id}/messages` is master-only (`requireMaster`).**
  Sending a message between tasks is a cross-task mutation; the device
  token can only read its task's inbox and ack. Don't relax this without
  understanding the threat model (a stolen device token shouldn't be able
  to spoof a message between agents).
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
