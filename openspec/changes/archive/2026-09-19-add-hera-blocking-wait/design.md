## Context

`task_ask` already bounds a blocking MCP request to 120 seconds and delegates polling to `db.WaitForReply`, including an immediate fast-path query and cancellation through the daemon shutdown context. Hera has durable role-addressed messages but only exposes immediate inbox reads, so agents improvise their own timers. Hera unread rows use SQL `NULL` in `read_at`, unlike task messages' empty-string sentinel.

## Goals / Non-goals

**Goals:**

- Let an existing `hera_inbox` call wait server-side for unread mail without changing its default behavior.
- Cap waits at 120 seconds and cancel them promptly during daemon shutdown.
- Preserve Hera inbox ordering, read acknowledgement, and doorbell cancellation semantics.
- Make the blocking call the documented default for awaiting a role reply.

**Non-goals:**

- Add an indefinite wait or server-push transport.
- Filter a wait to one sender or `in_reply_to` value.
- Change task-message storage or Hera delivery gating.

## Decisions

### Extend `hera_inbox` instead of adding `hera_wait`

An optional `timeout_seconds` argument keeps one consumption path: messages returned after waiting are formatted, acknowledged, and have doorbells cancelled exactly like an immediate inbox read. A separate tool would duplicate those semantics and expand the registered tool surface. Omitted or zero timeout remains non-blocking for compatibility.

### Poll the existing unread query in the database layer

Add `WaitForHeraInbox(ctx, roleID)` beside `HeraInbox`, with an immediate `HeraInbox` fast path followed by a 500 ms ticker. This mirrors `WaitForReply` while intentionally querying `read_at IS NULL`, preserving Hera's distinct NULL convention. SQLite has no notification primitive already used by this subsystem, so a bounded database poll is the smallest consistent implementation.

### Parent waits on daemon shutdown

The MCP handler derives its timeout context from `Server.shutdownCtx`, matching `task_ask`. A daemon stop therefore cancels an in-flight wait instead of delaying shutdown until the user timeout expires.

## Risks / trade-offs

- [A waiting call can return any unread role message, not only a particular reply] → Document the primitive as waiting for inbox activity; callers can inspect `in_reply_to` and wait again when needed.
- [Polling adds periodic SQLite reads] → Use the established 500 ms cadence and cap each wait at 120 seconds.
- [Messages can arrive between timeout and a later retry] → Every invocation starts with the fast-path unread query, so already-arrived mail returns immediately.

## Migration plan

No data migration is required. Deploy the additive tool-schema and handler change; rollback restores immediate-only inbox behavior without changing stored messages.

## Open questions

None.
