## 1. `TerminalPane` banner overlay

- [x] 1.1 Add `closedOutBannerShown bool` field + doc comment.
- [x] 1.2 Add `ShowClosedOutBanner()` / `DismissClosedOutBanner()` / `ClosedOutBannerShown()` methods; fire `notifyBranchChange()` on an actual state change (mirrors `SetPending`).
- [x] 1.3 Reset `closedOutBannerShown` in `ResetVT()`.
- [x] 1.4 Gate `Draw()`: when armed and the session is not alive, render the banner and return BEFORE the existing "no content" placeholder / replay branches.
- [x] 1.5 Add `drawClosedOutBanner` (or equivalent) — centered, multi-line, in-pane text; no new emulator/goroutine.

## 2. `heraReattach` toggle

- [x] 2.1 Replace the close-out branch's bare `statusbar.SetError` + return with a call to a new `heraReattachClosedOut(t *model.Task)` helper.
- [x] 2.2 `heraReattachClosedOut` resolves `a.heraPage.AgentPane()`, checks `ClosedOutBannerShown()`: if armed, dismiss + `statusbar.SetInfo(...)`; else arm + `statusbar.SetError(...)` (preserve the existing "closed out" substring so `TestSmoke_HeraReattachRefusesClosedOutDeadWorker` keeps passing unmodified).

## 3. Tests

- [x] 3.1 `internal/tui/terminal`: table test(s) for `ShowClosedOutBanner`/`DismissClosedOutBanner`/`ClosedOutBannerShown`, `ResetVT` clearing the flag, and `Draw()` rendering the banner text when armed+dead vs. falling through when dismissed (with and without replay content).
- [x] 3.2 `internal/tui/terminal`: `Draw()` never shows the banner over a live session even if armed (defensive).
- [x] 3.3 `internal/tui`: SimulationScreen smoke test extending `TestSmoke_HeraReattachRefusesClosedOutDeadWorker`'s fixture — first Enter arms the pane's banner (screen shows banner text), second Enter dismisses it (screen falls through to replay/placeholder), status bar messages differ between the two.
- [x] 3.4 `internal/tui`: smoke test that leaving the closed-out task's row (rebinding the agent pane elsewhere) and returning resets `ClosedOutBannerShown()` to false.
- [x] 3.5 Run `make test-cover` on touched packages; target ≥95% (internal/tui/terminal) / ≥90% (UI-smoke-only additions in internal/tui).

## 4. Docs

- [x] 4.1 Add a gotcha bullet to `context/knowledge/gotchas/hera-view.md` (the two-Enter toggle mechanic + why the state lives on `TerminalPane`, not `App`).

## 5. Spec

- [x] 5.1 Delta spec: MODIFY `hera-coordination`'s "Enter refuses to restart a dead-session worker awaiting close-out" requirement + scenarios.
- [x] 5.2 Archive this change into `openspec/specs/` in the SAME PR before merge.
