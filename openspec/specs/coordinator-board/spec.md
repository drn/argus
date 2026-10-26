# Coordinator board specification

## Purpose

Keep coordinator-curated human decisions and FYIs durable, current and visible across session recycles, with safe TUI reply composition and a shared REST surface.

## Requirements

### Requirement: Durable coordinator-owned board

The system SHALL store stable numeric-id items owned by coordinator role, with decision/fyi kind, one-line subject, body, created/updated timestamps, read state, resolved state, resolver and optional resolution note. Only a coordinator binding SHALL authorize agent post/update/resolve/list tools. Material edits SHALL mark unread; identical edits SHALL preserve read state.

#### Scenario: Material edit and recycle

- **WHEN** a coordinator changes a read item's body and its session recycles
- **THEN** the same role retains the same unread item id and the fresh coordinator sees open ids and subjects in join, status and recycle context

#### Scenario: Ownership enforcement

- **WHEN** a worker or another coordinator attempts to mutate an item
- **THEN** the operation is rejected without changing the item

#### Scenario: Edit races a read acknowledgment

- **WHEN** the agent materially edits an item after Aaron expands its prior revision but before the asynchronous read acknowledgment commits
- **THEN** the edited item remains unread

### Requirement: Human board interaction

The TUI SHALL render collapsed entries with bold kind/id/subject headers, support j/k and arrows, Enter expansion marking read, r reply and x dismissal. Reply SHALL focus the coordinator and paste the subject/id followed by two newlines with user input origin, without submitting. Decision dismissal SHALL durably notify its coordinator; resolution SHALL record the human as resolver.

#### Scenario: Reply composition

- **WHEN** Aaron presses r on an item in the focused board
- **THEN** the coordinator composer contains the reply prefix with an empty line ready for typing and no submission occurs

#### Scenario: Decision dismissal

- **WHEN** Aaron dismisses a decision
- **THEN** it is resolved by human and the coordinator inbox durably records that it was dropped
- **AND** resolution and notice commit atomically; the unread notice is delivered through the normal notifier as a bridge plugin-framed turn or PTY fallback, labelled `Aaron (board)` in doorbells, inbox/history and bridge mail UI

#### Scenario: Dismissal before binding or notification enqueue

- **WHEN** a human decision dismissal commits before the coordinator is bound or the process exits before enqueue
- **THEN** the pending notice remains durable and the notification tick delivers it after a live binding exists; repeated dismissal creates no second notice

### Requirement: Predictable Hera presentation

The default-on coordinator-board setting SHALL enable mailbox icons for unread coordinator items and landing on board when unread items exist at selection time. With the setting off the existing landing and icon behavior SHALL be restored. The lower details region SHALL toggle board/plan with p and SHALL NOT switch automatically during refresh. Cmd+Right SHALL use the details focus ladder. REST SHALL expose role-scoped items and per-coordinator unread counts independently of this setting.

#### Scenario: Refresh preserves view

- **WHEN** an unread item arrives while Aaron views a selected coordinator's plan
- **THEN** the plan remains visible until p or a new selection

#### Scenario: Default and disabled landing

- **WHEN** Aaron selects a coordinator with unread items
- **THEN** the board appears and the mailbox glyph shows if the setting is enabled, otherwise the existing plan and coordinator glyph remain

### Requirement: Coordinator maintenance guidance

Coordinator orientation and builtin Hera guidance SHALL instruct agents to put human decisions and attention on the board, rewrite superseded items, and resolve settled items including completed follow-up after a human reply.

#### Scenario: Fresh coordinator

- **WHEN** a coordinator starts or reclaims its role
- **THEN** it receives the maintenance rule and open board item ids/subjects

### Requirement: Focus-aware board hint row

The TUI SHALL reserve the bottom interior row of every visible board for a single muted hint, including empty boards. Unfocused boards SHALL show `⌘→ open board · p plan`; focused boards SHALL show `j/k move · ⏎ expand · r reply · x dismiss · p plan · ctrl+q back` with default bindings. Rebindable labels SHALL come from the active keymap; structural focus, Enter and back labels SHALL reflect the actual fixed keys. Items SHALL scroll above the hint. Narrow panes SHALL clip the hint to available cells without wrapping, overflow or item overlap.

#### Scenario: Unfocused and empty board

- **WHEN** the board is visible without focus, including with no open items
- **THEN** the bottom interior row shows the muted open-board and plan hint

#### Scenario: Focused board with many items

- **WHEN** focus enters a board whose items exceed its height
- **THEN** the bottom interior row shows movement, expand, reply, dismiss, plan and Ctrl+Q back hints while the selected item scrolls above it

#### Scenario: Rebound action

- **WHEN** a board action is rebound in the active keymap
- **THEN** the hint uses the new binding for that action

#### Scenario: Narrow board

- **WHEN** the board is narrower than its hint text
- **THEN** the hint clips cleanly on its reserved row without overwriting the border or item content

### Requirement: Orange unread mailbox glyph

The TUI SHALL color only the unread coordinator mailbox glyph with the existing orange theme color, preserving the row's other style attributes on selected rows. Normal coordinator icons and surrounding text SHALL retain their existing styles. This SHALL apply to orchestrator headers and bridging sub-coordinator rows.

#### Scenario: Unread mailbox on normal and selected rows

- **WHEN** a coordinator has unread board items with the board setting enabled
- **THEN** its mailbox glyph is orange on both normal and selected rail rows and the surrounding text keeps its row style

#### Scenario: Normal coordinator marker

- **WHEN** the coordinator has no unread items or the board setting is disabled
- **THEN** its normal coordinator icon keeps its existing style

### Requirement: Single expanded board item

The TUI board SHALL behave as an accordion. Enter on a collapsed item SHALL expand it, collapse any previously expanded item, and acknowledge the expanded revision as read. Enter on the expanded item SHALL collapse it. Cursor movement SHALL NOT change expansion. Refreshes SHALL preserve the expanded item by id while it remains in the open board; when it is resolved or removed, nothing SHALL remain expanded.

#### Scenario: Expand a different item

- **WHEN** item A is expanded and Aaron presses Enter on collapsed item B
- **THEN** only B is expanded and its displayed revision is marked read

#### Scenario: Collapse the expanded item

- **WHEN** Aaron presses Enter on the expanded item
- **THEN** no item is expanded

#### Scenario: Move without expanding

- **WHEN** Aaron moves the cursor with j/k or arrow keys
- **THEN** the expanded item remains unchanged

#### Scenario: Refresh retains expansion

- **WHEN** the board refreshes and the expanded item still exists
- **THEN** the same item remains expanded by id even if its content or order changes

#### Scenario: Expanded item disappears

- **WHEN** the expanded item is resolved or removed during refresh
- **THEN** no item is expanded, including if that id later returns
