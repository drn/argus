## Why

Each argus task owns a git worktree and an `argus/<task>` branch, but once an agent pushes and a pull request is opened, nothing in argus surfaces that the branch is now waiting on a human reviewer — you have to leave the tool to check GitHub. Surfacing open-PR review state inline keeps the "what needs my attention" loop inside argus.

## What Changes

- Add a daemon-side poller that, every 60s, runs `gh pr view <branch> --json …` per non-archived task with a branch and caches the resulting PR review state in the `task_meta` sidecar (namespace `pr`).
- Add a `PRState` enum (`none / draft / awaiting-review / changes-requested / approved / merged-closed / unknown`).
- Render a **second indicator cell** beside the existing status glyph in the TUI task list — distinct glyph + color per review state (awaiting-review, changes-requested, approved). Drafts, merged/closed, and undetectable PRs render nothing. The cell coexists with (does not replace) the status glyph.
- Mirror the indicator as a badge in the web PWA task list, fed from the same cached `task_meta` value via a new `pr_state` field on the task DTO.
- Degrade silently when `gh` is absent, unauthenticated, or offline — never block the UI, never overwrite a known value with a transient failure.

## Capabilities

### New Capabilities

- `pr-status`: Detecting a task branch's open-PR review state and surfacing it as a non-blocking, cached indicator in the TUI task list and web PWA.

### Modified Capabilities

<!-- None. The task-list rendering and REST task DTO are not yet captured as base specs; this change introduces pr-status as a standalone capability rather than modifying plugin-views. -->

## Impact

- **New code:** `internal/model/prstate.go`, `internal/gitutil/pr.go` (gh shell-out behind a test seam), `internal/db` batch meta read helper.
- **Modified code:** `internal/daemon/daemon.go` (poller goroutine), `internal/tui/taskview/tasklist.go` + `internal/tui/theme/theme.go` + `internal/tui/app.go` (render + wiring), `internal/api/handlers.go` (`pr_state` DTO field), `internal/api/static/index.html` + `sw.js` (badge + service-worker version bump).
- **Dependencies:** none added — `gh` is already shelled out for the "open PR in browser" action.
- **Data:** new `task_meta` rows under namespace `pr`; auto-cleaned on task delete/archive by existing `DeleteMetaForTask`.
