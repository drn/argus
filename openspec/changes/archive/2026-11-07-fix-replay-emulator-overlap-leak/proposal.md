## Why

A live TUI held 16-17 replay emulators against a design bound of 6 (3 `TerminalPane` instances x 2 slots), growing with use; each is ~4 MB of fixed x/vt parser buffer plus its scrollback and a drain goroutine blocked forever. Heap and goroutine dumps (23:18, 23:49, 00:19) and a failing test pin the cause.

`ResetVT` and `invalidateReplayCache` cleared `replayBuilding` while an `asyncReplayRebuild` goroutine was still running, so the next Draw started a second, overlapping rebuild. The overlapping rebuild found the spare slot empty and allocated a new emulator; at publish, `retireReplayEmulator` found the spare slot occupied and silently dropped the outgoing emulator with no Close (Close races the drain goroutine's Read), leaking it for the process lifetime. Hera's `bindPane` calls `ResetVT` on every rail navigation, so this fires constantly (10 overlapping rebuilds allocated 11 emulators, 9 leaked).

The same overlap also let a slow rebuild for the PREVIOUS binding finish after the fresh one and overwrite it with stale content (a garbling source).

## What Changes

- A per-pane `replayGen` bumped by `ResetVT`, `invalidateReplayCache` and a task change. A rebuild captures it at kick; on completion a mismatch means stale: nothing is published (including the alt-screen flag), the emulator is parked, and the completion redraw kicks a fresh rebuild.
- `replayBuilding` is cleared only by the rebuild goroutine itself (single deferred release covering normal, empty, stale and panic exits), so at most one rebuild per pane is in flight.
- A second spare slot so a stale result can be parked next to the retired published emulator; never more than two emulators per pane.
- Drop tripwire counter and uxlog line (never Close), plus emulator create/reuse/stale counts in uxlog.
- No supervisor ProtocolVersion bump, no wire change, no new `screen.Sync()`.

## Capabilities

### Modified Capabilities

- `terminal-rendering`: replay rebuild concurrency, staleness and emulator bound.

## Non-goals

Releasing the spare slots of idle or unbound panes (the fixed ~24 MB floor of 2 emulators x 3 panes) is a follow-up. The replay scrollback cap is unchanged.
