## MODIFIED Requirements

### Requirement: Rail keybindings (area 4)

The system SHALL bind the following keys while the rail holds focus: `j`/`k` and Down/Up move the cursor; Left walks to the parent; Space collapses/expands the row under the cursor; `Tab`/`Backtab` and `Ctrl+Alt+Left`/`Ctrl+Alt+Right` walk the focus ladder; `Ctrl+Q` returns focus to the rail; `Enter` enters the selected role's pane (restarting a dead/suspended session first); `w` spawns a worker under the selected coordinator's orchestrator via the full new-task modal; `n` creates a new top-level coordinator via the full new-task modal; `r` renames the selected role/orchestrator; `a` HIDES the selected worker / sub-coordinator into its parent coordinator's nested archive (Tier 1 — reversible, keeps the session + worktree alive); `P` toggles pin; `s`/`S` advance/revert the selected role's hera status; `J` adopts a freelancer / re-parents a coordinator; `C` clears the selected coordinator's archive (NUKES every Tier-1 hidden item under it); `Ctrl+Z` fullscreens the focused pane; `Ctrl+J` opens the unified task/role switcher (see the switcher requirement below) — like `Ctrl+Z`, this fires regardless of which region (rail, coordinator pane, or agent pane) holds focus; `/` filters the rail by name; `Ctrl+D` NUKES the selected role/orchestrator (Tier 2 — removes it and its whole subtree from the rail, reclaims worktrees). Every bound key SHALL appear in the help overlay's "Hera View (rail)" section.

The rail SHALL NOT bind `R` (retire) or a rail-wide `Ctrl+R` (prune) — both are removed by this redesign. All rail mutation keys SHALL be suppressed while the rail is in `/` filter INPUT mode (every keystroke is filter input). Selection-acting keys (`w`, `r`, `a`, `P`, `s`/`S`, `J`, `C`, `Ctrl+D`) are no-ops on an empty selection; `n` is selection-INDEPENDENT and fires even on an empty rail.

`Ctrl+K` SHALL NOT be a rail or Hera-page key at all — it is intercepted globally (see the `command-palette` capability) before the Hera page ever sees it, so it never forwards to a focused pane's PTY and never collides with any rail-focus key.

Derived from: `internal/tui/hera/rail.go` (rail `InputHandler`), `internal/tui/hera/page.go` (page `InputHandler` focus ladder + `handleRailMutation`), `internal/tui/heraactions.go` (handlers), `internal/tui/modal/help.go` (help overlay Hera section).

`NOTE:` `Ctrl+D` is the only key that NUKES a live selection directly (`C` nukes only the selected coordinator's already-hidden Tier-1 archive items); the rail binds no `R` (retire) or rail-wide `Ctrl+R` (prune). `Ctrl+D` never collides with the agent-view `Ctrl+R` (Claude session switcher), which runs in a different mode/widget. A focused content pane forwards `C`/`a`/`Ctrl+D` to its PTY.

#### Scenario: Hide key acts on the current selection

- **WHEN** the rail is focused and the user presses `a` on a selected worker
- **THEN** the hide callback fires for that worker's `(role, orchestrator)` selection (no confirmation) and the key does not leak to navigation

#### Scenario: Retire and rail-wide prune keys are unbound

- **WHEN** the user presses `R` or `Ctrl+R` while the rail is focused
- **THEN** nothing end-of-life happens (`R` is unbound; `Ctrl+R` is not a rail-wide prune) — the redesign removed both

#### Scenario: Help overlay lists every rail key

- **WHEN** the help overlay is opened
- **THEN** its "Hera View (rail)" section lists `j`/`k`, space, Left, Tab/Ctrl+Q, Enter, `w`, `n`, `r`, `a` (hide), `P`, `s`/`S`, `J`, `C` (clear archive), `Ctrl+Z`, `Ctrl+J` (switcher), `/`, and `Ctrl+D` (nuke), and does NOT list `R`, a rail-wide `Ctrl+R`, or `Ctrl+K` (which is not a Hera-page key)

#### Scenario: ctrl+j opens the switcher regardless of focused region

- **WHEN** the user presses `ctrl+j` while the rail, the coordinator pane, or the agent pane holds focus
- **THEN** the unified task/role switcher opens in every case, and the key never reaches a focused pane's PTY

## ADDED Requirements

### Requirement: Unified task/role switcher is reachable from Hera

The system SHALL make the unified task/role switcher (`ctrl+j`) reachable from the native Hera view in both rail focus and pane/coordinator focus. In rail focus the switcher was previously unreachable (no binding existed); in pane/coordinator focus the key previously leaked to the live agent PTY. The switcher's entry list SHALL include Hera role bindings alongside plain argus tasks, using the same needs-input union (`heraManaged` ∪ plain in-progress tasks) the Hera rail's own attention accounting already computes.

#### Scenario: Switcher opens from rail focus

- **WHEN** the user presses `ctrl+j` while the Hera rail holds focus
- **THEN** the unified switcher opens, listing both plain argus tasks and Hera roles

#### Scenario: Switcher opens from a live pane, overriding PTY passthrough

- **WHEN** the user presses `ctrl+j` while a Hera coordinator or worker terminal pane holds focus
- **THEN** the switcher opens and the `ctrl+j` byte does not reach the pane's PTY

### Requirement: Switcher defaults to needs-input-first ordering

The switcher's entry list SHALL sort entries with `(?)` (needs-input) first, ahead of every other entry, then alphabetically. Pressing `Enter` immediately after opening the switcher (no filter text typed) SHALL therefore select the first needs-input entry; arrow-down walks to the 2nd, 3rd, and so on.

#### Scenario: Enter with no filter jumps to the first needs-input entry

- **WHEN** the switcher is opened and at least one entry needs input
- **THEN** the topmost entry is a needs-input entry, and pressing `Enter` immediately jumps to it

#### Scenario: Arrow-down walks through remaining needs-input entries before others

- **WHEN** more than one entry needs input
- **THEN** arrow-down visits every needs-input entry, in order, before reaching the first non-needs-input entry

### Requirement: Selecting a folded-away Hera role expands its ancestors before landing

When the switcher's selected entry is a Hera role nested under one or more collapsed ancestor coordinators in the rail, the system SHALL first expand every ancestor on the role's canonical parent chain (uncollapsing them, exactly as a user Space-toggle would, persisting the expansion) before selecting the role and moving focus to its pane — mirroring the existing plan-view `jumpToLeaf` ancestor-expansion behavior.

#### Scenario: Selecting a buried role expands its ancestor chain

- **WHEN** the user selects, from the switcher, a Hera role whose containing coordinator (or a further ancestor) is currently collapsed
- **THEN** the system uncollapses that role's entire ancestor chain, then lands the rail selection and focus on the role's pane — the jump does not silently no-op

### Requirement: Command palette is reachable from Hera and no longer passes ctrl+k through to a pane

The command palette (`ctrl+k`) SHALL open from any Hera focus state (rail, coordinator pane, or agent pane). In pane/coordinator focus this REPLACES the prior behavior of forwarding the raw `ctrl+k` byte to the live PTY — an intentional, documented trade-off (see the `command-palette` capability and this change's design notes), not a silent regression. When opened from Hera rail focus, the palette's action list SHALL include the Hera rail's mutation actions (acting on the rail's current selection). When opened from a Hera coordinator/worker terminal pane, the palette's action list SHALL include BOTH the Hera rail's mutation actions (same target-resolution the Details-mode rail-mutation routing already uses) AND the pane's own two literal actions (fullscreen toggle; clipboard copy, when the pane is a live terminal — the coordinator Details/plan region has nothing to copy so that row is absent there) — never a different tab's action set (e.g. the plain task list's actions never appear).

#### Scenario: Palette opens from a Hera pane instead of forwarding to the PTY

- **WHEN** the user presses `ctrl+k` while a Hera coordinator or worker terminal pane holds focus
- **THEN** the command palette opens and the byte does not reach the pane's PTY

#### Scenario: Palette actions from Hera act on the rail's current selection

- **WHEN** the user invokes a Hera rail mutation action (e.g. spawn worker, toggle pin) from the palette while a Hera pane is focused
- **THEN** the action acts on the rail's currently selected role/orchestrator, the same target it would act on if invoked from rail focus

#### Scenario: Palette from a Hera pane also offers fullscreen and copy

- **WHEN** the palette is opened while a Hera coordinator or worker terminal pane holds focus
- **THEN** its rows include "toggle fullscreen" and "copy staged clipboard" (the pane's own focused-element literal actions) alongside the Hera rail's actions

#### Scenario: Palette never shows another tab's actions

- **WHEN** the palette is opened from any Hera focus state
- **THEN** its rows never include the plain task list's or Settings tab's actions

### Requirement: Rail reveals the ancestor path to a hidden needs-input descendant through closed folds

When a coordinator or nested sub-coordinator row is folded (collapsed) and any descendant role — at any depth, across bridged sub-orchestrators — needs input, the rail SHALL still render the specific ancestor chain down to each such leaf, even though the fold stays visually closed. Every other row at each level along that chain (sibling workers, sibling sub-coordinators with no needs-input descendant) SHALL remain fully hidden, exactly as under ordinary collapse. This reveal is a pure rendering behavior: it SHALL NOT alter the underlying collapse/fold state, and revealed rows SHALL be normal, selectable rail rows (cursor navigation, selection, and mutation keys all act on them exactly as they would if the ancestor were manually expanded).

#### Scenario: Single hidden leaf under one closed coordinator

- **WHEN** a coordinator is collapsed and exactly one descendant worker needs input
- **THEN** the rail renders the coordinator's header (with its own `(?)` marker) and, beneath it, that one worker's row — every other worker under the coordinator stays hidden

#### Scenario: Nested closed coordinators reveal the full chain

- **WHEN** a coordinator is collapsed, a sub-coordinator nested beneath it is also collapsed, and a worker under that sub-coordinator needs input
- **THEN** the rail renders the outer coordinator's header, the sub-coordinator's header (both marked `(?)`), and the needing-input worker's row beneath it — siblings at every level along the chain stay hidden

#### Scenario: Multiple hidden leaves under the same coordinator each get revealed

- **WHEN** a collapsed coordinator has two or more descendant workers (possibly under different collapsed sub-coordinators) that need input
- **THEN** the rail reveals a path to each such worker, not only the first found

#### Scenario: Unrelated siblings stay hidden

- **WHEN** a collapsed coordinator has both a needs-input descendant and unrelated siblings with no needs-input descendant
- **THEN** only the ancestor chain(s) to the needs-input descendant(s) render; the unrelated siblings remain fully hidden

#### Scenario: Toggling the fold still behaves exactly as before

- **WHEN** the user presses `Space` on a coordinator whose subtree is partially revealed via this behavior
- **THEN** the fold fully expands (or collapses) exactly as it would have before this change — the reveal does not change what `Space` does or leave any different post-toggle state
