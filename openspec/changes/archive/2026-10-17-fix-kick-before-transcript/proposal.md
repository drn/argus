## Why

A size-drift kick can terminate Claude before its first transcript exists. Every subsequent resume then targets a conversation that was never saved and exits immediately.

## What changes

- Defer kill-and-resume kicks until the backend's recorded session is resumable, preserving existing retry machinery.
- Recover dead sessions with missing transcripts by generating a new session ID and launching with the original task prompt.
- Declare transcript availability in the backend family registry and share launch recovery across all clients.

## Capabilities

### New capabilities

None.

### Modified capabilities

- `agent-execution`: Guard kicks and recover missing-transcript relaunches.

## Impact

Agent family registry, runner, daemon RPC response, TUI kick handling, and REST resize. REST resume/restart responses gain an additive `session_id` field for remote TUI persistence. REST-driven recovery benefits web and macOS as well as TUI; these clients safely ignore the additive field. No new endpoint or frontend UI changes.
