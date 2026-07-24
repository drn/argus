## Why

`ctrl+g` already jumps to the next role needing input, force-expanding any folded ancestor coordinator so the target is never swallowed (BUG-007). That expand-on-jump is correct and stays. The friction is what happens AFTER: once the operator fixes the problem, they have no way back to their prior tidy fold state except manually re-folding everything by hand — the jump has no undo.

## What Changes

- The rail (in-memory, TUI-side, not persisted) now holds an optional **excursion snapshot**: the fold/expand state of every orchestrator plus the previously-selected task, captured the INSTANT the whole-rail needs-input count transitions from fully-at-rest (0) to interrupted (>=1) — never at keypress time, so it captures the operator's true pre-interruption layout before they've had a chance to fuss with it themselves.
- A second (or third) needs-input signal appearing while one is already open folds into the SAME excursion — it does not retrigger a capture.
- `ctrl+g` (existing binding) is reworked: with one or more roles still needing input it behaves exactly as before (jump/cycle to the next candidate); once the count drops to zero, a repeat press instead RESTORES the captured fold/selection state and discards the snapshot.
- A new binding, `ctrl+b` ("restore rail"), manually ends the excursion at any time regardless of the remaining needs-input count — the explicit "I'm done looking, put it back" action. Restoring never hides remaining problems: a folded coordinator's header still surfaces its own needs-input signal, so the operator can always tell more work exists.
- **Deviation from the originally-discussed key**: the coordination conversation that approved this design named `ctrl+shift+g` as the manual-restore binding. That chord is not implementable in argus's keymap — `ctrl+<letter>` is a C0 control byte with no room for a Shift bit at the wire level (most terminals send the identical byte for `ctrl+g` and "`ctrl+shift+g`"), and `internal/tui/keymap`'s `Parse` already documents and rejects combining `shift` with `ctrl+<letter>`. `ctrl+b` is substituted: unused in every context (`CtxGlobal`/`CtxAgent`/`CtxHeraRail`/`CtxTaskList`), and doesn't alias a structural key the way `ctrl+i`(tab)/`ctrl+m`(enter) would.

## Capabilities

### Modified Capabilities

- `hera-view`: `ctrl+g`'s "no role needs input" no-op becomes a rail-restore action when an excursion snapshot is held; a new `ctrl+b` binding manually restores the rail at any time; the rail gains excursion-snapshot state and its arm/re-arm/discharge rules.
- `keybindings`: a new `global.restore_rail` action (default `ctrl+b`), dispatched via the same unconditional-global mechanism `global.jump_needs_input`/`global.palette` already use.

## Impact

- **Modified code:**
  - `internal/tui/hera/rail.go` — excursion snapshot type + capture/restore/count methods, hooked into `SetModel`.
  - `internal/tui/hera/model.go` — `Model.NeedsInputTotalCount()`, a fold-independent whole-rail needs-input count.
  - `internal/tui/app.go` — `jumpToNextNeedsInput` reworked with the count>=1/count==0 branches; new `restoreHeraRailExcursion` for `ctrl+b`.
  - `internal/tui/keymap/actions.go` — new `ActGlobalRestoreRail` action (default `ctrl+b`), `ActGlobalJumpNeedsInput`'s help label updated.
  - `internal/tui/commandpalette_actions.go` — palette wiring for the new action.
- **No schema change, no daemon RPC, no persistence** — the snapshot is in-memory, TUI-side only (explicitly not part of the persisted `hera.rail_view_state` blob).
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make / Go-build wiring is added or changed. The quality gate stays `make pre-pr`.
