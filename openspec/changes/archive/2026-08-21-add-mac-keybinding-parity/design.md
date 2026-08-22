## Context

The macOS companion app (`macos/Sources/Argus/`) is a SwiftUI client of the argus daemon's REST + SSE API. Its task rail (`Sidebar.swift`) is a native SwiftUI `List`, which gets arrow-key row navigation for free. Today it has exactly four keyboard shortcuts, each attached directly to a button/menu command via `.keyboardShortcut(...)`: Cmd+N (new task), Cmd+R (rename), Cmd+Shift+S (schedules window), Cmd+Q (quit). No command palette, task/role switcher, or Hera-rail key handling exists.

The TUI (`internal/tui/keymap`) has a much larger rebindable keymap across several contexts (global, task list, agent view, file panel, settings, Hera rail), plus structural keys handled as literal cases in Go. The user wants every *useful* action reachable in the mac app — assume they never fall back to opening the TUI — but not a literal 1:1 replay: several TUI keys are vi-style single letters that would collide with normal text-field typing in a GUI, some need entirely new UI (a global command palette, a global task/role switcher) that's a bigger lift than this change, and some are meaningless until a separate change adds a Hera rail view to the mac app.

This design was converged interactively (see the coordinator/user brainstorm transcript) into a specific per-key resolution rather than a blanket policy; that resolution is captured in full under Decisions and in the acceptance criteria.

## Goals / Non-Goals

**Goals:**

- Every TUI action in scope (see Decisions) becomes reachable in the mac app via a button, menu item, context-menu item, or keyboard shortcut — never "only possible by opening the TUI."
- The agent/terminal-view chords that are direct ports of existing TUI bindings (Cmd+↑/↓, Cmd+←/→, Shift+scroll) work identically to the TUI, without leaking into the PTY stream.
- No regression to today's terminal input behavior: every keystroke not explicitly claimed by a new binding continues to reach `POST /api/tasks/{id}/input` byte-for-byte as it does today.

**Non-Goals (deferred, named follow-ups — not silently dropped):**

- Web SPA equivalent of the Claude session switcher (D3) — the two new daemon endpoints this change adds (`GET`/`POST /api/tasks/{id}/claude-sessions...`, see `specs/rest-api/spec.md`) are REST-reachable by any client, but this change only wires the mac app's UI to them. Follow-up: add a session-switcher affordance to the web SPA's terminal tab. Same shape as the existing "hera mutations are TUI-only" carve-out (CLAUDE.md's Frontend Parity section).

- A global command palette (Ctrl+K equivalent) — new UI, bigger lift than this change.
- A global task/role switcher beyond direct sidebar row selection (Ctrl+J equivalent) — the task-jump portion is already covered by clicking a row plus the new filter field; the Hera-role-jump portion is meaningless until the mac app has a Hera rail (separate change).
- Restore-rail (Ctrl+B equivalent) — no mac-app concept of a collapsed rail today; SwiftUI's native sidebar toggle already covers the underlying need.
- Copy branch/path (`c`) — deferred; no mechanism added this change.
- Manual refresh and the agent-view "show links" extra affordance (both meanings of `ctrl+l`) — deferred; refresh is already automatic via the events stream, links are already reachable via the Info tab.
- Files-tab per-row actions (`f`/`o`/`e`/`t`: reveal/open/edit/terminal) — deferred.
- All Hera-rail mutation keys (`s`/`S`, `m`/`M`, `J`, `w`, `n`, `c`/`C`, `ctrl+d`) — relocated to the separate Hera-rail-toggle change; meaningless without that view.

## Decisions

### D1: Extend the existing per-action shortcut pattern; do not build a centralized dispatch table

The mac app already attaches `.keyboardShortcut` directly to the button/menu command that performs each action (Cmd+N, Cmd+R, etc.). This change follows that same pattern for every new chrome-level shortcut (tab switch, help, destroy, fork, open-repo, open-PR, jump-to-needs-input) and every new context-menu item (status advance/revert, and the archive/pin shortcuts added to their existing buttons).

**Alternative considered:** a centralized dispatch table modeled on `internal/tui/keymap` (a single context-aware resolver). Rejected as YAGNI for this scope — SwiftUI's Commands/`.keyboardShortcut` already is that mechanism for a GUI app; introducing a second one would fight the framework and the existing code, not follow it.

### D2: Terminal-safe chords go through an explicit local key monitor, not SwiftUI Commands

`TerminalTab.swift` already has a local `NSEvent` monitor guarding mouse-click focus-stealing. This change extends that same monitor to intercept a small, explicit allowlist of Cmd-modified chords — Cmd+↑/↓ (task switch), Cmd+←/→ (pane focus), Shift+↑/↓/PageUp/PageDown/End (scroll) — before they reach SwiftTerm. Every other keystroke, including all Ctrl chords and plain arrows, falls through untouched to `POST /input`, exactly matching today's behavior.

**Resolved during implementation:** the mac app's detail pane has no split-pane view (unlike the TUI's agent view) — there is exactly one terminal surface per task. Cmd+←/→ therefore cycles the detail tab (Terminal→Diff→Files→Info, wrapping) instead, the closest structural analog to "move focus to an adjacent view without leaving the keyboard." Acceptance criteria and the delta spec's scenario wording below have been updated to reflect this.

This mirrors why the TUI itself gates these exact bindings behind Cmd instead of Ctrl in agent view: Cmd-modified chords are not something a CLI running inside the terminal would ever consume, so intercepting only that allowlist carries no risk of stealing a keystroke the inner agent needs. Whether SwiftTerm's own key handling would otherwise swallow these chords before they reach a SwiftUI Command is untested; routing them through the pre-existing local monitor sidesteps the question entirely by intercepting before SwiftTerm's `keyDown` runs.

**Risk:** the allowlist must stay in one place (a single Swift constant) so it can't drift from the chrome-level shortcuts declared elsewhere. See Risks below.

### D3: "Switch Claude session" gets a scoped picker, not the deferred global switcher

The TUI's `ctrl+r` in agent view (switch which Claude session is attached to this task, via `internal/claudesession`) has no mac-app equivalent today, and the user explicitly asked for a mechanism rather than a deferral. Scope: a toolbar button in the Terminal tab (near the existing tab bar) opens a picker sheet — same UI pattern already used for rename/new-task — listing that task's available Claude sessions. This is materially smaller than the deferred global task/role switcher (which spans all tasks/roles, not one task's session history), so it's in scope for this change.

**Correction discovered during implementation:** this requirement is the one exception to this change's "no daemon/REST API changes" framing (see Impact note below). `internal/claudesession` is a pure Go package the TUI calls in-process (`internal/tui/app.go`'s `openSessionPickerModal`/`switchSession`) — there is no REST route exposing it, and the mac app, as a REST-only client with zero Go coupling, cannot reach it any other way. This change therefore adds two minimal daemon endpoints (`GET /api/tasks/{id}/claude-sessions`, `POST /api/tasks/{id}/claude-session`) mirroring the TUI's list/switch flow — see the delta at `specs/rest-api/spec.md`. Per this repo's Frontend Parity rule, the web SPA's own equivalent affordance is an explicit, named non-goal (see Non-Goals below), not a silent gap — same precedent as the existing "hera mutations are TUI-only" carve-out.

### D4: Right-click context menus absorb the vi-style single-letter actions

`s`/`S` (status advance/revert) becomes a context menu on the task row. This establishes the pattern for any future single-letter TUI action that would otherwise collide with text-field typing: a context menu, not a bare-letter capture.

### D5: `H` (hera-managed filter) becomes a visible toggle, not a hidden hotkey

Persistent view-state (show/hide hera-managed tasks) belongs in a visible control in the sidebar's filter bar, not a hotkey a user has to remember. `/` (filter) becomes Cmd+F focusing that same filter bar's text field, following the standard macOS search idiom.

### D6: `ctrl+r` (global "prune worktrees") gets a menu item, not a hotkey

Low-frequency maintenance action → a new item in the existing toolbar overflow ("…") menu. No shortcut needed; the requirement is reachability, not speed.

## Risks / Trade-offs

- **[Risk]** The terminal-safe chord allowlist (D2) could drift out of sync with chrome-level shortcuts declared elsewhere, causing either a double-binding or a chord that silently does nothing in one context. → **Mitigation**: define the allowlist as a single Swift constant (e.g. `TerminalCoordinator.interceptedChords`) that both the local monitor and any documentation/tests reference; a unit test asserts no chrome-level `.keyboardShortcut` reuses one of these chords.
- **[Risk]** Whether SwiftTerm's own key handling already claims some of the target chords (e.g. Shift+End) is unverified. → **Mitigation**: the local monitor intercepts before SwiftTerm's responder chain runs regardless (D2), so this risk is structurally avoided rather than needing empirical verification — but a smoke test should still confirm the intercepted chords never appear in `POST /input` payloads during a terminal session.
- **[Risk]** Picking concrete chords for destroy/fork/open-repo/open-PR/jump-to-needs-input (D1) without checking for collisions against standard macOS reserved shortcuts (e.g. Cmd+G is "Find Next" in most apps) could produce a jarring UX. → **Mitigation**: tasks.md includes an explicit chord-selection step that checks each candidate against macOS HIG reserved shortcuts before wiring it up; where a natural TUI-mirroring chord collides, prefer a Shift- or Option-augmented variant over the reserved one.

## Open Questions

- Exact key chords for destroy/fork/open-repo/open-PR/jump-to-needs-input/help are decided during implementation (tasks.md), constrained only by "no collision with macOS reserved shortcuts" — not pinned here since they're a mechanical choice, not a design decision.

## Acceptance criteria

**Global chrome shortcuts (D1, D6):**

- It should switch the active tab (Terminal/Diff/Files/Info) via a Cmd+digit shortcut.
- It should open a shortcuts-help sheet via a shortcut.
- It should trigger the existing destroy/delete action via a shortcut, still showing its existing confirmation dialog.
- It should trigger the existing fork action via a shortcut.
- It should open the task's repo (Finder/editor) via a shortcut.
- It should open the task's PR via a shortcut, usable from both the app's global scope and while the agent/terminal view is focused.
- It should select the next task whose session needs input via a shortcut.
- It should reveal a "Prune stale worktrees" item in the toolbar overflow menu that triggers the existing prune action.

**Task rail (D4, D5):**

- It should show status-advance and status-revert as right-click context-menu items on a task row.
- It should trigger the existing archive action via a shortcut.
- It should trigger a new pin action (no pin affordance existed in the mac app before this change) via a shortcut.
- It should focus the sidebar's filter field via Cmd+F.
- It should show a persistent, visible toggle in the sidebar's filter bar that shows/hides hera-managed tasks.

**Agent/Terminal view (D2, D3):**

- It should move the task selection to the previous/next task via Cmd+↑/Cmd+↓ while the terminal has focus, without those keystrokes reaching `POST /input`.
- It should cycle the active detail tab via Cmd+←/Cmd+→ while the terminal has focus, without those keystrokes reaching `POST /input`.
- It should scroll the terminal's scrollback via Shift+↑/↓/PageUp/PageDown/End while the terminal has focus, without those keystrokes reaching `POST /input`.
- It should copy the terminal's visible output via a shortcut.
- It should open a "Switch Claude session" picker sheet via a toolbar button in the Terminal tab, listing that task's available sessions.

**Non-regression (D2):**

- It should forward every keystroke not claimed by the allowlist above to `POST /input` unchanged, identically to today's behavior, while the terminal has focus.
