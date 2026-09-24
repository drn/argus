## Why

`add-branch-namespacing` (archived 2026-09-22) namespaced every hera worker branch under its spawning orchestrator's name (`argus/<orchestrator>/<role>`) but deliberately kept `SpawnHeraCoordinator`'s own branch flat (`argus/<orchestrator>`) — the design's Non-Goals explicitly excluded it, treating the resulting git ref-namespace collision as a rare, self-resolving edge case. In practice it is not rare: because a root coordinator's task name defaults to its orchestrator's own name, its branch is *always* `argus/<orchestrator>`, and *any* worker later spawned under that same orchestrator needs `argus/<orchestrator>/<role>` — a ref that cannot coexist with the leaf ref already occupying `argus/<orchestrator>`. This is a 100%-reproducible failure on the very first worker spawned under any hera coordinator, not a coincidental name clash. It was confirmed live: `hera_spawn_worker` under orchestrator "argus-folders" fails with `fatal: cannot lock ref 'refs/heads/argus/argus-folders/<role>': 'refs/heads/argus/argus-folders' exists`, which also makes `hera_spawn_worker` unusable for delegating this very fix.

## What Changes

- `SpawnHeraCoordinator` now namespaces its own branch under its orchestrator's name, mirroring `SpawnHeraWorker`/`MaterializeHeraWorker` — the orchestrator name becomes a pure namespace directory, never a leaf ref by itself.
- The coordinator's default task name (used when `TaskName` is left blank, which is every current caller) is derived from its coordinator role name (`coordName`, e.g. `"coord"`) joined with the orchestrator name, instead of being the bare orchestrator name — so the branch leaf is always distinct from the namespace segment above it, never colliding with a worker's own `argus/<orchestrator>/<role>` branch.
- Corrects the `worktree-management` base spec's "Branch namespace for hera-managed worker spawns" requirement, which explicitly (and incorrectly) carved out root hera-coordinator spawns as staying flat.

## Capabilities

### Modified Capabilities

- `worktree-management`: the "Branch namespace for hera-managed worker spawns" requirement's exclusion of root hera-coordinator spawns from namespacing is reversed — coordinator branches are now namespaced under their own orchestrator too, using a leaf distinct from the namespace itself.

## Impact

- `internal/agent/hera_spawn.go` (`SpawnHeraCoordinator`): threads `BranchNamespace` into its `CreateAndStart` call and changes the default task-name derivation.
- `internal/tui/heraactions.go` (`heraDoNewCoordinator`): stale comment describing the old "TaskName defaults to the orchestrator name" behavior needs updating.
- Tests: `internal/agent/hera_spawn_test.go` (coordinator branch assertions), plus a regression test proving the old flat-coordinator + first-worker-spawn sequence no longer collides.
- `context/knowledge/gotchas/worktree.md`: one-line invariant documenting that an orchestrator name must never double as both a namespace prefix and a leaf ref.
- No effect on `MaterializeHeraSubCoordinator` (already namespaces under the PARENT orchestrator using a freeform planned-role leaf name, not a name that defaults to the orchestrator itself — not exposed to this collision).
