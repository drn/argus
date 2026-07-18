## 1. Keymap plumbing (keybindings capability)

- [ ] 1.1 Rebind `ActAgentSwitcher` default from `ctrl+k` to `ctrl+j` in `internal/tui/keymap/actions.go` `defaultSpecs[CtxAgent]`.
- [ ] 1.2 Add `ActGlobalPalette` (`global.palette`, default `ctrl+k`) to `CtxGlobal`'s `defaultSpecs`/`actionLabels`/`contextOrder`.
- [ ] 1.3 Write/update `internal/tui/keymap` unit tests: default `ctrl+j`→`ActAgentSwitcher`, `ctrl+k`→`ActGlobalPalette`, both rebindable via config override, both appear in `HelpRows`.
- [ ] 1.4 In `handleGlobalKey` (`app.go`), resolve `CtxGlobal` once at the top (ahead of the `suppressRune`/mode gate) and special-case `ActGlobalPalette` there so it fires unconditionally, mirroring the existing Ctrl+Q/Ctrl+Z placement; leave the rest of the existing gated `CtxGlobal` switch unchanged.
- [ ] 1.5 Add a smoke test asserting `ctrl+k` opens the palette from `modeAgent`, `modeTaskList` (Tasks tab), and both Hera rail and Hera pane focus.

## 2. Action registry + dispatch-by-ActionID (command-palette capability)

- [ ] 2.1 Extract any multi-line `handleGlobalKey`/`handleAgentKey` switch-case bodies relevant to the palette's initial scope into single named `App` methods (behavior-preserving; each extraction gets/keeps a passing test for the physical key before wiring the registry).
- [ ] 2.2 Build a `map[keymap.Action]func()` registry per relevant context (`CtxGlobal`, `CtxTaskList`, `CtxAgent`) at `App` construction, each entry calling the same named method its switch case calls.
- [ ] 2.3 Build the equivalent registry for `CtxHeraRail`, reusing the existing `OnXxx` callback-field/`fire` pattern (`heraactions.go`) — no new mutation logic, just a second caller of the existing callbacks.
- [ ] 2.4 Add `App.paletteApplicableActions() []keymap.Action` (or equivalent) that returns the ordered, filtered action list for the current mode/focus region per the design's context table.
- [ ] 2.5 Add `App.invokeAction(act keymap.Action)` that looks up `act` in the current context's registry and calls it directly (no synthetic `tcell.EventKey`).
- [ ] 2.6 Unit-test the registry: every action reachable from `paletteApplicableActions()` for a given context is present in that context's registry (no dangling "listed but not invokable" entries).

## 3. Command palette modal + open/filter/invoke flow

- [ ] 3.1 Create `CommandPaletteModal` (new type) mirroring `TaskSwitcherModal`'s filter/cursor mechanics: type-to-filter by label substring, arrow-key cursor movement, `Enter` confirms.
- [ ] 3.2 Row rendering: label + right-aligned resolved key chord, sourced from `keymap.HelpRows`-equivalent data (reuse `actionLabels`/`Keymap.Resolve` output, not a separate hardcoded list).
- [ ] 3.3 Wire `ActGlobalPalette`'s handler to build the applicable action list (task 2.4), open `CommandPaletteModal`, and on `Enter` call `App.invokeAction` (task 2.5) then close the palette.
- [ ] 3.4 Implement the resolved applicable-action hierarchy (design.md Decision 2 table): focused-element ∪ current-tab-rail ∪ `CtxGlobal` (uniform, not accidental-typing-gated), with `modeAgent` as the sole exception keeping its pre-existing `CtxGlobal`-off boundary. Add the two fixed Hera literal-action rows (fullscreen; copy, terminal-panes only) when a Hera pane is the focused element.
- [ ] 3.5 Smoke test: open palette, type a filter substring, confirm only matching rows remain, press Enter, confirm the action's effect fires and the palette closes.
- [ ] 3.6 Smoke test: an invoked-but-inapplicable action (guard not satisfied) no-ops without crashing.
- [ ] 3.7 Smoke test: no cross-tab bleed — a Hera-focused palette (rail or pane) never lists `CtxTaskList`/`CtxSettings` rows and vice versa; a Hera pane-focused palette lists the fullscreen row (and the copy row only for a live terminal pane, absent in the coordinator Details/plan region).

## 4. Unified task/role switcher — Hera reach

- [ ] 4.1 Extend `openTaskSwitcher`'s entry-building to include Hera role bindings (task ID, name, orchestrator context) alongside plain argus tasks, reusing the `heraManaged` union `needsInputForHeraRail` already computes; keep the existing needs-input-first-then-alphabetical sort.
- [ ] 4.2 Add `HeraPage.OnSwitcher func()` callback field; wire the App to call the (now-unified) `openTaskSwitcher()`.
- [ ] 4.3 Add `case tcell.KeyCtrlJ:` to `HeraPage.InputHandler`'s top-level switch (alongside the existing `Ctrl+Z` case), always consuming the key and firing `OnSwitcher`, regardless of `FocusRail`/`FocusCoord`/`FocusAgent`.
- [ ] 4.4 On selecting a Hera-role entry, reuse the `jumpToLeaf` ancestor-expansion pattern: `Rail.Model().OrchIDsForTask(id)` → `Rail.EnsureAncestorsExpanded(orchID)` per ancestor → `Rail.SelectByTaskID(id)` → `focus.SetRegion(...)`.
- [ ] 4.5 Smoke test: `ctrl+j` opens the switcher from Hera rail focus and from a live Hera pane (coordinator and worker), and the byte never reaches the pane's PTY.
- [ ] 4.6 Smoke test: selecting a Hera role buried under a closed ancestor coordinator expands the chain and lands focus on the role's pane.
- [ ] 4.7 Smoke test: with multiple needs-input entries present, opening the switcher and pressing Enter immediately lands on the first one; arrow-down visits the rest before non-needs-input entries.

## 5. Rail partial-fold reveal

- [ ] 5.1 In `internal/tui/hera/rail.go` `appendOrchWorkers`, add the collapsed-but-`SubtreeNeedsInput` branch: recurse only into children whose own `ShowsNeedsInput()`/`SubtreeNeedsInput` is true; skip all others (no placeholder row).
- [ ] 5.2 Confirm the branch recurses correctly through nested closed sub-coordinators (a revealed child that is itself a closed coordinator with a needs-input descendant re-applies the same branch).
- [ ] 5.3 Confirm revealed rows are fully normal/selectable rows requiring no changes to `step()`/`selectable()`/`clampCursor`/drawing/mutation-by-`Selection`.
- [ ] 5.4 Confirm `Space` (`ToggleCollapse`) on a partially-revealed coordinator behaves exactly as before (full expand/collapse, no special partial-fold state to unwind).
- [ ] 5.5 Unit tests: single hidden leaf under one closed coordinator; two-level nested closed coordinators; multiple hidden leaves under one coordinator; unrelated siblings stay hidden; a coordinator with no needs-input descendant renders unchanged (regression guard).
- [ ] 5.6 Revealed rows render identically to a normal open-fold row (RESOLVED: no distinct "peeking" styling) — confirm no special-casing was accidentally introduced in the draw path.

## 6. Documentation

- [ ] 6.1 Verify the `?` help overlay reflects the `ctrl+j`/`ctrl+k` rebind automatically (it is GENERATED from the keymap) — add/confirm a `help_test.go` assertion rather than assuming.
- [ ] 6.2 Update the README Reference keybinding table for the rebind and the two new Hera-reachable keys.
- [ ] 6.3 Add invariants to `context/knowledge/gotchas/keybindings.md`: the `ctrl+j`/`ctrl+k` rebind, the new unconditional-dispatch-while-still-rebindable pattern, the action-registry/dispatch-by-ActionID mechanism.
- [ ] 6.4 Add invariants to `context/knowledge/gotchas/hera-view.md`: the `ctrl+j` switcher literal case (mirroring Ctrl+Z), the `ctrl+k` no-longer-forwards change, the rail partial-fold reveal mechanism and its "extend `appendOrchWorkers`, don't fork a parallel traversal" rationale.

## 7. Verification and ship

- [ ] 7.1 Run `make test` and `make test-cover` on touched packages (`internal/tui`, `internal/tui/keymap`, `internal/tui/hera`); confirm ≥95% on touched packages (90% for UI smoke-only code) per this repo's testing rules.
- [ ] 7.2 Archive this OpenSpec change (`openspec archive hera-nav-palette` or the manual merge-into-base-specs + move-to-archive fallback) in the same PR, before merge.
- [ ] 7.3 Run `make pre-pr` clean (build+vet+fmt-check+lint-pr+vuln+test-cover-gate).
- [ ] 7.4 Open the PR via `mcp__argus__iris_gh_pr_create`.
