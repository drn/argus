## Context

Argus's rebindable keymap (`internal/tui/keymap`) resolves a physical keypress → `Action` per `Context`. `handleGlobalKey` (`internal/tui/app.go`) is the `tview.Application`'s `SetInputCapture`, so it sees every key BEFORE any focused widget — its top section already special-cases `Ctrl+C`/`Ctrl+Q` (hardcoded literals) and, since hera-nav-palette, `ActGlobalPalette` (a normal rebindable `CtxGlobal` action resolved and dispatched unconditionally, ahead of the `a.mode`/`suppressRune` gate that guards every other `CtxGlobal` case) — the established "guaranteed global reach" pattern.

`ActAgentSwitcher` (`agent.switcher`, default `ctrl+j`) is registered ONLY under `CtxAgent` and is resolved ONLY from `handleAgentKey` (`a.mode == modeAgent`). The native Hera view's reach for the same conceptual action is a SEPARATE mechanism: a hardcoded literal `case tcell.KeyCtrlJ:` in `HeraPage.InputHandler` (page.go), firing `p.OnSwitcher` regardless of focus region — deliberately NOT a keymap action (Hera's page-level keys have never gone through `keymap.Resolve`; Ctrl+Z/Ctrl+Y are the same shape). Neither path covers `modeTaskList`/`TabTasks` (the plain Tasks list): the key falls through `handleGlobalKey` to `TaskListView`'s own `InputHandler`, which resolves `CtxTaskList` and has no case for the switcher at all — silently swallowed. `closeTaskSwitcherModal` compounds this: it assumes the switcher's only non-agent origin is Hera (`else { a.tapp.SetFocus(a.heraPage) }`), which is simply wrong once the Tasks tab can open it too.

The Hera rail's needs-input signal is a well-tested rollup: `RoleView.NeedsInput` (own signal, threaded from `App.needsInputIDs` into `BuildModel`) and `RoleView.SubtreeNeedsInput` (the rollup `rollupNeedsInput` computes, crossing bridged sub-orchestrators). `Rail.buildRows()` already has a well-established traversal order (Pinned → Active depth-first → Freelance → Archive) and a partial-fold-reveal branch (`appendOrchWorkers`'s `revealOnly` mode, shipped in hera-nav-palette) that surfaces a needs-input leaf's ancestor chain even while its coordinator stays visually folded. `HeraPage.JumpToTask` (extracted from `jumpToLeaf` in hera-nav-palette) already does "ancestor-expand (`EnsureAncestorsExpanded` per `OrchIDsForTask`) → `SelectByTaskID` → reattach a dead/suspended session → focus the resolved pane" for any argus task id — the switcher's own Hera landing already reuses it verbatim.

## Goals / Non-Goals

**Goals:**

- Fix `ctrl+j` so it works from the plain Tasks tab exactly as it already does from the classic agent view and the native Hera view.
- A single dedicated chord (`ctrl+g`) jumps straight to the next role needing input, reachable from every mode/focus region, with repeated presses cycling forward through every candidate without getting stuck or re-visiting one before the others.

**Non-Goals:**

- **Not re-opening the switcher's own scope.** `ctrl+j`'s entry list, sort, and Hera-reach mechanics (hera-nav-palette) are untouched; this change only fixes ONE dispatch gap (the Tasks tab) and fixes a focus-restore bug that gap exposed.
- **No plain (non-Hera) task target for `ctrl+g`.** Unlike the switcher (which unifies plain tasks + Hera roles), `ctrl+g`'s candidate ring is Hera-rail-order only — "role" in the proposal's own framing, not "task." A plain Tasks-tab task that needs input but is never bound to any Hera role is out of scope for this hotkey (it's already covered by the task list's own `(?)` indicator and the switcher).
- **No jump target for a top-level coordinator's own need.** See Decision 3 below — this is a scoping decision forced by an existing `SelectByTaskID` limitation, not a partial implementation to fill in later within this change.
- **No web/macOS parity.** Same Non-Goal as hera-nav-palette: Hera interaction is TUI-only by existing, documented design.

## Decisions

### 1. `ctrl+j` reaches the Tasks tab by resolving `CtxAgent` directly from `handleGlobalKey`, not a new `CtxTaskList` binding

A new block in `handleGlobalKey`, gated on `a.mode == modeTaskList && a.header.ActiveTab() == widget.TabTasks`, calls `a.activeKeymap().Resolve(keymap.CtxAgent, event)` and checks for `ActAgentSwitcher` directly — reusing the SAME `agent.switcher` binding table the classic agent view already resolves, rather than adding a second `CtxTaskList` entry for the same conceptual action. This means a user's config override of `agent.switcher` applies uniformly to both surfaces, and `contextOrder`/`actionLabels`/`defaultSpecs` need no changes (the existing `CtxAgent` entry already covers it).

**Alternative considered:** add `ActAgentSwitcher` (or a new alias action) to `CtxTaskList`'s `defaultSpecs`. Rejected — this is the SAME switcher, not a new one; a second binding-table entry for one conceptual action, kept in sync by convention rather than by construction, is a smaller but real drift risk (`gofmt`-clean but semantically double-booked) for no benefit over resolving the existing table directly.

**Why not fold `ctrl+j` into `ActGlobalPalette`'s unconditional dispatch pattern (`CtxGlobal`) instead?** That would change its behavior in the classic agent view and in Hera too (both already have their OWN, independently-shipped, already-tested reach paths — `handleAgentKey`'s `CtxAgent` interception and `HeraPage.InputHandler`'s literal case respectively). Touching either risks regressing already-shipped, already-tested behavior for a fix that only needs to add ONE new surface (the Tasks tab). The new block is scoped exactly to that gap and runs BEFORE the `switch a.mode { case modeAgent: ... }` dispatch, alongside (not replacing) the existing `CtxGlobal` unconditional section.

**`closeTaskSwitcherModal` fix (a bug this exposes, not new scope):** its `if a.mode == modeAgent { ... } else { a.tapp.SetFocus(a.heraPage) }` branch assumed Hera was the only non-agent origin. Fixed to switch on `a.header.ActiveTab()` (Hera → `heraPage`, else → `tasklist`), mirroring `closeCommandPaletteModal`'s existing three-way fallback exactly.

### 2. `ctrl+g` is a new `CtxGlobal` action dispatched unconditionally, exactly like `ActGlobalPalette`

`ActGlobalJumpNeedsInput` (`global.jump_needs_input`, default `ctrl+g`) is added to `CtxGlobal`'s `defaultSpecs`/`actionLabels`/`contextOrder`, and its `case` lives in the SAME unconditional-dispatch section of `handleGlobalKey` `ActGlobalPalette` already established (resolved once, ahead of the `a.mode`/`suppressRune` gate). This is a single dispatch point covering every context uniformly (fullscreen agent view, plain Tasks tab, Hera rail, Hera pane) — unlike `ctrl+j`'s fragmented per-surface reach (Decision 1's `CtxAgent` fix + Hera's own literal case), `ctrl+g` needs NO Hera-page-local literal case at all: `handleGlobalKey`'s `SetInputCapture` always runs first, so the unconditional dispatch consumes the key and returns `nil` before it can ever reach `HeraPage.InputHandler`, regardless of what's focused.

**Alternative considered:** mirror `ctrl+j`'s own multi-path shape (a `CtxAgent`-ish binding plus a separate Hera-page literal case). Rejected — that fragmentation is exactly what left the Tasks tab gap in place for `ctrl+j`; `ctrl+g` is new, so there is no reason to repeat a shape that's already caused one dispatch gap when the unconditional-dispatch pattern (proven by `ActGlobalPalette`) covers every context in one place.

### 3. `Rail.NextNeedsInputTaskID` scopes candidates to `row.role`-bearing rows only — never a top-level coordinator header

The new `railRow.needsInputTaskID()` requires `row.role != nil` (a genuine `rrRole`/`rrFreelanceRole`/`rrPinnedBreadcrumb` row) and checks that role's OWN `needsInputOwn()` signal (the same unit the switcher's needs-input-first sort and the rail's leaf "(?)" glyph both key on) — NOT a coordinator header's rolled-up `SubtreeNeedsInput`. `Rail.NextNeedsInputTaskID` scans `r.rows` (today's built order — Pinned → Active depth-first → Freelance → Archive, including any rows the partial-fold reveal already surfaces behind a closed fold) starting strictly AFTER the current cursor and wrapping around the whole ring back to (and including) the cursor itself, mirroring `SelectByTaskID`'s scan-and-select shape but position- rather than id-driven.

**Why exclude a top-level coordinator's own need entirely, even when genuinely set:** `appendOrchWorkers` folds a TOP-LEVEL orchestrator's coordinator role entirely into its `rrOrch` HEADER row (`if w.Kind == db.HeraKindCoordinator { continue // folded into the header }`) — it is never emitted as its own `row.role`-bearing row. `SelectByTaskID` (which `JumpToTask` uses to land the jump) only ever matches `row.role`, so it can NEVER select a top-level coordinator by its own task id — confirmed empirically (a top-level coordinator's own task id, passed to `JumpToTask`, always returns `false`, a PRE-EXISTING limitation the switcher's Hera landing already silently has today, not introduced by this change). Offering the header as a `ctrl+g` candidate would therefore produce a "found but unreachable" dead cycle stop — worse than simply not offering it, since the flashed "no role needs input" notice would be actively wrong (something DOES need input, the jump just can't reach it). A NESTED sub-coordinator is unaffected: it bridges as an ordinary role-bearing WORKER row in its PARENT orchestrator (`appendWorkerRow`), so its own need remains a perfectly reachable candidate.

**Alternative considered:** extend the rail/page with a new "select an orchestrator header directly" mechanism (e.g., `SelectByOrchID`) so a top-level coordinator's own need becomes reachable too. Rejected as out of scope for this change (see Non-Goals) — it would add a second selection mechanism alongside `SelectByTaskID` for one edge case, when the coordinator's need is still visibly surfaced today via the header's existing rollup glyph; only the DIRECT-JUMP reachability is deferred, not the visibility.

### 4. `HeraPage.JumpToNextNeedsInput` reuses `JumpToTask` verbatim — no new ancestor-expand/reattach/focus logic

`Rail.NextNeedsInputTaskID` returns a plain argus task id; `HeraPage.JumpToNextNeedsInput` looks one up and calls the EXISTING `JumpToTask(id)` with it — the exact same ancestor-expand (`EnsureAncestorsExpanded` per `OrchIDsForTask`) + `SelectByTaskID` + reattach-a-dead/suspended-session + focus sequence every other jump (the switcher, the plan widget's leaf-Enter) already goes through. No new selection/expansion/focus code is written for `ctrl+g` at all — only the "which task id is next" scan is new.

## Risks / Trade-offs

- **[Risk] A top-level coordinator that is itself genuinely blocked on a prompt is invisible to `ctrl+g`'s cycle** (Decision 3). → Mitigation: it is NOT invisible to the operator — the coordinator's header still shows the rolled-up `(?)` glyph exactly as it does today (BUG-028), and it remains reachable via the rail's own `j`/`k` cursor nav or the `ctrl+j` switcher (whose landing has the identical `JumpToTask` limitation, so this is not a regression relative to the switcher). Only the DEDICATED direct-jump reachability is scoped out.
- **[Risk] `ctrl+g` (BEL, 0x07) intercepted unconditionally means it never reaches a focused Hera pane's PTY** (a nested program's own ctrl+g binding, if any, becomes unreachable from inside a Hera pane). → Mitigation: same accepted trade-off class as `ctrl+j`/`ctrl+k` (hera-nav-palette design.md); confirmed free in every context before choosing it (Ctrl+O is taken by `open_repo`, which is why G was chosen instead).
- **[Risk] The `closeTaskSwitcherModal` focus-restore fix touches previously-stable dispatch code.** → Mitigation: mirrors `closeCommandPaletteModal`'s already-shipped, already-tested three-way fallback chain exactly; covered by the new Tasks-tab switcher smoke test (Esc must return focus to `a.tasklist`, not silently to `a.heraPage`).

## Migration Plan

Single PR, no data/schema migration, no feature flag (per this repo's Breaking Changes Policy). `openspec archive add-hera-jump-question` runs in the same PR before merge. Rollback is a plain revert (no persisted state to unwind — keybindings and rail scan logic are pure code).

## Open Questions

None outstanding — both pieces of work were fully scoped by the coordinator's brief before implementation began.
