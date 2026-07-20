## 1. Cascade the archived bridge's subtree

- [x] 1.1 Add `Model.SubtreeArchivedBridges(orchID) []int64` (`internal/tui/hera/eol.go`)
  returning the child orchestrator IDs behind every ARCHIVED bridging worker role
  in the subtree rooted at orchID. `SubtreeArchivedWorkers` is unchanged — it still
  includes the bridging role itself (ending its own binding stays correct).
- [x] 1.2 `heraClearArchive` (`internal/tui/heraactions.go`) gathers
  `SubtreeArchivedBridges(scopeID)`, merges every child's full `BridgeSubtree` into
  one combined cascade set, and — on confirm — runs `heraDoCascadeNuke` against it
  in addition to the existing flat `heraDoClearArchive(workers)` call. Multi-binding
  safety is preserved: both nuke paths re-validate live-binding state against the
  DB at call time, so running them in either order converges to the same correct
  end state (a shared task's worktree is reclaimed exactly once).
- [x] 1.3 Extract `countCascadeSubtree` (agents/worktrees/preserved tally) out of
  `heraCascadeNukeFrom` into a shared helper, reused by `heraClearArchive`'s new
  confirm message.
- [x] 1.4 (post-review fix) `heraTaskBoundOutside`/`countCascadeSubtree` gained
  an `excludeRoleIDs` param: `heraClearArchive`'s cascade sub-message tally now
  excludes `workers`' own role IDs from the "bound outside" check, since
  `heraDoClearArchive(workers)` always ends those bindings FIRST, in the same
  confirm action, before the cascade half runs. Without this the preview could
  call a shared bridge task "preserved" when it was actually about to be
  reclaimed by the cascade right after — a real, deterministic message-accuracy
  bug caught in review, not just a cosmetic nit (it contradicted this
  codebase's own stated invariant that the cascade confirm never undercounts).
  `heraCascadeNukeFrom` (Ctrl+D) passes `nil` — unaffected, since nothing else
  is being nuked alongside its cascade.

## 2. Tests

- [x] 2.1 `TestModel_SubtreeArchivedBridges` — pure model test: an archived
  bridging role's child orch ID surfaces; a plain archived leaf worker and a
  LIVE bridging role (Ctrl+D's job, not `C`'s) do not.
- [x] 2.2 `TestHeraActions_ClearArchiveCascadesArchivedBridge` — end-to-end:
  archiving a sub-coordinator's bridging role, then pressing `C` on the parent,
  fully NUKES the child orchestrator (and its own worker) — it does not survive
  as an orphan and does not reappear as a top-level root on the next rail
  rebuild. Also asserts the confirm-message preview's worktree/preserved counts
  are accurate (2 reclaimed, 0 preserved — see 1.4) and that the shared bridge
  task converges to reclaimed+archived exactly once.
- [x] 2.3 Existing `TestHeraActions_ClearArchiveBranches` and
  `TestHeraActions_ClearArchiveScopesToSubCoordinator` still pass unchanged
  (no regression to the flat and BUG-003 scoping paths).
- [x] 2.4 (post-review fix) `TestHeraActions_ClearArchiveCascadesMultiLevelArchivedBridge`
  — a 3-level chain (P →archived bridge→ C →LIVE, never-archived bridge→ D)
  confirms `BridgeSubtree`'s walk correctly reaches D through an intermediate
  hop whose OWN bridge to D was never itself archived — the single archived
  row on P is enough to cascade the whole chain.

## 3. Docs

- [x] 3.1 Update the `hera-view` base spec's Tier-2/`C` requirement + scenario
  to state the cascade explicitly.
