**Design doc:** `openspec/changes/add-mac-keybinding-parity/design.md`

## 1. Tests

- [x] 1.1 Write failing tests for each scenario in `specs/macos-app/spec.md`: global shortcuts (tab switch, help, destroy, fork, open-repo, open-PR, jump-to-needs-input, overflow prune) — pure-logic seams tested (needs-input nav, prune client call); pure SwiftUI-wiring scenarios documented as untestable glue (no XCUITest harness exists in this repo)
- [x] 1.2 Write failing tests for task rail quick actions (context menu status advance/revert, archive shortcut, pin shortcut) — status cycling + pin round-trip tested; menu-wiring documented as untestable glue
- [x] 1.3 Write failing tests for task rail filter access (Cmd+F focus, hera-managed toggle) — no new logic; documented as untestable glue
- [ ] 1.4 Write failing tests for terminal view shortcuts (Cmd+arrows nav, Shift+scroll, copy-output) and the non-regression scenario (unclaimed keystrokes still reach `POST /input` unchanged) — allowlist-membership tested (`TerminalChordsTests.swift`); live NSEvent-monitor dispatch + full non-regression smoke test deferred to Stage 5 (5.4/5.5)
- [ ] 1.5 Write failing tests for the Claude session switcher (toolbar button opens picker, selecting a session attaches to it) — BLOCKED: escalated to coordinator (no daemon REST endpoint exists for `internal/claudesession`); paused pending decision
- [x] 1.6 Confirm every "it should" criterion in `design.md`'s Acceptance criteria section has a corresponding failing test (Prove-It Pattern) — done for all except the paused session-switcher requirement

## 2. Global keyboard shortcuts and overflow actions

**Depends on:** Stage 1

- [x] 2.1 Check each candidate chord (tab switch, help, destroy, fork, open-repo, open-PR, jump-to-needs-input) against macOS HIG reserved shortcuts; pick a Shift/Option-augmented alternative wherever the natural TUI-mirroring chord collides (per design.md Risks) — final: ⌘1-4 tabs, ⇧⌘/ help, ⌘⌫ destroy, ⇧⌘B fork, ⇧⌘E open-repo, ⇧⌘U open-PR, ⇧⌘J jump-to-needs-input
- [x] 2.2 Add `.commands`/`.keyboardShortcut` wiring in `ArgusApp.swift`/`ContentView.swift` for tab switch, help-sheet, destroy, fork, open-repo, open-PR, jump-to-needs-input
- [x] 2.3 Build the shortcuts-help sheet view
- [x] 2.4 Add "Prune stale worktrees" to the toolbar overflow menu, wired to the existing prune action (new app-chrome-level overflow menu; new `ArgusClient.pruneCompleted()` client method added, no daemon change needed)
- [x] 2.5 Verify tests from 1.1 pass

## 3. Task rail quick actions

**Depends on:** Stage 1

- [x] 3.1 Add a `.contextMenu` to `TaskRow.swift` with status-advance/status-revert items calling the existing status-transition logic
- [x] 3.2 Add `.keyboardShortcut` to the existing archive action and the new pin action (net-new UI; no pin affordance existed before this change) in `TaskActions.swift`
- [x] 3.3 Verify tests from 1.2 pass

## 4. Task rail filter access

**Depends on:** Stage 1

- [x] 4.1 Add Cmd+F shortcut in `Sidebar.swift` that moves keyboard focus to the filter field
- [x] 4.2 Add a persistent, visible toggle control in the sidebar's filter bar for hera-managed task visibility, wired to the existing filter state
- [x] 4.3 Verify tests from 1.3 pass

## 5. Terminal view keyboard shortcuts

**Depends on:** Stage 1

- [x] 5.1 Define the intercepted-chord allowlist as a single Swift constant (e.g. `TerminalCoordinator.interceptedChords`) per design.md D2/Risks — final: `TerminalChords.intercepted` (`macos/Sources/ArgusKit/TerminalChords.swift`), a `KeyChord`/`Modifier` value type kept in ArgusKit rather than the App target so Stage 1's `TerminalChordsTests` cover it with real tests; the Cmd+Shift+U defensive fallback (5.2) is a deliberately SEPARATE constant, not folded into this set
- [x] 5.2 Extend the existing local `NSEvent` monitor in `TerminalTab.swift` to intercept Cmd+Up/Down (task switch), Cmd+Left/Right (pane focus), and Shift+Up/Down/PageUp/PageDown/End (scroll) ahead of SwiftTerm, dispatching to the corresponding rail/pane/scroll action — implemented as a SECOND local `.keyDown` monitor alongside the existing `.leftMouseDown` one (`FocusTakingTerminalView`); "pane focus" resolved to cycling the detail pane's Terminal→Diff→Files→Info tab (`AppState.cycleDetailTab`, no split-pane concept exists in this app); task switch clamps at the rail's ends (`AppState.selectPreviousTask`/`selectNextTask` over `TaskNavigation`, ArgusKit) rather than wrapping like `jumpToNextNeedsInput`; scroll goes through `TerminalController.scrollLineUp/scrollLineDown/scrollPageUp/scrollPageDown/scrollToBottom` (SwiftTerm's `scrollUp`/`scrollDown`/`scroll(toPosition:)`, never `pageUp()`/`pageDown()`, which can forward bytes to the PTY in alt-screen mode); the same monitor also swallows Cmd+Shift+U (open PR) as a documented, structurally separate fallback per Stage 2's review finding
- [x] 5.3 Add a copy-visible-output shortcut, added to the same allowlist — `TerminalController.copyVisibleOutput()` reads the visible viewport directly via SwiftTerm's `Terminal.getLine(row:)` + `BufferLine.translateToString(...)` (already scroll-offset-aware), not select-all+getSelection
- [x] 5.4 Add a unit test asserting no chrome-level `.keyboardShortcut` declared in Stage 2-4 reuses a chord from the Stage 5.1 allowlist — `ChromeShortcutCollisionTests.swift` (ArgusKitTests), hardcoding the grep'd chrome-chord list (ArgusKit can't introspect SwiftUI declarations) plus a count canary
- [x] 5.5 Add a smoke test confirming intercepted chords never appear in `POST /input` payloads during a live terminal session, and that all other keystrokes still do (non-regression) — no XCUITest/UI-automation harness exists in this repo (confirmed by every prior stage); delivered as (a) the ArgusKit-level allowlist-membership tests (Stage 1's `TerminalChordsTests`, now passing) proving the boundary is exhaustive and correct, plus (b) an explicit documented code-review-level self-review of the monitor's two-path structure (intercepted/fallback → swallow via `nil`, else → return the event completely unmodified) — see the Stage 5 report for the self-review; not an automated end-to-end smoke test
- [x] 5.6 Verify tests from 1.4 pass — `make mac-test`: 244 tests / 19 suites, all green

## 6. Claude session switcher

**Depends on:** Stage 1

Resolved mid-implementation: this requirement needs two new minimal daemon REST endpoints (`internal/claudesession` had no HTTP route) — escalated to and approved by the coordinator; see design.md D3 and `specs/rest-api/spec.md`. Split into 6a (daemon) and 6b (mac client), dispatched in parallel against a fixed contract.

- [x] 6a.1 Add `GET /api/tasks/{id}/claude-sessions` and `POST /api/tasks/{id}/claude-session` per `specs/rest-api/spec.md`'s scenarios, with Go tests (switch reuses the existing `Runner.KickRerender` primitive, not a hand-rolled stop+start, to avoid a session-map race)
- [x] 6.1 Add a toolbar button in the Terminal tab (near the existing tab bar) that opens a picker sheet
- [x] 6.2 Build the picker sheet listing the current task's available Claude sessions (reuse the existing sheet pattern from rename/new-task)
- [x] 6.3 Wire session selection to attach the terminal view to the chosen session
- [x] 6.4 Verify tests from 1.5 pass (written as part of Stage 6 itself, since Stage 1 explicitly excluded this requirement pending the endpoint decision)

## 7. Documentation and wrap-up

**Depends on:** Stages 2-6

- [ ] 7.1 Document the terminal-safe-chord allowlist mechanism as a gotcha in `context/knowledge/gotchas/macos-app.md` (non-obvious invariant: why Cmd-chords are intercepted pre-SwiftTerm rather than via SwiftUI Commands)
- [ ] 7.2 Update the README's Reference keybinding table if one covers the mac app (check first; skip if none exists)
- [ ] 7.3 Run `make mac-build` and `make mac-test`; fix any failures
- [ ] 7.4 `openspec archive add-mac-keybinding-parity` (or the manual merge-and-move fallback) on the change branch before merge, per this repo's CLAUDE.md
