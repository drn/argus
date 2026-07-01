## Why

**BUG-C — the Hera rail working spinner does NOT animate for a live worker that is actively producing output while its task is in `in_review`, so a genuinely-working worker looks parked/in-review.**

`RoleView.IsActive()` — the honest "working" signal the rail spinner animates on — gated on `TaskStatus == in_progress`:

```go
return r.Live && r.TaskStatus == model.StatusInProgress.String() && !r.SessionIdle
```

Per the #707 invariant a hera worker deliberately sits in `in_review` (status + `meta:hera.ready_to_close`, stamped by `RollHeraWorkerToReview`) while its session lingers alive for the coordinator to close it out — the binding stays LIVE. If that still-alive worker keeps producing output (e.g. resumed after answering an AskUserQuestion), `IsActive()` returned false because the task was no longer `in_progress`, so the rail fell through to the static review glyph instead of the animated spinner. Reproduced live.

This is the DISPLAY sibling of BUG-A. BUG-A un-gated the `(?)` needs-input signal from task status (`buildRoleView`'s gate became `taskInProgress || rv.Live`). The working spinner was the last rail signal still gated on task status.

The task-status gate was the pre-content-aware blunt instrument for two stale-session concerns, both of which are now carried without it:

- **BUG-003** (a stale/stopped/dead/days-old session must NOT spin): a dead session is absent from the App's per-tick RUNNING set, so gating on session-running excludes it. CRUCIALLY, a hera binding does NOT end when its agent session exits — bindings end only on task-delete, reparent, detach, or the daemon-startup missing-task sweep — so `rv.Live` STAYS TRUE for a dead worker whose task row lingers. Liveness alone therefore CANNOT exclude a dead worker (an earlier draft of this fix gated on `Live && !SessionIdle` and regressed BUG-003: a dead session is neither running nor in the idle set, so it spun). The running signal is the correct gate.
- **BUG-036** (a parked fullscreen agent must NOT spin forever): the App's content-aware idle classification marks such a session idle, and `SessionIdle` unions raw-byte idle with that content-idle set, so `!SessionIdle` covers both idle modes. The idle set is a subset of the running set, so "running AND not idle" is exactly "running and producing".

## What Changes

- **`RoleView.IsActive()` (internal/tui/hera/model.go) → `r.Live && r.SessionRunning && !r.SessionIdle`.** The predicate trusts liveness + a RUNNING session + the content-aware idle set instead of the bound task's workflow status. A live, running, content-active worker spins regardless of task status — including the #707 `in_review` close-out window; a live-but-idle, live-but-dead, or unbound worker does not. The doc comment explains how BUG-003 (session-running, because bindings outlive session exit) and BUG-036 (`SessionIdle` unions raw + content idle) stay covered without the task-status gate.
- **New `RoleView.SessionRunning` signal, plumbed like `SessionIdle`.** Added a `sessionRunning map[string]bool` parameter to `BuildModel`/`buildRoleView` (keyed by live task ID), a `HeraPage.SetSessionRunning` setter + page field, and an App push of `runner.RunningAndIdle`'s running list (`a.heraPage.SetSessionRunning(runningIDs)` in `refreshTasksWithIDs`, alongside the existing `SetSessionIdle`).
- **`coordRoleStatusLabel` (internal/tui/hera/details.go)** is unchanged in logic — it already delegates the "is this stale-working really working?" decision to `role.IsActive()`. Its doc comment is refreshed: a live, running, content-active coordinator in `in_review` honestly reads "working"; a live-but-session-idle or live-but-dead one reads "live".

## Capabilities

- `idle-detection` — a LIVE hera role's rail working-spinner is gated on a RUNNING, non-idle session, not bound-task status; a running, content-active worker spins in the #707 `in_review` close-out window while a live-but-idle or live-but-dead (binding lingering) worker stays static.

## Out of scope

- Task status / reconcile / revive logic (`RollHeraWorkerToReview`, bounce reconcile, `ReviveHeraWorkerToInProgress`) is untouched — the fix assumes a live worker CAN legitimately be in `in_review` (#707) and makes the spinner correct in that state. The underlying status-stranding is a sibling change (BUG-B, PR #825).
- The `(?)` needs-input gate BUG-A already loosened (`buildRoleView` `allowNeedsInput`, the `RoleStatusIcon` precedence) is left intact — this change touches only the working-spinner display.
