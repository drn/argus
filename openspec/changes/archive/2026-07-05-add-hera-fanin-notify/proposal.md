## Why

`heragater.resolveBaseBranch` picks a fan-in node's base branch as the branch of whichever blocker has the highest `hera_bindings.id` — "most recently materialized," not a merge of all blockers. This is intentional and already documented (code comment + the `task-orchestration` spec's gater-materialization requirement).

The gap: `materializeNode` never tells the coordinator this happened. The existing `CoordinatorPinger` seam only fires today for a FAILED blocker (`holdAndPing`) or a recovery (`emitRecoveryNotice`) — never for the ordinary "2+ blockers, picked one, ignored the rest" case.

This is a verified live gap, not theoretical: inspecting 5 real materialized fan-in nodes across 3 active feature builds (via `~/.argus/data.sql` + their worktree git histories) found all 5 coordinators had independently recognized the gap and hand-written a mitigation into the node's prompt (ranging from an unconditional `git merge --no-edit <sibling>` to "wait for an explicit coordinator go+SHA" to a separately-maintained integration branch). One (`6a-prearchive` in `add-variant-naming`, 3 blockers) used a fragile "check for two specific artifacts, ask if missing" self-guard that only worked because its third blocker's tip commit happened to already be an ancestor of the chosen base — verified via `git merge-base --is-ancestor` — not because the mitigation was actually robust. A real third-blocker divergence would have been silently dropped with no error.

## What Changes

- **Materialization of a 2+-blocker planned node now pings the coordinator**, naming the chosen base branch (and which blocker it came from) and the other blocker(s)' branches that were NOT merged in — mirroring the existing `holdAndPing`/`emitRecoveryNotice` pattern (same `CoordinatorPinger` seam, sent from the materialized node's own role).
- **No behavior change to which branch is picked.** The highest-binding-ID fan-in resolution is unchanged; this is purely additive visibility.
- **Ping failure is one-shot and non-retried** (unlike `holdAndPing`'s retry-on-failure contract) — it's an informational notice tied to a single materialization event, not recurring state. Logged and dropped on failure; materialization itself always succeeds regardless.
- **Root nodes and single-blocker nodes are unaffected** — no ping fires unless 2+ blockers gated the materialize.
- Subcoord-node materialization (`materializeSubCoord`) is out of scope for this change.

## Capabilities

### Modified Capabilities

- `task-orchestration`: adds a notify scenario to the existing "Gater materializes a planned node when its blockers complete" requirement. The base-branch-resolution behavior itself (including the existing fan-in pick-one scenario) is unchanged.

## Impact

- **Code:** `internal/heragater/heragater.go` (`resolveBaseBranch` returns the winning blocker's role id; `materializeNode` pings on 2+ blockers).
- **Tests:** `internal/heragater/heragater_test.go` — fan-in ping fires once with correct content/addressing; 1-blocker and root-node materializations stay silent; a ping failure doesn't fail or retry materialization.
- **Docs:** `context/knowledge/gotchas/orchestration.md`.
- **No schema change, no new MCP tool, no TUI/API/web change.** Reuses the existing `CoordinatorPinger` seam.
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make / Go-build wiring added or changed. The quality gate stays `make pre-pr`.
