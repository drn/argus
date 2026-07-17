# Design

## Context

`task.SessionID` is read verbatim at resume time and passed to the backend's resume flag (`BuildCmd` → `--resume` for Claude). It is refreshed to the newest worktree transcript UUID ONLY by the PTY-exit hook `captureSessionIDPostExit`, gated on `NeedsSessionRecapture` (Claude re-mints its UUID on every exit / `/clear`, so the stored ID goes stale).

Hera workers never reach that exit hook:

1. They idle rather than exit — `RollHeraWorkerToReview` early-returns a live worker to `in_review` without a process exit.
2. Supervisor death surfaces as `StreamLost`, which short-circuits `handleSessionExit` BEFORE the recapture.

The supervisor-restart reconcile (`reattachSupervised`) re-attaches only sessions the supervisor still reports live and never re-derives a SessionID. Net: `task.SessionID` stays pinned to the create-time `--session-id` UUID, so the next resume targets the stale FIRST session. There is no resume-time recapture anywhere.

## Decision

Add a single shared helper `agent.RefreshResumeSessionID(database *db.DB, task *model.Task)` — the resume-time analog of `captureSessionIDPostExit` — and invoke it at each DB-persisting resume chokepoint.

The helper:

- Resolves the backend and acts ONLY for Claude backends (`IsClaudeBackend`); this is the same "Claude-only" gate the exit hook expresses via `NeedsSessionRecapture`. codex/pi/opencode resume with a stable captured ID (`--session` / capture-style) and are left byte-identical.
- No-ops when `task` is nil, the worktree is empty, or `task.SessionID` is empty (first start — nothing to refresh; never fabricate an ID).
- Re-derives the newest transcript via `CaptureClaudeSessionID(task.Worktree)`. On error (no transcript yet) or unchanged ID, it keeps the existing SessionID and never blanks it.
- On a genuine change, mutates `task.SessionID` in place (so the caller's immediate resume uses it) AND persists via a read-modify-write on the DB row (mirroring `captureSessionIDPostExit`), so the row is correct even for callers that don't re-issue an Update.
- Logs the refresh and the skip/no-op cases via `uxlog`.

### Why `internal/agent`

All three seam packages (`daemon`, `tui`, `api`) already import `internal/agent`, and `internal/agent` already imports `internal/db` (no cycle — `db` does not import `agent`). The Claude capture primitives already live there. One helper, called at each seam, beats duplicating the read-modify-write logic three times.

### Seams

- `internal/daemon/bounce.go` `reattachSupervised` — MUST-FIX (reported symptom). After the reconcile flips true orphans to `in_review`, each orphan's Claude session ID is refreshed to the newest transcript so its later resume (coordinator revive, TUI, or REST) targets the most recent session.
- `internal/tui/app.go` `startSession` — refresh before the resume `Start`, guarded on a local `*db.DB` (remote mode has no local worktree/transcripts).
- `internal/api/handlers.go` `handleResumeTask` / `handleRestartTask` — refresh before `StartOrReattach`.

The TUI hera "reattach dead worker" path routes through `startSession`, so it is covered transitively.

## Non-Goals

- **No REST-surface change.** No new/changed endpoint, event, field, or status semantics. The fix lives at the shared daemon/agent seam, so the TUI, web SPA/PWA, and macOS app all inherit corrected resume behavior without per-client work. Frontend-parity trigger does not fire.
- **codex/pi/opencode resume semantics unchanged.** They mint IDs post-exit and keep them stable across resumes; the helper is Claude-only.
- **Live in-place rerender (`KickRerender` / `reviveHeraWorker`) is out of scope.** That primitive stops-and-resumes a currently-live session whose newest transcript is already current; the reported symptom is resume of a session the supervisor already tore down. Left as a named follow-up if a live-suspended worker is later observed carrying a stale ID.

## Risks

- Reading the filesystem (`~/.claude/projects`) at resume time adds a stat/readdir on the resume path. Bounded by the existing `claudesession.List` cost, already paid by the exit hook; acceptable on a human-driven resume.
- Concurrent capture with a still-writing agent is benign: newest-ModTime is the active conversation, exactly what a resume should target (same reasoning as `captureSessionIDPostExit`).
