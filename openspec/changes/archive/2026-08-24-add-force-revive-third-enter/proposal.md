## Why

The closed-out banner's two-Enter sequence (arm, then dismiss to read-only replay) was shipped with an explicit "no separate third state" decision: further Enters just kept toggling the banner on and off forever. After dogfood testing this exact PR, Aaron found that unhelpful in practice — once he's deliberately pressed Enter three times in a row against a closed-out task, that's not an accidental double-tap, it's a clear signal he wants to reopen it. The only sanctioned way to do that today is the `hera_revive` MCP tool, which is far less discoverable than the keyboard shortcut the UI already trained him to reach for.

## What Changes

- A third Enter press against a closed-out task (immediately following the arm-then-dismiss sequence) now actually revives the task instead of re-arming the banner. **BREAKING** in the sense that it reverses the previously-shipped and spec'd "no separate third state" behavior (design.md Decision 4 of `add-hera-closeout-banner`) — no back-compat toggle, per this repo's Breaking Changes Policy.
- Reviving clears BOTH of the close-out guard's signals (`meta:hera.ready_to_close` and any terminal `done`/`failed` role status on a live binding) before starting the session, so the guard doesn't immediately re-trip on the freshly-revived session's own next natural exit. This is a deliberate operator override, not a call to the existing `ReviveHeraWorkerToInProgress` (which exists specifically to *refuse* this exact case).
- The revive is reached identically from the Hera tab and the plain Tasks tab (both already route through the same shared `App.reattachClosedOut`), so no new parity gap is introduced.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-coordination`: the closed-out Enter-key sequence gains a third step (force-revive) that supersedes the "no separate third state" requirement from `add-hera-closeout-banner`.

## Impact

- `internal/tui/terminal/terminalpane.go`: new `closedOutDismissedOnce` pane state, `ClosedOutReadyToRevive()` accessor, `ClearClosedOutState()`.
- `internal/tui/app.go`: `reattachClosedOut` gains the third branch; new `forceReviveClosedOut`.
- `internal/db/hera.go`: new `ClearHeraCloseout` (clears both close-out signals; distinct from `ReviveHeraWorkerToInProgress`, which refuses to act on a closed-out worker).
- No API/wire changes; local-mode only (matches the existing `heraTaskClosedOut`/`heraKickRestartClosedOut` local-mode-only scope).
