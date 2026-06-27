## Why

**BUG-030 — a status-bar error/info notice never clears; it stays until the TUI is restarted.**

When the user does something invalid, the bottom-left status bar shows a red
notice (e.g. `! J: select a freelancer or a coordinator to adopt`) set via
`StatusBar.SetError`, or a dimmed informational notice set via `SetInfo`. The
notice text itself is correct, but it is sticky forever: the only code paths
that clear it are explicit `ClearError`/`ClearInfo` calls on unrelated actions,
so a notice that no follow-up action clears persists for the whole session. The
only reliable way to get the default `N active  M pending  K done` counts back
is to restart the TUI.

## What Changes

- **A notice set via `SetError` or `SetInfo` SHALL auto-expire after a fixed
  TTL (`StatusNoticeTTL` = 15s) and the status bar SHALL revert to its default
  task counts on its own — no keypress required.**
- **The notice still displays INSTANTLY when set** (red for errors, dimmed for
  info); only its disappearance is delayed.
- **Each new `SetError`/`SetInfo` call resets the expiry window** so a fresh
  notice always gets its full TTL and notices never clear early or stack.
- **The revert is realized lazily in `Draw`** (an expired notice paints as the
  default counts) and is guaranteed to repaint on a static screen by the app's
  existing unconditional ~1s `onTick` `QueueUpdateDraw`. The revert MUST NOT use
  `screen.Sync()` — it flows through tcell's normal per-cell diff
  (`gotchas/ui-threading.md`).

## Capabilities

### Modified Capabilities

- `tui-shell`: the status bar's transient error/info notices now auto-expire
  after ~15s and revert to the default task counts without user input, instead
  of persisting until the next explicit clear or a TUI restart.

## Impact

- **Modified code:**
  - `internal/tui/widget/statusbar.go` — `SetError`/`SetInfo` stamp an
    `expiresAt` from an injectable clock; `Draw`, `Error`, and `Info` drop a
    notice once its TTL has elapsed (`expireNotices`).
- **No new key, no new dependency, no schema change, no daemon RPC, no new
  goroutine or timer** (the expiry rides the existing 1s tick).
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make / Go-build
  wiring is added or changed. The quality gate stays `make pre-pr`.
