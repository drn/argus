## 1. Rail excursion state machine

- [x] 1.1 Add `Model.NeedsInputTotalCount()` — a fold-independent whole-rail needs-input count across every orchestrator section (Pinned/Active/Archived) and Freelance, including coordinator-kind roles.
- [x] 1.2 Add `railSnapshot` (fold/collapse maps, freelance/archive section bools, focused kanban group, selection ref) plus `Rail.captureExcursionSnapshot`/`RestoreExcursion`/`EnsureExcursionArmed`/`HasExcursionSnapshot`/`NeedsInputCount`.
- [x] 1.3 Add `Rail.noteExcursionTransition`, hooked into `Rail.SetModel`, implementing the 0→≥1 capture / fold-into-existing-excursion / re-arm-after-explicit-restore rules.

## 2. App + keymap wiring

- [x] 2.1 Rework `App.jumpToNextNeedsInput` (ctrl+g): count>=1 behaves as before (plus arming the snapshot); count==0 restores a held snapshot instead of a bare no-op.
- [x] 2.2 Add `App.restoreHeraRailExcursion` (ctrl+b): unconditional restore, silent no-op when nothing held.
- [x] 2.3 Add `keymap.ActGlobalRestoreRail` (default `ctrl+b`) to `defaultSpecs`/`actionLabels`/`contextOrder` for `CtxGlobal`; update `ActGlobalJumpNeedsInput`'s label to reflect the new cycle/restore semantics.
- [x] 2.4 Add the new case to `app.go`'s unconditional `CtxGlobal` dispatch switch (same bucket as `ActGlobalJumpNeedsInput`/`ActGlobalPalette`).
- [x] 2.5 Wire `ActGlobalRestoreRail` into `commandpalette_actions.go`'s global registry.

## 3. Tests

- [x] 3.1 Unit tests (`internal/tui/hera/excursion_test.go`): `Model.NeedsInputTotalCount` correctness; fresh-capture-on-0→1; fold-into-same-excursion on a second interruption; re-arm-after-explicit-restore-mid-excursion; no-op restore when nothing held; snapshot survives a count-back-to-0 resolution until explicitly discharged.
- [x] 3.2 Keymap unit test: `ctrl+b` resolves to `ActGlobalRestoreRail` in `CtxGlobal` (`TestResolve_Defaults`); `TestDefaultsParseAndUnique`/`TestInventoryConsistency` cover parse/uniqueness/completeness automatically.
- [x] 3.3 Help-overlay assertion (`internal/tui/modal/help_test.go`, `TestHelpModal_Draw`): `ctrl+b` / "restore rail (end excursion)" are discoverable in the generated overlay.
- [x] 3.4 Command-palette label fix (`internal/tui/commandpalette_test.go`): update the two exact-match assertions for `ActGlobalJumpNeedsInput`'s new label text.
- [x] 3.5 SimulationScreen smoke tests (`internal/tui/ctrlg_test.go`): ctrl+g restores when the count drops to zero; ctrl+b restores manually from within a live fullscreen agent view without tearing it down, and the still-outstanding problem remains reachable via a subsequent ctrl+g; ctrl+b is a silent no-op when nothing is held.

## 4. Docs

- [x] 4.1 Add gotcha bullets to `context/knowledge/gotchas/hera-view.md` (transition-triggered capture invariant, fold-into-existing-excursion vs re-arm-after-restore, ctrl+b key substitution rationale).
- [x] 4.2 Mirror the new `ctrl+b` binding into the README Reference keybinding table.

## 5. Archive

- [x] 5.1 Run `openspec archive add-ctrlg-excursion` (or the manual merge-into-base-specs + move-to-archive fallback) before merge, in the same PR.
