## Why

Argus's Hera (native "Projects" tab) view has no fast way to jump to the one thing that actually needs attention. A coordinator running several nested sub-teams can bury a blocked worker several folds deep in the rail with no visible cue, and there is no keyboard shortcut that jumps straight to it — an operator must manually walk the rail, expanding folds, hunting for `(?)`. Separately, Argus's rebindable keymap (`internal/tui/keymap`) already backs a generated `?` help overlay, but there is no faster way to *find and run* an action than reading through that overlay and manually pressing the right chord — command-palette-style discovery (type a few letters, see the bound key, hit Enter to run it) does not exist anywhere in the TUI. Both gaps compound in Hera, where the operator is juggling several live agent panes at once and switching context is expensive.

This change adds three related navigation/UI primitives to close both gaps in one pass: a unified task/role switcher reachable from anywhere (including Hera), a global command palette that runs any currently-applicable keymap action by name, and rail rendering that reveals a hidden needs-input path without requiring the operator to expand every fold along the way. The three land together because the switcher and the palette both claim ctrl+j/ctrl+k (a rebind, not an addition) and both need the same new "dispatch an action right now" plumbing in Hera's key-forwarding path, and the rail change reuses the same needs-input signal the switcher's default sort uses.

## What Changes

- **BREAKING (rebind)**: `agent.switcher` (today's task switcher, `ActAgentSwitcher`) moves from `ctrl+k` to `ctrl+j`. Ctrl+j is unused in every existing keymap context today (verified against `internal/tui/keymap/actions.go` `defaultSpecs`), so this is a clean rebind with no collision to resolve elsewhere.
- The task switcher (`ctrl+j`) becomes reachable from the native Hera view in BOTH rail focus and pane/coordinator focus — today it does not work at all inside Hera (dead in rail focus; the raw byte leaks into the live agent PTY in pane focus). Opening it from Hera includes Hera roles as switchable entries, not just top-level argus tasks.
- The switcher's entry list defaults to sorting `(?)` (needs-input) entries first, so pressing Enter with no filter typed jumps to the first needs-input entry, and arrow-down walks to the 2nd, 3rd, etc. Selecting a Hera role buried under a closed fold expands its ancestor chain before landing (reusing the existing `jumpToLeaf` ancestor-expansion pattern).
- **BREAKING (repurpose)**: `ctrl+k` is freed up (since the switcher moves off it) and rebound to a NEW global command palette: a searchable, filterable list of the currently-applicable keymap actions (contextual to whatever pane/context has focus), each row showing its label and its bound hotkey. Type to filter, arrow keys + Enter to invoke the selected action immediately.
- The command palette has GLOBAL reach: it works from the classic fullscreen agent view, the plain task list, and both Hera rail and pane/coordinator focus. This requires a new "invoke action X by id, right now" dispatch entry point (keymap resolution today only maps a physical keypress to an action id via a `switch`; there is no reverse "run this action" path) — added without duplicating each action's implementation.
- **BREAKING (behavior change)**: In Hera pane/coordinator focus, `ctrl+j` and `ctrl+k` no longer pass through raw to the live agent PTY — both are intercepted globally, same class as the existing Ctrl+Z fullscreen intercept. This is an intentional, documented tradeoff (see design.md Non-Goals/tradeoffs), not a silent behavior change.
- The Hera rail gains a new rendering mode: when a coordinator (or nested sub-coordinator) row is folded/closed and has any descendant role needing input, the rail renders the specific ancestor chain down to that leaf even though the fold stays visually closed, while every other sibling at each level stays fully hidden. This is purely ambient visibility (no interaction), independent of the switcher/palette.
- No new standalone "jump to next `(?)`" hotkey and no `Ctrl+G` — the switcher's needs-input-first default sort in point 1 covers that need.

## Capabilities

### New Capabilities

- `command-palette`: a global, context-aware, searchable list of keymap actions with a new dispatch-by-action-id entry point; type-to-filter, arrow+Enter to invoke immediately; reachable from the classic agent view, task list, and both Hera rail and pane focus.

### Modified Capabilities

- `keybindings`: `agent.switcher` rebinds from `ctrl+k` to `ctrl+j`; `ctrl+k` is repurposed from the task switcher to the new command palette; both are documented as globally-intercepted chords (mirroring the existing Ctrl+Z precedent) rather than resolved per-context only.
- `hera-view`: the task/role switcher becomes reachable from Hera rail and pane/coordinator focus (previously dead/leaking); the switcher's needs-input-first sort extends to Hera role entries with ancestor-fold expansion on selection; the rail gains a new partial-fold rendering mode that reveals only the ancestor path(s) to a hidden needs-input leaf while a coordinator stays folded; `ctrl+k` no longer passes through to a focused pane's PTY (repurposed to the command palette).

## Impact

- **Affected code**: `internal/tui/keymap/actions.go` (defaultSpecs/actionLabels/contextOrder), `internal/tui/app.go` (handleGlobalKey unconditional switch, handleAgentKey, openTaskSwitcher/openTaskSwitcherModal), `internal/tui/hera/page.go` (InputHandler top-level switch, forwardKey interception), `internal/tui/hera/rail.go` (buildRows, a new partial-fold traversal variant), `internal/tui/hera/model.go` (RoleView/needs-input plumbing reused, not changed), `internal/tui/taskswitcher.go` (or equivalent switcher modal — extended to accept Hera-role entries), a new command-palette modal + dispatch-by-ActionID entry point in `internal/tui/keymap`.
- **Affected docs**: `?` help modal generation (should reflect the rebind automatically since it's keymap-generated — verified, not assumed), README Reference keybinding table, `context/knowledge/gotchas/keybindings.md`, `context/knowledge/gotchas/hera-view.md`.
- **Frontend parity**: TUI-only. Hera interaction (and therefore both the switcher's Hera reach and the rail rendering) is TUI-only today per this repo's Frontend Parity rule — the web and macOS Hera tabs are documented read-only rosters. The command palette's content is TUI keymap actions, which have no web/macOS equivalent surface. See design.md for the explicit Non-Goals statement covering all three surfaces.
- **No DB/schema changes.** No MCP tool changes. No REST endpoint changes.
