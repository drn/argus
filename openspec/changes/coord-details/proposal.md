## Why

When a coordinator role is selected, native argus's Hera view renders a Details pane that shows only the orchestrator **Name**, the coordinator **Status**, and the **Agents** roster. The plugin Hera view (`internal/view/coord_details.go`, spec §1113) showed a richer metadata block — when the coordinator's group was created, when it was last active, every repo in its scope, the coordinator's argus task name and worktree, plus a reserved Summary placeholder.

The parity audit flags this as a degradation (`docs/NATIVE-VS-PLUGIN-PARITY-MATRIX.md` §6, `docs/PARITY-OUTCOME.md` item #5: "leaner details pane"). An operator inspecting a coordinator loses the at-a-glance context the plugin gave them. This change restores that metadata block — additively, alongside the roster and the embedded Orchestration Tree that native already renders.

## What Changes

- Enrich the read-only rail projection so the coordinator Details pane can render the missing fields without any Draw-time I/O (the `DetailsView` stays a pure projection renderer):
  - `OrchView.CreatedAt` — the orchestrator's creation time.
  - `RoleView.CreatedAt`, `RoleView.ArgusProject`, `RoleView.WorktreePath`, `RoleView.BindingStartedAt`, `RoleView.StatusUpdatedAt`, `RoleView.TaskName` — the per-role inputs the metadata derivation needs.
- Restore the coordinator Details metadata block, between the coordinator status line and the Agents roster:
  - **Created** — orchestrator creation time.
  - **Last activity** — the max over orchestrator creation, each role's creation, each role's live-binding start, and each role's status-update time.
  - **Agent** — the coordinator's argus task name (omitted when unbound).
  - **Worktree** — the coordinator's live-binding worktree path (omitted when absent), shortened to the trailing `project/task` components when it overflows the pane.
  - **Repos in scope** — the distinct argus projects across the orchestrator's roster roles, sorted (a `(none)` line when empty).
- Append a reserved **Summary:** field rendering the `(auto-generated overview coming soon)` placeholder, mirroring the plugin.
- Keep the existing roster and the embedded **Orchestration Tree** DAG exactly as they are — this is purely additive to the Details pane.
- Keep `ContentHeight()` exactly in lockstep with the new `Draw` row budget so the stacked Details region sizes the roster correctly.

## Capabilities

### Modified Capabilities

- `hera-view`: The coordinator Details region gains a rich metadata block (Created, Last activity, Agent, Worktree, Repos in scope, Summary placeholder) in addition to the existing roster and Orchestration Tree.

## Impact

- **Modified code:** `internal/tui/hera/model.go` (additive projection fields + their population in `BuildModel`/`buildRoleView`), `internal/tui/hera/details.go` (metadata derivation + render + `ContentHeight`).
- **Tests:** `internal/tui/hera/details_test.go` (Last-activity max, Repos-in-scope distinct/sorted, render assertions, updated `ContentHeight` contract), `internal/tui/hera/model_test.go` (projection-field population).
- **Dependencies:** none added.
- **Data:** none — every field is read from data `BuildModel` already loads (orchestrators, roles, live bindings, role status, task snapshot).
- **Out of scope:** the rail rows, the coordinator/agent panes, and the Orchestration Tree projection are untouched; the `Prompt` field the plugin rendered is intentionally not restored (not in the parity target).
