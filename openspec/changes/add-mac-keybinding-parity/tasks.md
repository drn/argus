**Design doc:** `openspec/changes/add-mac-keybinding-parity/design.md`

## 1. Tests

- [ ] 1.1 Write failing tests for each scenario in `specs/macos-app/spec.md`: global shortcuts (tab switch, help, destroy, fork, open-repo, open-PR, jump-to-needs-input, overflow prune)
- [ ] 1.2 Write failing tests for task rail quick actions (context menu status advance/revert, archive shortcut, pin shortcut)
- [ ] 1.3 Write failing tests for task rail filter access (Cmd+F focus, hera-managed toggle)
- [ ] 1.4 Write failing tests for terminal view shortcuts (Cmd+arrows nav, Shift+scroll, copy-output) and the non-regression scenario (unclaimed keystrokes still reach `POST /input` unchanged)
- [ ] 1.5 Write failing tests for the Claude session switcher (toolbar button opens picker, selecting a session attaches to it)
- [ ] 1.6 Confirm every "it should" criterion in `design.md`'s Acceptance criteria section has a corresponding failing test (Prove-It Pattern)

## 2. Global keyboard shortcuts and overflow actions

**Depends on:** Stage 1

- [ ] 2.1 Check each candidate chord (tab switch, help, destroy, fork, open-repo, open-PR, jump-to-needs-input) against macOS HIG reserved shortcuts; pick a Shift/Option-augmented alternative wherever the natural TUI-mirroring chord collides (per design.md Risks)
- [ ] 2.2 Add `.commands`/`.keyboardShortcut` wiring in `ArgusApp.swift`/`ContentView.swift` for tab switch, help-sheet, destroy, fork, open-repo, open-PR, jump-to-needs-input
- [ ] 2.3 Build the shortcuts-help sheet view
- [ ] 2.4 Add "Prune stale worktrees" to the toolbar overflow menu, wired to the existing prune action
- [ ] 2.5 Verify tests from 1.1 pass

## 3. Task rail quick actions

**Depends on:** Stage 1

- [ ] 3.1 Add a `.contextMenu` to `TaskRow.swift` with status-advance/status-revert items calling the existing status-transition logic
- [ ] 3.2 Add `.keyboardShortcut` to the existing archive and pin actions in `TaskActions.swift`
- [ ] 3.3 Verify tests from 1.2 pass

## 4. Task rail filter access

**Depends on:** Stage 1

- [ ] 4.1 Add Cmd+F shortcut in `Sidebar.swift` that moves keyboard focus to the filter field
- [ ] 4.2 Add a persistent, visible toggle control in the sidebar's filter bar for hera-managed task visibility, wired to the existing filter state
- [ ] 4.3 Verify tests from 1.3 pass

## 5. Terminal view keyboard shortcuts

**Depends on:** Stage 1

- [ ] 5.1 Define the intercepted-chord allowlist as a single Swift constant (e.g. `TerminalCoordinator.interceptedChords`) per design.md D2/Risks
- [ ] 5.2 Extend the existing local `NSEvent` monitor in `TerminalTab.swift` to intercept Cmd+Up/Down (task switch), Cmd+Left/Right (pane focus), and Shift+Up/Down/PageUp/PageDown/End (scroll) ahead of SwiftTerm, dispatching to the corresponding rail/pane/scroll action
- [ ] 5.3 Add a copy-visible-output shortcut, added to the same allowlist
- [ ] 5.4 Add a unit test asserting no chrome-level `.keyboardShortcut` declared in Stage 2-4 reuses a chord from the Stage 5.1 allowlist
- [ ] 5.5 Add a smoke test confirming intercepted chords never appear in `POST /input` payloads during a live terminal session, and that all other keystrokes still do (non-regression)
- [ ] 5.6 Verify tests from 1.4 pass

## 6. Claude session switcher

**Depends on:** Stage 1

- [ ] 6.1 Add a toolbar button in the Terminal tab (near the existing tab bar) that opens a picker sheet
- [ ] 6.2 Build the picker sheet listing the current task's available Claude sessions (reuse the existing sheet pattern from rename/new-task)
- [ ] 6.3 Wire session selection to attach the terminal view to the chosen session
- [ ] 6.4 Verify tests from 1.5 pass

## 7. Documentation and wrap-up

**Depends on:** Stages 2-6

- [ ] 7.1 Document the terminal-safe-chord allowlist mechanism as a gotcha in `context/knowledge/gotchas/macos-app.md` (non-obvious invariant: why Cmd-chords are intercepted pre-SwiftTerm rather than via SwiftUI Commands)
- [ ] 7.2 Update the README's Reference keybinding table if one covers the mac app (check first; skip if none exists)
- [ ] 7.3 Run `make mac-build` and `make mac-test`; fix any failures
- [ ] 7.4 `openspec archive add-mac-keybinding-parity` (or the manual merge-and-move fallback) on the change branch before merge, per this repo's CLAUDE.md
