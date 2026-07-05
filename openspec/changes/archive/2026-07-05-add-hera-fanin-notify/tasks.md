## 1. Delta spec

- [x] 1.1 Add a scenario to the existing "Gater materializes a planned node when
      its blockers complete" requirement in
      `openspec/changes/add-hera-fanin-notify/specs/task-orchestration/spec.md`:
      a 2+-blocker materialization notifies the coordinator naming the chosen
      branch and the un-merged sibling blocker branches. Keep the existing
      scenarios verbatim (this is additive, not a rewrite).

## 2. Code

- [x] 2.1 `resolveBaseBranch` (`internal/heragater/heragater.go`) also returns
      the winning blocker's role id (0 when no blocker branch resolved). Its
      only caller (`materializeNode`) is updated; no other call site exists.
- [x] 2.2 `materializeNode` fetches `blockerIDs` and, after a successful
      worker-kind materialize with `len(blockerIDs) > 1`, pings the coordinator
      (looked up via `ListHeraRolesByKind(..., HeraKindCoordinator)`, same as
      `holdAndPing`) naming the chosen branch + winning blocker and the
      un-merged sibling blockers' names/branches.
- [x] 2.3 Ping failure is logged and dropped — no retry, materialization is
      already committed by the time the ping fires.
- [x] 2.4 No ping for root nodes or single-blocker nodes. `materializeSubCoord`
      is untouched (the subcoord branch returns before the ping call).

## 3. Tests

- [x] 3.1 2-blocker materialization pings exactly once, addressed to the
      coordinator, body/tldr name the winning branch and the other blocker.
- [x] 3.2 1-blocker materialization: no ping (regression guard).
- [x] 3.3 Root node (no blockers): no ping (regression guard).
- [x] 3.4 Ping failure on a multi-blocker materialize: materialization still
      succeeds (node appears in `f.materialized()`), no panic, no retry.

## 4. Docs

- [x] 4.1 Add a bullet to `context/knowledge/gotchas/orchestration.md` (hera
      plan-DAG substrate section): fan-in pick-one is unchanged, materialization
      now pings the coordinator; cite the verified live-data motivation.

## 5. Archive + gate

- [x] 5.1 `openspec archive add-hera-fanin-notify` (merge the delta into
      `openspec/specs/task-orchestration/spec.md`, move the change folder to
      `openspec/changes/archive/<date>-add-hera-fanin-notify/`) before opening
      the PR.
- [x] 5.2 `make pre-pr` clean.
