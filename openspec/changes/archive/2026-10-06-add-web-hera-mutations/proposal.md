# Web Hera mutations (removal + rail-key parity)

## Why

The webapp's Projects tab is read-only (`GET /api/hera` only). Stale, unbound
orchestrators (no live sessions) pile up and can only be removed from the TUI
(`ctrl+d`). The webapp must reach parity with the TUI rail's end-of-life and
organisation keys.

## What Changes

New authenticated REST mutations under `/api/hera`, each a thin adapter over the
SAME store/primitive the TUI uses (no re-implemented business logic):

- `POST /api/hera/orchestrators/{id}/nuke` — cascade-nuke the orchestrator + every
  orchestrator nested beneath it (BridgeSubtree). Master-token gated.
- `POST /api/hera/roles/{id}/nuke` — nuke one role. Master-token gated.
- `POST /api/hera/{orchestrators|roles}/{id}/archive` and `/unarchive` — hide/unhide.
  Hiding a worker also stops its live session (TUI `heraHide` parity).
- `POST .../pin` / `.../unpin`, `POST .../rename {name}`.
- `POST /api/hera/roles/{id}/status {status}` — set role status (worker→done rolls
  the task to in_review, TUI `StepStatus` parity).
- `POST /api/hera/orchestrators/{id}/kanban {status}` — top-level orchestrators only.
- `GET /api/hera/orchestrators/{id}/nuke-preview` — counts (orchestrators, agents,
  worktrees reclaimed, tasks preserved) for the confirm dialog.

Nuke semantics (daemon-side, extracted into `internal/hera` so TUI and REST share
it where practical): stop live sessions (backgrounded), end each role's binding
as `user_deleted`, stamp roles + orchestrators NUKED (never hard-deleted), archive
sole-bound tasks (in_review → complete), preserve tasks bound outside the subtree.
Worktree/branch reclaim and task-row prune are delegated to the existing
`hera.ReconcileHeraReclaims` sweep, triggered immediately after the nuke.

Webapp: per-card/per-role action menu on the Projects tab (confirm modal for nuke
showing preview counts); SW_VERSION bump.

## Non-Goals (named follow-ups)

- **macOS app parity**: macOS Hera tab stays read-only; follow-up task
  "macOS Hera mutations".
- Spawn worker / new coordinator / move / join / detach, inbox viewer, and the
  merge-safety Cleanup popup remain TUI-only (follow-up "web Hera spawn + cleanup").
- The TUI's per-role merge-safety review popup and the `in_progress`-at-nuke completion marker are not reproduced: a web nuke deletes the worktree and branches without a merge-safety gate (the confirm shows counts only), and a task nuked while `in_progress` lands at in_review (follow-up "web Hera nuke safety review").
- The TUI's Tier-D stacked-branch review modal is not reproduced; the reclaim sweep's
  existing safe-only stacked-branch deletion applies.

## Impact

`internal/api` (new handlers, routes), `internal/hera` (shared nuke primitive),
`internal/api/static/index.html` + `sw.js`, specs `rest-api`, `mobile-pwa`,
`hera-view`; README REST table; gotchas/web-remote.md.
