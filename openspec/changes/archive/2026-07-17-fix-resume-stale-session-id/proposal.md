## Why

Coordinators reboot a worker's agent session in-place in the same worktree to preserve context, so a task accrues multiple Claude transcript UUIDs over its life. `task.SessionID` is only ever refreshed to the newest transcript by the PTY-exit hook (`captureSessionIDPostExit`), but hera workers never reach it: they idle instead of exiting (`RollHeraWorkerToReview`), and supervisor death surfaces as `StreamLost`, which short-circuits `handleSessionExit` before the recapture. As a result `task.SessionID` stays pinned to the first `--session-id` UUID minted at create, and on supervisor restart the worker resumes from the FIRST/stale session instead of the MOST RECENT one — losing all context accrued since.

## What Changes

- Add a resume-time analog of the exit-hook recapture: a single shared helper that, for a Claude task about to be resumed, re-derives the newest transcript UUID via `CaptureClaudeSessionID(task.Worktree)`, persists it to `task.SessionID`, and refreshes the in-memory task so the immediate resume targets it.
- Invoke the helper at the DB-persisting resume chokepoints: the supervisor-restart reconcile (`reattachSupervised`, the reported symptom), the TUI `startSession` resume path, and the REST `handleResumeTask` / `handleRestartTask` handlers.
- Claude-only, gated by backend family. codex/pi/opencode resume with a stable captured ID and MUST be byte-identical to today.
- Idempotent and safe: when the worktree has zero transcripts (or the task has no prior session ID) it falls back to the existing SessionID and never blanks it.

## Capabilities

### New Capabilities

<!-- none -->

### Modified Capabilities

- `agent-execution`: adds a resume-time session-ID refresh behavior (the analog of post-exit capture), scoped to Claude backends, idempotent, non-blanking.
- `daemon-lifecycle`: the supervisor-restart reconcile refreshes each orphaned Claude task's session ID to the newest transcript so its later resume targets the most recent session.

## Impact

- `internal/agent`: new `RefreshResumeSessionID` helper (new file `resume.go`), reusing the existing `CaptureClaudeSessionID` / `NeedsSessionRecapture` / `IsClaudeBackend` primitives.
- `internal/daemon/bounce.go`: `reattachSupervised` calls the helper for each reconciled orphan.
- `internal/tui/app.go`: `startSession` calls the helper before a resume Start (local `*db.DB` only).
- `internal/api/handlers.go`: `handleResumeTask` / `handleRestartTask` call the helper before `StartOrReattach`.
- No REST-surface change (no new/changed endpoint, event, or field), so TUI + web + macOS resume callers are all covered uniformly at the shared daemon/agent seam. See Non-Goals in `design.md`.
- No change to codex/pi/opencode resume semantics.
