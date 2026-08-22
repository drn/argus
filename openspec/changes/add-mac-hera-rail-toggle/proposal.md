## Why

The macOS companion app's Hera view is a flat, all-orchestrators roster that replaces the whole detail pane — it doesn't show nesting, doesn't group by kanban status, and isn't part of the sidebar. The user wants the sidebar itself to switch into a real nested Hera rail with a genuine coordinator/agent split-pane detail view, mirroring the TUI's native Hera view rather than a shallow approximation. Doing this properly requires finishing a pre-existing, deliberately-deferred gap: `GET /api/hera` doesn't expose orchestrator nesting today because the daemon couldn't import the TUI's bridging logic without pulling tview/tcell into the API binary — a package-boundary artifact, not a real blocker, once that logic is extracted.

## What Changes

- Extract `internal/tui/hera/model.go`'s nesting/bridging logic into a new tview-free package (`internal/hera/model`) importable by both the TUI and `internal/api`.
- `GET /api/hera` gains additive nesting fields (`bridge_parent_orch_id`/`bridge_parent_role_id` per orchestrator, `subtree_needs_input` per orchestrator, `needs_input` per role), computed via the shared package — finishing the "named follow-up" already documented in the `rest-api` base spec's scope note.
- The macOS app's sidebar gains a toggle switching between the flat task list and a nested Hera tree (kanban-grouped, mirroring the TUI's structure).
- A new dual-pane `HeraDetailView` replaces the single-task `DetailView` when the sidebar is in Hera mode and a role is selected: coordinator pane + agent pane side by side, matching the TUI's literal split geometry, with a roster-list details region replacing the agent pane when the selection is itself a coordinator.
- **BREAKING (mac app UX only, no API removal)**: the existing toolbar Hera toggle and `HeraTab.swift`'s standalone mount point are retired — the sidebar mode is the only way to see the Hera roster going forward.

Explicitly out of scope (named follow-ups, not silently dropped): the plan-DAG graph inside a coordinator's details region (v1 is roster-list only), any Hera mutation (stays TUI-only), the pre-existing `subtree_cost_usd` recursive-rollup gap, and web SPA parity for the new REST fields.

## Capabilities

### New Capabilities

(none — this extends existing capabilities)

### Modified Capabilities

- `rest-api`: `GET /api/hera`'s requirement gains the additive nesting/needs-input fields described above; its existing "MUST NOT import the TUI Hera package" constraint is satisfied via the new shared package rather than relaxed.
- `macos-app`: adds a sidebar Hera-tree mode, a dual-pane coordinator/agent detail view, and removes the existing toolbar Hera-toggle/`HeraTab` requirement (superseded by the sidebar mode).

## Impact

- `internal/tui/hera/model.go` → moved to `internal/hera/model` (new package); `internal/tui/hera` updates its imports.
- `internal/api/hera.go` (`handleHera`, `heraOrchJSON`, `heraRoleJSON`) — calls the shared package, adds the new response fields.
- `macos/Sources/Argus/HeraTab.swift` — removed.
- `macos/Sources/Argus/Sidebar.swift`, `ArgusApp.swift`/`ContentView.swift` (toolbar toggle removal) — sidebar mode toggle.
- `macos/Sources/Argus/DetailView.swift` — new sibling `HeraDetailView.swift` mounted alongside it, not a modification of it.
- `macos/Sources/ArgusKit/Models+Hera.swift` — decode the new fields plus the not-yet-decoded `kanban_status`.
- No changes to `internal/tui/hera`'s rendering behavior or the web SPA (the latter's gap is a named follow-up).
