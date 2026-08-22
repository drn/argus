## Why

**Focusing a Hera rail task's pane makes its needs-input icon flicker forever: moon → spinner → `(?)` → then bounce between `(?)` and moon roughly every second, for as long as the pane stays focused — even with static, unresized, non-restarted content.** Unfocusing settles it back to moon and it stays there; refocusing repeats the whole cycle.

Root cause: the live PTY emulator's terminal-capability-query auto-answer path (`NewLiveEmulator` / `forwardEmulatorResponse`, added in the OSC 10/11 color-query feature) delivers its generated responses via `Session.WriteInput`, which stamps `lastUserInput` exactly like a real keystroke. `idle-detection`'s clear-on-input logic (`NeedsInputClear`) reads that same timestamp to decide "the user answered" and clears the `(?)` flag. Because only the currently-focused pane owns a live (non-discard) emulator, this write happens repeatedly while focused (the agent CLI's periodic redraw re-triggers a capability-query response on roughly the same ~1s cadence as the app's detection ticker) and never while unfocused — matching the observed focus-gated bounce exactly.

`idle-detection`'s own spec already requires clear-on-input to "count only genuine user-delivered input, never system-injected delivery" — `Session.WriteInputSystem` exists for exactly this (used today by reliable-notify delivery). The terminal-rendering capability's query-response forwarder was never routed through it, so it silently violates that contract.

## What Changes

- **The live emulator's terminal-capability-query response forwarder now delivers via `WriteInputSystem`, not `WriteInput`.** The response still reaches the agent process's stdin identically (same bytes, same PTY write); only the input-classification bookkeeping changes, so it can no longer be mistaken for a genuine user keystroke by the needs-input clear-on-input logic.
- No change to which queries are answered, what the emulator reports, the forwarding goroutine/queue mechanics, or the stale-emulator drop guard — all of that is unchanged from the OSC 10/11 feature.

## Capabilities

### Modified Capabilities

- `terminal-rendering`: the "Live emulator answers terminal capability queries" requirement now specifies that forwarded responses are delivered as system-classified input, not user input.

## Impact

- **Modified code:**
  - `internal/app/agentview/terminal.go` — `TerminalAdapter` interface gains `WriteInputSystem`.
  - `internal/tui/terminal/terminalpane.go` — `forwardEmulatorResponse` calls `WriteInputSystem` instead of `WriteInput`.
  - Test fakes implementing `TerminalAdapter` (`internal/tui/terminal/terminalpane_test.go`, `internal/tui/terminal/forward_wheel_test.go`, `internal/tui/bug031_test.go`, `internal/tui/hera/panes_test.go`) gain a `WriteInputSystem` method so they keep satisfying the widened interface.
- **No schema change, no new key, no daemon RPC change.** Real session types (`agent.Session`, `apiclient.Session`, `daemon/client.RemoteSession`) already implement `WriteInputSystem` for the pre-existing reliable-notify path — nothing new to wire there.
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make / Go-build wiring is added or changed. The quality gate stays `make pre-pr`.
