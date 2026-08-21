## Why

**Pressing Enter on a closed-out hera worker/freelance task's dead-session row is a dead end.** `heraReattach`'s close-out guard (`internal/tui/heraactions.go`, add-enter-closeout-guard) correctly refuses to restart the session, but the only feedback is `statusbar.SetError(...)` — a 15-second-TTL footer notice that is easy to miss and leaves the pane showing whatever it already happened to show (the passively-rendered replay of the last session log, or the generic "Session not running - press Enter to start" placeholder if no replay content was ever loaded). The operator gets no in-pane explanation of *why* nothing happened, and no way to deliberately ask to see the task's last output.

## What Changes

- **`TerminalPane` gains a persistent, in-pane "closed out" banner overlay** (`ShowClosedOutBanner` / `DismissClosedOutBanner` / `ClosedOutBannerShown`), drawn in `Draw()` ahead of the pane's existing dead-session branches (replay content or the placeholder) whenever armed. Unlike the footer notice, it stays on screen for as long as the pane stays bound to this task.
- **`heraReattach`'s close-out branch now toggles that banner instead of only setting a footer error.** The first Enter on a closed-out worker/freelance row arms the banner (in addition to a footer note, preserving the existing message). A second, immediately-following Enter dismisses it — no new rendering path is added for this: dismissing just lets `TerminalPane.Draw()` fall through to its ALREADY-EXISTING dead-session rendering, which auto-replays the last session log when one was recorded (no PTY/process is ever spawned). Further Enters keep toggling between the reminder and the read-only view.
- **The banner is scoped to the current pane binding, not persisted.** It is reset by `TerminalPane.ResetVT()` — already called on every hera pane rebind (`panes.go`'s `bindPane`) — so leaving the task and coming back shows the banner again on the first Enter, requiring a fresh second press to view read-only again. No new teardown is needed beyond this: the override never allocates a new emulator or goroutine (it reuses the existing replay path), so there is nothing extra to release on unbind.
- No new keybinding: this is a second consecutive `Enter` press, not a distinct key.

## Capabilities

### Modified Capabilities

- `hera-coordination`: "Enter refuses to restart a dead-session worker awaiting close-out" now surfaces a persistent in-pane banner (not just a footer notice) and lets a second, immediately-following `Enter` dismiss it in favor of a read-only view of the task's last recorded output, reusing the pane's existing dead-session replay rendering. The refusal-to-restart behavior itself (no session start, no status write) is unchanged.

## Non-Goals

- **Web/macOS parity.** Hera mutation surfaces (including `Enter`-to-reattach) are TUI-only by established design — `GET /api/hera` is read-only over REST, and the web/macOS Hera tabs render read-only rosters (see CLAUDE.md "Frontend Parity"). This change is a TUI-only UX improvement on top of that existing, already-named gap; it does not create a new one.
- **The size-drift kick's own closeout refusal** (`heraKickRestartClosedOut`, `handleSessionExitUI`'s `pendingRerenderRestart` branch) is unaffected. That path has no keypress and no pane to arm a banner in — it already silently skips the restart, which is the correct behavior for a keypress-less trigger.
- **No new replay/emulator mechanism.** The read-only override reuses `TerminalPane`'s existing dead-session rendering verbatim (no `PreviewVT`, no second emulator instance).

## Impact

- **Modified code:** `internal/tui/terminal/terminalpane.go` (banner field + methods + `Draw()` gating + `ResetVT` reset), `internal/tui/heraactions.go` (`heraReattach`'s close-out branch).
- **No schema change, no daemon RPC surface change, no new dependency.**
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make / Go-build wiring is added or changed. The quality gate stays `make pre-pr`.
