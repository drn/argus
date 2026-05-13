# Inter-Task Messaging (MCP)

**Goal:** Let one Argus task communicate with another — share prompts, ask questions, get answers back — without requiring orchestrator-mediated `result` polling or `task_link` dependency edges.

## Today (the gap)

Tasks can only communicate through:

- `task_set_result` → `task_get` (one-way structured blob, last-write-wins; only useful after a child completes)
- `depends_on` (one-way: child blocks until parent reaches `complete`)
- `argus_clipboard_set` (one-way: task → user, not task → task)

There is **no peer-to-peer channel.** A running task cannot ask another running task a question, hand it a fresh prompt, or receive a reply. The daemon already owns the PTY (`runner.Get(id).WriteInput`) but no MCP tool exposes it.

## Approach (recommended)

A persistent, append-only **message log** keyed by `(from_task_id, to_task_id, created_at)`, with optional **request/response** semantics layered on top as a thin convention (`kind` + `in_reply_to`). Tasks send and receive via four new MCP tools; the daemon optionally **nudges** a running recipient's PTY with a one-line `📬 task <X> sent you a message — call task_inbox` so polling isn't required.

Why not raw PTY input injection? Writing bytes into another agent's terminal mid-stream interleaves with its own typing, has no addressability (no reply path), and only works while the target is live. A durable table buys: async delivery (target can be paused/restarted), reply path (in_reply_to), audit log, and PWA visibility — for the cost of one new SQL table plus four tools.

## Schema

```sql
CREATE TABLE IF NOT EXISTS task_messages (
    id            TEXT PRIMARY KEY,        -- ULID
    from_task_id  TEXT NOT NULL,
    to_task_id    TEXT NOT NULL,
    kind          TEXT NOT NULL DEFAULT 'note',  -- 'note' | 'question' | 'answer'
    body          TEXT NOT NULL,           -- payload, <= 64 KiB
    in_reply_to   TEXT NOT NULL DEFAULT '', -- message_id this answers (optional)
    created_at    TEXT NOT NULL,           -- RFC3339
    read_at       TEXT NOT NULL DEFAULT '' -- RFC3339; '' = unread
);
CREATE INDEX IF NOT EXISTS idx_msg_to_unread   ON task_messages(to_task_id, read_at);
CREATE INDEX IF NOT EXISTS idx_msg_in_reply_to ON task_messages(in_reply_to);
```

Caps: **64 KiB per message body**, **500 unread messages per recipient** (further sends rejected until inbox is acked), **50 messages/min/sender** rate limit, **archival** — a recipient's archived/deleted task drops queued messages (foreign-key-on-delete equivalent in app code; we don't enforce FK because tasks are soft-archivable). Self-messages rejected.

## New MCP Tools

All four require task management to be enabled (same gate as `task_create`). All four resolve the caller via `id` or `cwd` exactly like `task_set_result`, so the agent can reach the env-injected `ARGUS_TASK_ID` or fall back to cwd-lookup.

### `task_message_send`

```
to:           string  // target task ID
body:         string  // <= 64 KiB
kind?:        "note" | "question" | "answer"  // default "note"
in_reply_to?: string  // message ID being answered; required when kind="answer"
id?:          string  // caller (omitted → cwd-resolved)
cwd?:         string
```

Returns `{ message_id, delivered: "queued" | "nudged" }`. `nudged` means the target had a live session and the daemon wrote a single notification line to its PTY: `\n[argus] new message from task <id> (kind=<k>) — call task_inbox to read.\n` `queued` means the target was paused/idle/finished and the row sits until they next call `task_inbox`.

### `task_inbox`

```
unread_only?: boolean  // default true
since?:       string   // RFC3339; only return created_at > since
limit?:       integer  // default 50, max 500
sender?:      string   // filter to messages from this task ID
id?, cwd?:    standard caller resolution
```

Returns a compact list — `{ id, from, kind, in_reply_to, created_at, body }` — sorted oldest-first so the agent reads chronologically. Does **not** auto-mark-as-read; the agent must `task_message_ack` after processing so a transient crash doesn't lose unread state.

### `task_message_ack`

```
message_ids: string[]  // up to 500 per call
id?, cwd?: standard caller resolution
```

Marks the supplied IDs `read_at=now()` only when `to_task_id == caller` (silently ignores IDs that don't belong to the caller — avoids leaking existence and avoids errors when a list partially overlaps). Returns `{ acked: <count> }`.

### `task_ask` (convenience)

```
to:               string
body:             string  // the question
timeout_seconds?: integer // default 0 (return immediately); max 120
id?, cwd?:        standard caller resolution
```

Single round-trip request/response. Internally:

1. Insert message with `kind="question"`, capture its ID.
2. Nudge target PTY if live (same as `task_message_send`).
3. If `timeout_seconds > 0`, poll `task_messages WHERE in_reply_to=<qid> AND from_task_id=<to>` at 500ms cadence until a row appears or the deadline passes.
4. Return `{ question_id, answer?: { id, body, created_at }, timed_out: bool }`.

Timeout cap (120s) is hard — long-blocking MCP calls hold an HTTP connection and a goroutine; we want the caller to retry-poll rather than park a connection for minutes.

Answers are sent with `task_message_send(kind="answer", in_reply_to=<question_id>)`. There's no separate `task_answer` tool — the existing send/inbox/ack surface covers the reply.

## Daemon integration

**Nudge path.** Inside `RPCService.SendMessage` (new) or directly in the MCP handler before returning, look up `runner.Get(targetID)`; if non-nil and `IsRunning()`, call `sess.WriteInput([]byte(notificationLine))`. Errors are swallowed (the message is durable; nudge is a best-effort UX boost). Same approach `idleWatcher` uses for push.

**Cleanup.** Extend `task_archive` and the destroy path to delete that task's queued messages (both sent and received). Otherwise an archived task's unread inbox sits forever and counts against the 500-per-recipient cap (... when re-creating a task with the same ID, which doesn't happen — but still, dead rows = leak).

**Test guard.** Same `t.Setenv("HOME", t.TempDir())` discipline. New `internal/db/messages.go` with `InsertMessage`, `Inbox`, `AckMessages`, `UnreadCount`, `WaitForReply` (used by `task_ask`).

## REST API + PWA surface

The HTTP API already exposes `/api/tasks/:id/*` per-task endpoints. Add:

- `GET  /api/tasks/:id/inbox?unread_only=&since=` — same shape as `task_inbox`
- `POST /api/tasks/:id/messages` — `{ to, body, kind?, in_reply_to? }` (master token only — prevents a stolen device token from spoofing inter-task messages)
- `POST /api/tasks/:id/inbox/ack` — `{ ids: [...] }`

PWA: a small "inbox" badge on the task list when `unread_count > 0`, and a per-task panel that shows the conversation thread (group by `in_reply_to` chain). Bump `SW_VERSION` if any shell asset changes.

## Out of scope (deferred)

- **Broadcast / topics.** Single recipient only. Topics are a feature, not a foundation; revisit when we have a concrete use case.
- **Cross-daemon messaging.** All recipients live on the same daemon today.
- **Encryption at rest.** Messages are plaintext in SQLite, same as `tasks.prompt` and `tasks.result`. The daemon's data dir is already user-owned.
- **Web Push on message arrival.** Push infrastructure exists; we can layer this later if the nudge + inbox poll proves insufficient. Likely worth doing for the PWA but skipped from v1 to keep the surface tight.

## Rollout

1. Schema + `internal/db/messages.go` + unit tests.
2. MCP tool definitions + handlers + `task_inbox` / `task_message_send` happy-path tests, including 64 KiB / 500-unread cap rejections.
3. Daemon nudge wiring (live-session WriteInput from message-insert path) + smoke test that nudges a paused vs. running target.
4. `task_ask` polling loop + 120s cap test.
5. REST endpoints + PWA inbox view + service-worker bump.
6. Docs: README MCP-tools table; `context/knowledge/gotchas/misc.md` entry for the 500-unread cap + nudge-is-best-effort invariants.

## Open questions

- **Scope to plan_slug?** Today any task can message any task. Enforcing same-`plan_slug` mirrors how `depswatcher` already partitions stacks but blocks the "two unrelated tasks chatter" use case. Default: **no scoping**, document it, revisit if abuse shows up.
- **Should `task_ask` block on the HTTP request, or return immediately and require client-side polling?** Plan keeps both — `timeout_seconds=0` is poll-mode, anything else blocks up to 120s. Simpler than two tools.
- **Nudge format.** A literal terminal line is mildly intrusive (the recipient agent may interpret it as user input). Alternative: write to a sidecar file (`<worktree>/.argus/inbox`) the agent watches. PTY line is simpler; if it produces accidents, swap to file-based.
