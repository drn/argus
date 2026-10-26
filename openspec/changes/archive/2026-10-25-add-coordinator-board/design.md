## Context

Aaron has pre-approved the design and unattended execution. Existing Hera messages are immutable role-to-role delivery records whose reads acknowledge notifications. The human needs a maintained set of items rather than message history.

## Goals / non-goals

**Goals:** Stable numeric identity, editable decision/FYI items, explicit read and resolution state, coordinator ownership across recycles, safe user-initiated reply composition, REST visibility and full TUI interaction.

**Non-Goals:** Blocking needs-input prompts, automatic submission of replies, merge/deploy. Named follow-ups: `web-coordinator-board-read-only` and `macos-coordinator-board-read-only` add read-only views using the REST surface; full mutations remain TUI-only in this change.

Named follow-ups: `board-arrival-toasts` (the band/toasts render only in the coordinator's pane, where its board tool call is already visible), `board-slash-command` (a list-only `/board` command duplicates the TUI board), and `bridge-board-reply-draft` (the mod exposes no daemon/TUI-accessible draft setter; retain bracketed paste without new plumbing).

## Decisions

- Model accordion expansion as one item id, separate from the cursor. Enter toggles that id and acknowledges only expansion; refresh clears an id absent from the open snapshot so removed/resolved items cannot regain stale expansion if they later reappear.

- Reserve the last board interior row for a muted focus-aware hint before laying out or scrolling items, including empty boards. Resolve rebindable action labels from the active keymap; keep Cmd+Right, Enter and the actual Ctrl+Q back key literal. Clip to the pane width. Reuse `theme.ColorInProgress` for only the unread mailbox foreground, preserving selected-row attributes in orchestrator headers and bridging rows. These presentation refinements remain TUI-only under the named web/macOS follow-ups above; they change no REST contract.

- Use a dedicated `hera_board_items` table keyed by coordinator role. Extending `hera_messages` would introduce a human pseudo-role and change immutable delivery/acknowledgment semantics; a separate table preserves those invariants while reusing the durable inbox for decision-dismissal notifications.
- A material edit is a change to kind, subject or body. Identical writes preserve read state; a changed item becomes unread. Resolved items remain historical and cannot be silently reopened by edits.
- Resolution records agent/human and an optional note. Human decision dismissal and its coordinator inbox notification are atomic, preventing a resolved decision without durable notice.
- Treat self-addressed Hera messages as reserved board system notices: ordinary agent self-send remains rejected. A shared sender-label helper renders them as `Aaron (board)` in inbox/history, bridge band/toasts and the notify doorbell without adding a pseudo-role or changing schema. The transaction writes a pending notice; the existing daemon notification tick drains unread pending board notices through Hera Service delivery and the shared notifier (bridge plugin-framed turn or PTY fallback). The in-process TUI notification tick drains the same outbox. Unbound notices remain pending until a live binding exists; a committed notice survives a crash before enqueue. Repeated resolution creates no duplicate notice.
- Coordinator bridge bindings expose `board_unread` and `board_subject` (newest unread by update timestamp then id). The band shows a separate `board N: <subject>` segment, capped at 60 Unicode characters for the subject, independent of mail counts and the TUI landing preference. Older/partial schemas without the board table yield no board segment.
- Snapshot board data with the existing Hera refresh. Choose board versus plan only when coordinator selection changes; subsequent refreshes update content without stealing the current view. Explicit `p` always toggles.
- Gate automatic board landing and mailbox glyph with default-on `ui.coordinator_board`; storage, tools and explicit board navigation remain available when off.
- Reply sends a single bracketed paste frame containing `Re: <subject> (#<id>)` and two newlines through the user input origin, then focuses the coordinator pane. No carriage return is appended.
- Board item access is scoped by coordinator role, including numeric-id mutations. Workers cannot curate their coordinator's board.

- Read acknowledgments compare the item's update timestamp with the revision expanded by the human. An edit racing the asynchronous UI callback stays unread. The board toggle yields to active rail-filter typing.

## Risks / trade-offs

- Large bodies can crowd the pane → bounded input sizes, wrapping and scrolling in the board view.
- Refreshes can race a mutation → scope mutations by role/id and refresh after completion; log success and failure with `[board]`.
- Existing databases need the table → idempotent schema creation and a schema-version update, tested against existing data.

## Migration plan

Create the additive table/index on database open; preserve existing roles/messages. Archive specs before the PR. No live daemon restart or dogfooding.

## Open questions

None; implementation forks are decided and recorded here during unattended work.
