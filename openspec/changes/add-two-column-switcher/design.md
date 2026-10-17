## Context

Moving between argus tasks and Hera projects takes too many keystrokes, so the user opens a plain terminal tab and runs vanilla `claude` instead of using a task. The friction is the feature's failure mode: if jumping into a task is slower than opening a new tab, tasks lose.

Today `ctrl+j` opens a single-list task/role switcher (`TaskSwitcherModal`, grouped mode) that mixes plain tasks and Hera roles under project folders. Its reach is stitched together from three separate dispatch paths:

- `CtxAgent`'s `agent.switcher` in `handleAgentKey` (classic agent view),
- a Tasks-tab-only block in `handleGlobalKey` that resolves the `CtxAgent` binding,
- a hardcoded `case tcell.KeyCtrlJ` in `HeraPage.InputHandler` (via `HeraPage.OnSwitcher`).

Meanwhile `ctrl+g` (`global.jump_needs_input`) and `ctrl+k` (`global.palette`) use the simpler unconditional-`CtxGlobal` shape: resolved once in `handleGlobalKey` ahead of the mode gate, so they fire from every context. `gotchas/keybindings.md` already recommends that shape for the next cross-cutting hotkey.

The two surfaces the user actually thinks in are the Tasks tab list (`taskview.TaskListView`) and the Projects tab rail (`hera.Rail`). Both are self-contained widgets with their own row building, folding, filtering, status icons and `(?)` markers.

## Goals / Non-Goals

**Goals:**

- One hotkey (`ctrl+j`) that opens a jump popup from anywhere: zoomed or unzoomed agent view, Tasks tab, Projects tab rail, or a focused Projects pane.
- The popup shows the Tasks list (left) and the Projects rail (right) side by side, looking like the real rails.
- Arrow keys move within and between columns; typing filters immediately; `ctrl+g` hops to the next `(?)`; Enter goes there.
- The Tasks column never shows hera-managed tasks — no `H` toggle needed.

**Non-Goals:**

- **Web SPA and macOS app parity.** This is a TUI keyboard affordance. Named follow-up: a cross-project jump popup for the web SPA and the macOS app (the mac app already defers a global task/role switcher in `add-mac-keybinding-parity`). No REST surface changes, so no client is broken by this change.
- Mutations from inside the popup (rename, archive, pin, spawn). The popup is jump-only.
- Starting a new task from the popup.
- Reaching the popup while a form/modal is open or a plugin view has surrendered the keyboard (see D2).
- Restarting a dead session on Enter from the Tasks column (see D7).

## Decisions

### D1: Embed private second instances of the real widgets

The popup (`SwitcherPopup`, `internal/tui/switcherpopup.go`) owns one `*taskview.TaskListView` and one `*hera.Rail`, both freshly constructed and fed the same data the real tabs get at open time. It never draws the real tab widgets.

- **Why:** the user asked for "the rail of each". Reusing the widgets gives identical row shapes, glyphs, fold behavior and `(?)` rollups for free, and they can never drift from the real tabs.
- **Own state:** the popup's `Rail` gets no `RailStateStore`, so it never persists; the popup's `TaskListView` is a separate instance. Moving or folding inside the popup never touches the real tabs' cursor or fold state (user decision: "mirror the rails, own copy").
- **Alternative — a new bespoke two-column widget** rendering from switcher entries: rejected; it would re-implement `rail.go`'s ~2.5k lines of tree/fold/reveal logic and drift.
- **Alternative — overlay the live tab widgets:** rejected; shares state with the real tabs, contradicting the "own copy" decision.

Small exported seams are added to the widgets rather than reaching into internals:

- `TaskListView.SetFilter(q string)` and `Rail.SetFilter(q string)` — set the query non-interactively and rebuild (each widget keeps its own existing match semantics).
- `TaskListView.NextNeedsInputTaskID(afterID string) (string, bool)` — mirrors `Rail.NextNeedsInputTaskID`, scanning visible task rows (expanding the project that holds the hit).
- A non-interactive focus-highlight setter on each (the Rail already has `SetFocused`).
- `Rail.CopyFoldStateFrom(other *Rail)` and `TaskListView.SetExpanded` (exists) — seed the popup copies with the real tabs' current folds so the popup opens looking like the rails, then diverge privately.

### D2: `ctrl+j` becomes an unconditional global action

`agent.switcher` (`CtxAgent`) is removed and replaced by `global.switcher` (`CtxGlobal`, default `ctrl+j`), dispatched in `handleGlobalKey`'s unconditional section alongside `global.palette` and `global.jump_needs_input`. The Tasks-tab-only block in `handleGlobalKey`, the `CtxAgent` case in `handleAgentKey`, and the `case tcell.KeyCtrlJ` / `OnSwitcher` hook in `HeraPage` are deleted.

- **Reach:** every context the unconditional section reaches today: classic agent view (zoomed or not), Tasks tab, Projects rail, Projects coordinator/agent pane (incl. fullscreen). The `ctrl+j` byte never reaches a PTY.
- **Exclusions (unchanged precedence):** open forms/modals are handled earlier in `handleGlobalKey` and keep the key (e.g. typing in the new-task form), and plugin-view mode's full surrender keeps forwarding every key but the double-`ctrl+q` failsafe. Pressing `ctrl+j` while the popup itself is open is a no-op.
- **Help:** `global.switcher` appears in the generated Global help section; the Hera rail help section keeps its static `Ctrl+J` row (now labelled "jump switcher") because the rail-keybindings requirement lists it there.
- **Breaking:** a user config override of `agent.switcher` no longer applies; rebind via `global.switcher`. Acceptable under the one-user breaking-changes policy.

### D3: Layout — Tasks left, Projects right

A centered overlay (~90% of the screen) with a shared filter line on top, then two equal-width columns with headers "Tasks" and "Projects", and a footer hint line (`↑↓ move · ←→ column · ctrl+g next (?) · enter go · esc clear/close`). The active column's header and cursor render focused; the inactive column's cursor renders unfocused.

- The Tasks column is fed `a.tasks` (archived excluded), the running/idle/idle-unvisited/needs-input/PR-state maps the real list gets, and `SetManagedTasks(managed)` + `SetHideHeraManaged(true)` — hera-managed tasks (workers + coordinators) are always hidden regardless of the real list's `H` toggle.
- The Projects column is fed a fresh model: `heraPage.Refresh()` then `heraPage.Rail().Model()`, plus the real rail's PR meta.
- In remote mode (`heraPage.IsRemote()`) the Projects column shows a "Projects unavailable in remote mode" placeholder and is skipped by ←/→ and `ctrl+g`.

### D4: Navigation

- ↑/↓ call the active column's `CursorUp`/`CursorDown` (the widgets' own header-skipping and auto-expand/auto-fold rules apply).
- ←/→ switch the active column; each column keeps its own cursor.
- Space toggles a fold in the Projects column (structural, mirrors the rail) — but typed runes otherwise go to the filter, so Space is the one rune that does NOT extend the query. **Rejected alternative:** Space as filter text — task names rarely need spaces, and the TaskListView filter already splits terms on whitespace, so losing literal Space in the query costs nothing.

### D5: Filtering

Every printable rune (except Space) appends to one shared query, applied via `SetFilter` to both columns on every keystroke. Backspace / `ctrl+w` / `ctrl+u` edit it. If a query change leaves the active column with no selectable row while the other column has one, the active column switches to the other.

### D6: `ctrl+g` and the starting cursor share one order

The needs-input order is: Tasks column top-to-bottom, then Projects column top-to-bottom, wrapping. Inside the popup, `ctrl+g` moves the cursor (and active column) to the next `(?)` after the current position in that order; it never navigates or closes, and is a no-op when nothing needs input. Because the popup consumes all keys while open, this does not trigger the global `ctrl+g` jump.

On open the cursor lands on the first `(?)` in that order. If nothing needs input it lands on the currently-viewed task (`agentState.TaskID`, or the Projects rail's selected task when opened from the Projects tab) in whichever column holds it; failing that, the top of the Tasks column.

### D7: Enter and Esc

- **Enter on a Tasks row:** close the popup, `tasklist.SelectByID(id)`, then `onTaskSelect(task, false)` — identical to today's switcher (autoStart=false, a dead session is not resurrected; Enter in the agent view restarts it). Selecting the task you are already viewing simply closes the popup.
- **Enter on a Projects row:** close the popup; if in `modeAgent`, `exitAgentView()` first (the teardown `switchTab(TabHera)` does not perform — the gotcha the old switcher already pins), then `switchTab(TabHera)` and `heraPage.JumpToTask(focusTaskID)`, which expands ancestors and reattaches exactly like rail Enter. A row with no focus task (coordinator-less orchestrator header, structural fold row) lands on the Projects tab with the same orchestrator row selected, via a new `HeraPage.SelectOrch(orchID)` seam.
- **Esc:** with a non-empty query, clears the query (both columns restore their pre-filter cursor); with an empty query, closes the popup and restores the mode/focus it opened from (`taskSwitcherReturnMode` logic, including the three-way Tasks/Hera/agent fallback).
- `ctrl+q` closes the popup immediately, query or not.

### D8: Retire the grouped `TaskSwitcherModal`

`openTaskSwitcher`, `openTaskSwitcherModal`'s grouped path, and the modal's grouped mode (`SetGrouped`, grouped rows/cursor/draw code) are deleted. The flat mode stays — `herapicker.go` (Hera link/unlink parent picker) still uses it.

## Risks / Trade-offs

- [Two live `Rail` instances could share hidden global state] → `Rail` state is per-instance; the popup instance gets no state store and no `onSelectionChanged`, so it never persists nor drives panes. A test asserts the real rail's cursor and folds are unchanged after popup navigation.
- [The popup's model goes stale while it's open] → it is a short-lived jump UI; it snapshots at open time and re-snapshots on each App tick while open (cheap: the Hera model is already rebuilt by the tick gate and simply re-handed to `SetModel`, the task list via `SetTasks`). Cursor identity is preserved by the widgets' own restore-by-ID logic.
- [Losing `ctrl+j` passthrough to agents whose readline binds it] → already the case today (the old switcher steals it in agent view and Hera panes); now also true on the Tasks tab, which has no PTY.
- [Narrow terminals] → below a minimum width (e.g. 60 cols) the columns truncate names with the widgets' own rune-safe truncation; no stacked fallback (YAGNI).
- [Removing three dispatch paths could leave one context uncovered] → smoke tests open the popup from each context named in the spec (agent view zoomed and unzoomed, Tasks tab, Projects rail, Projects pane).

## Migration Plan

None beyond the keymap rename: `agent.switcher` → `global.switcher`. No schema, REST, or daemon change.

## Alternatives considered

- **Bespoke two-column widget** — full control, but duplicates the rail tree logic; rejected (D1).
- **Live overlay of the real tabs** — zero new rendering, but shares fold/cursor state; rejected by user decision.
- **Keep the old ctrl+j list and add a new key** — two switchers to maintain and remember; rejected by user decision.

## Discovery findings

- `Rail` and `TaskListView` are both standalone `tview.Box` widgets with exported cursor/selection APIs; only filter-setting and a task-list needs-input finder are missing.
- `HeraPage.JumpToTask` already encapsulates ancestor expansion + reattach + focus — the Projects-column Enter can reuse it verbatim.
- The old switcher's hera-managed jump path must call `exitAgentView()` before `switchTab(TabHera)`; that requirement carries over (`TestSwitcher_SelectingHeraManagedEntryJumpsIntoHeraTab` pins it today).
- `herapicker.go` is the only other `TaskSwitcherModal` consumer and uses flat mode only.

## Acceptance criteria

**Opening (D2)**

- It should open the popup when `ctrl+j` is pressed in the classic agent view, zoomed and unzoomed.
- It should open the popup when `ctrl+j` is pressed on the Tasks tab, the Projects rail, and a focused Projects pane, without the byte reaching any PTY.
- It should not open the popup while a form/modal is open or a plugin view is active.

**Layout (D3)**

- It should show the Tasks list on the left and the Projects rail on the right.
- It should never show a hera-managed task in the Tasks column, even when the Tasks tab's hide-hera-managed toggle is off.
- It should not show archived tasks in the Tasks column.

**Navigation (D4)**

- It should move the cursor within the active column on ↑/↓ and switch columns on ←/→.
- It should leave the real Tasks tab and Projects rail cursor and fold state unchanged after navigating and folding in the popup.

**Filtering (D5)**

- It should filter both columns as soon as a character is typed.
- It should move to the other column when the filter empties the active one and the other still has matches.

**ctrl+g and start position (D6)**

- It should move the cursor to the next needs-input row, Tasks column first then Projects, wrapping, without closing the popup.
- It should open with the cursor on the first needs-input row when one exists.
- It should open with the cursor on the currently-viewed task when nothing needs input.

**Enter / Esc (D7)**

- It should open the selected task's agent view when Enter is pressed on a Tasks row.
- It should switch to the Projects tab and land on the selected role when Enter is pressed on a Projects row, including from the agent view.
- It should clear the filter on Esc when a filter is applied, and close the popup on Esc when none is.
