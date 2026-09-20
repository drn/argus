## Why

The live dogfood daemon contains two active bindings for task `1789930752095619000`: it remains a worker in `argus-codex-archetypes` while also being the coordinator of `sketch-preview-server`. Hera currently infers a parent/child bridge from any such shared task ID, so the rail and subtree message scan expose Sketch activity to the Argus orchestration.

The `J` detach path has the same ambiguity in reverse. `DetachCoordinator` identifies parent links as every other binding of the coordinator task, then ends and deletes each corresponding role. It therefore cannot distinguish a real bridge from a separate, concurrently active membership in another orchestrator. A task ID is an identity for an agent session, not proof that its bindings represent hierarchy.

## What Changes

- Make the structural parent relation used by the Hera rail, `hera_tree_updates`, and `hera_get_messages` explicit and auditable instead of inferring it from any pair of bindings that share an Argus task ID.

- Have TUI `J` re-parent create the explicit parent relation, and have TUI `J` detach remove only that relation and its owned bridge role. It must never enumerate arbitrary bindings by coordinator task ID as a teardown target.

- Preserve legitimate nested coordinators and independent multi-orchestrator memberships, while preventing either from silently becoming the other.

- Add diagnostics that record the requested parent, child, bridge role, and detached relation for topology mutations so a future unexpected relationship can be identified without inspecting raw SQLite rows.

- Add regression coverage with concurrently active, unrelated orchestrators proving that a foreign coordinator/worker message cannot appear in another orchestrator's rail tree, `hera_tree_updates`, or `hera_inbox`.

## Capabilities

### New Capabilities

- None.

### Modified Capabilities

- `hera-coordination`: Explicitly scope hierarchy and subtree message access to validated parent relationships rather than incidental shared-task bindings.

## Impact

- `internal/db/hera*.go` and schema migration for the explicit hierarchy relation.

- `internal/mcp/hera.go` subtree delivery reads, plus TUI `J` re-parent/detach mutation handling and observability.

- `internal/hera/model` and `internal/tui/hera` rail construction.

- Existing Hera MCP and TUI regression suites, plus `context/knowledge/gotchas/hera-view.md`.

- Live remediation remains out of scope: binding `1801` / role `1827` must be corrected only with owner approval.
