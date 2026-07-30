## 1. Extract a width-parameterized rerender-kick core in app.go

- [x] 1.1 Extract `App.maybeKickRerender`'s body into `maybeKickRerenderAtWidth(task *model.Task, sess agent.SessionHandle, panelCols uint16)`; `maybeKickRerender` becomes a thin wrapper computing `panelCols` via `computePTYSize()` and delegating.
- [x] 1.2 Add `App.heraKickRerender(taskID string, panelCols uint16)`: resolves `task` via `a.db.Get(taskID)` and `sess` via `a.runner.Get(taskID)`, then delegates to `maybeKickRerenderAtWidth`. No-ops cleanly on a nil/errored task lookup.
- [x] 1.3 Unit tests for both: existing `maybeKickRerender` behavior unchanged (regression); `heraKickRerender` covering nil task, nil session, and the kick-fires path.

## 2. Wire a Hera-facing callback

- [x] 2.1 Add `RerenderKicker func(taskID string, panelCols uint16)` type and `HeraPage.SetRerenderKicker(fn RerenderKicker)` in `internal/tui/hera` (mirrors `SetSessionResolver`).
- [x] 2.2 Wire `a.heraPage.SetRerenderKicker(a.heraKickRerender)` in app setup, next to the existing `SetSessionResolver` wiring.
- [x] 2.3 Remote mode: confirmed nil exactly like `resolve` (both wired only inside the `heraReader != nil` local-mode block) — `bindPane`/`Draw` never touch it under `--remote` (unreachable: `applySelection`/`Draw`'s pane paths are gated on `p.remote` upstream).

## 3. Evaluate the kick with a fresh, correct width

- [x] 3.1 **Design change from the original plan:** the kick is NOT evaluated in `bindPane`. `bindPane` runs synchronously in the input handler, before `Draw()` has had a chance to give a newly-shown pane its real rect — reproduced during implementation: a coordinator selected first (details mode never shows the agent pane, so its tracked width stays 0), then a worker selected for the first time in the session, would silently skip the check at `bindPane`-time. Instead, `HeraPage.maybeKickPaneRerender(bound, kickedFor, cols)` is called from each of `Draw`'s four `SetRect`+`Draw` call sites (fullscreen coord/agent, split coord/agent), right after `SetRect`, so `cols` is always fresh. New `HeraPage.coordKickedFor`/`agentKickedFor` fields (paired with `coordBound`/`agentBound`) suppress a redundant call every frame while the same task stays bound; `bindPane` resets the relevant marker to `""` on unbind so a later rebind to the same task still gets evaluated.
- [x] 3.2 Confirmed safe: the kick fires from `Draw()` after the pane is already bound and painting; a fired kick stops the session, and the existing (unmodified) exit-handler resume path (`handleSessionExitUI`, already task-keyed and global — not agent-view-specific) brings it back at the current dimensions on a later tick/reattach. No new nil-deref or double-bind risk — `maybeKickPaneRerender` only reads/writes the two new string fields and calls the (nil-checked) `kickRerender` callback.
- [x] 3.3 Confirmed: not wired into `reconcileOne` (tick-driven late-bind/dead-handle path) — matches the main agent view's entry-only semantics. Documented in `maybeKickPaneRerender`'s doc comment.

## 4. Tests

- [x] 4.1 `TestPanes_DrawInvokesRerenderKicker` (internal/tui/hera): proves `Draw` invokes the kicker with each pane's fresh width, exactly once per genuine bind (not on a repeated Draw at the same bound task), and that unbind+rebind to the SAME task re-evaluates (kickedFor reset). Deliberately selects the worker BEFORE any Draw so the agent pane's width is genuinely 0 at bind time — the exact scenario that would defeat a bindPane-time check.
- [x] 4.2 Same test proves both `coordPane` and `agentPane` are wired (both produce a kick with a nonzero width from the same `Draw` pass).
- [x] 4.3 `internal/tui`: `TestMaybeKickRerenderAtWidth_KicksOnGenuineDrift` (new — the actual `RerenderKick` branch had no prior TUI-level test, only the pure predicate and the DeferPrompt branch), `TestHeraKickRerender_UnknownTaskIsNoop`, `TestHeraKickRerender_NoRunnerSessionIsNoop`. Existing `TestMaybeKickRerender_TUIDefersWhenBlockedOnPrompt`/`TestIsRedundantAttach` pass unchanged (regression).
- [x] 4.4 Offline reproduction (real dogfood session log, task `1785307013068092000` "sketch-links") was performed via a throwaway scratch `_test.go` during investigation, deleted before committing — not part of the diff. Documented in the PR description and in `gotchas/hera-view.md`/`gotchas/pty-terminal.md`.

## 5. Docs and gates

- [x] 5.1 Added a `context/knowledge/gotchas/hera-view.md` bullet (BUG-074) describing the kick-on-bind behavior, why it's evaluated from `Draw()` not `bindPane`, and the rationale.
- [x] 5.2 Cross-referenced from `context/knowledge/gotchas/pty-terminal.md`'s "Width-mismatched scrollback can only be repaired by kill+resume" bullet (now lists three fix sites: TUI main view, API, and Hera).
- [x] 5.3 `make pre-pr` green (build/vet/fmt-check/lint-pr/test-cover-gate all clean; `vuln` fails only on pre-existing stdlib CVEs, CI continue-on-error; 2 pre-existing `internal/agent` profile-env tests fail only due to this hera-worker sandbox's own `ARGUS_*` env leaking into the test subprocess, unrelated to this diff).
- [x] 5.4 `openspec archive fix-hera-pane-rerender-kick` run in the same PR, before merge.

## 6. Follow-up (not in this change)

- [x] 6.1 Flagged to the coordinator (chat, not a separate artifact): `ShouldKickRerender`'s margin is cols-only, but the clearest reproduction evidence (holding cols fixed, varying rows) was row-driven. Real terminal resizes usually change both dimensions together, so the existing margin should catch most real drift, but a symmetric row margin is an open question for a follow-up change (it would also touch the web API's `Server.maybeKickRerender`, outside this Hera-scoped fix) — see design.md's Open Questions.
