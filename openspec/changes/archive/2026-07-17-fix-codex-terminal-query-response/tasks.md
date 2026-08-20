# Tasks

- [x] 1.1 Failing test in `internal/tui/terminal`: a new `NewLiveEmulator(cols, rows, forward)` constructor feeds it an `OSC 11 ?` query and asserts `forward` is called with a response reporting the assumed background color (and likewise for `OSC 10 ?` / foreground).
- [x] 1.2 Implement `NewLiveEmulator` (drains the emulator's response pipe like `NewDrainedEmulator`, but calls `forward` with the drained bytes instead of discarding; sets `SetBackgroundColor`/`SetForegroundColor` to the assumed defaults).
- [x] 2.1 Wire `newTrackedEmulatorWithCallback` (the pane's live-emulator constructor) to use `NewLiveEmulator` with a new `forwardEmulatorResponse` method that looks up `tp.session` under `tp.mu` at call time and calls `WriteInput` if non-nil, no-oping otherwise.
- [x] 2.2 Test: `forwardEmulatorResponse` with no session attached does not panic or error.
- [x] 2.3 Test: `forwardEmulatorResponse` with a `mockAdapter` session forwards bytes via `WriteInput` (extend `mockAdapter` to record written bytes).
- [x] 3.1 Confirm `newTrackedReplayEmulatorWithCallback` (replay/preview path) is untouched and still uses `newDrainedReplayEmulator` (discard-only) — add/keep a test asserting a replay emulator's query response is never forwarded.
- [x] 4.1 Add a gotcha note to `context/knowledge/gotchas/pty-terminal.md` next to the existing `NewDrainedEmulator` bullet, documenting the live-vs-replay split and why the live path now answers queries.
- [x] 5.1 Run the full `make pre-pr` gate and fix any gaps.
- [x] 6.1 Archive: fold the delta into `openspec/specs/terminal-rendering/spec.md`, move the change folder to `openspec/changes/archive/<date>-fix-codex-terminal-query-response/`, in the same branch before merge.
- [x] 6.2 Re-run `make pre-pr` after archiving to confirm no drift.
