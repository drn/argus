**Design doc:** `openspec/changes/add-mac-hera-rail-toggle/design.md`

## 1. Tests

- [x] 1.1 Write failing tests for each scenario in `specs/rest-api/spec.md` (nesting fields, subtree_needs_input, needs_input, unchanged existing fields/scenarios)
- [x] 1.2 Write failing tests for each scenario in `specs/macos-app/spec.md` that pertain to sidebar mode (sidebar mode toggle, kanban grouping, nested rendering, fold state) — the dual-pane/coordinator-roster/no-mutation-controls scenarios are covered by `HeraDetailPaneResolverTests.swift` (the pure pane-selection decision) plus manual/build verification for the SwiftUI wiring, since `ArgusKitTests` has no SwiftUI/AppState harness
- [x] 1.3 Confirm every "it should" criterion in `design.md`'s Acceptance criteria section has a corresponding failing test (Prove-It Pattern) — daemon D1/D2 portion only

## 2. Extract shared nesting package (daemon)

**Depends on:** Stage 1

- [x] 2.1 Move `RoleView`/`OrchView`/`Model`/`BuildModel` and the bridging helpers (`bridgeIndex`, `canonicalParents`, `coordBridgeParentOf`, `BridgeSubtree`) from `internal/tui/hera/model.go` into a new package `internal/hera/model` — mechanical move, no logic changes
- [x] 2.2 Update `internal/tui/hera`'s imports to the new package; confirm `internal/tui/hera`'s existing test suite still passes unchanged (regression guardrail — behavior must stay byte-identical)
- [x] 2.3 Confirm no other package outside `internal/tui/hera` needs updating, and that the new package has zero tview/tcell imports — see gotchas/hera-view.md for the one exception found (internal/tui's App-level heraactions.go etc., resolved via type aliases)

## 3. REST API nesting/needs-input fields

**Depends on:** Stage 2

- [x] 3.1 Wire `internal/api/hera.go`'s handler to call `internal/hera/model.BuildModel`, sourcing `needsInput`/`sessionIdle`/`sessionRunning` from the same daemon-authoritative idle-detection signal that backs `GET /api/tasks` and the SSE events stream (verify field-for-field parity with the TUI's in-memory maps); pass `false` for `sustainedActive` if no daemon-authoritative equivalent exists (documented cosmetic gap per design.md) — implemented via `s.sessionStateMaps()` (the same helper `GET /api/tasks` uses) and `nil` for sustainedActive
- [x] 3.2 Add `bridge_parent_orch_id`/`bridge_parent_role_id` to each orchestrator's JSON envelope (`heraOrchJSON`) — via new `heramodel.Model.BridgeParentOf`
- [x] 3.3 Add `subtree_needs_input` to each orchestrator's JSON envelope
- [x] 3.4 Add `needs_input` to each role's JSON envelope (`heraRoleJSON`)
- [x] 3.5 Confirm no existing field's shape or meaning changed (additive only)
- [x] 3.6 Verify tests from 1.1 pass

## 4. Sidebar Hera-tree mode (mac app)

**Depends on:** Stage 3

- [x] 4.1 Decode `kanban_status` and the new nesting/needs-input fields in `Models+Hera.swift`
- [x] 4.2 Add the sidebar mode toggle (flat task list ↔ Hera tree)
- [x] 4.3 Build the tree row-producing pipeline: group top-level orchestrators (null `bridge_parent_orch_id`) by `kanban_status`; nest orchestrators with a set `bridge_parent_orch_id` under their parent's bridging role
- [x] 4.4 Wire local (unpersisted) fold/expand state per tree node
- [x] 4.5 Reuse `HeraTab`'s existing data-fetch polling and `selectHeraTask` wiring inside the new tree view rather than duplicating it — mirrors the same fetch call (`AppState.heraRoster()`, via a new `refreshHeraRoster()` wrapper) and poll cadence since `HeraTab.swift` itself stays untouched until Stage 6; `selectHeraRole(_:)` is a new, deliberately separate selection handler (see design notes in AppState.swift)
- [x] 4.6 Verify tests from 1.2 (sidebar-mode scenarios) pass

## 5. Dual-pane Hera detail view (mac app)

**Depends on:** Stage 3

- [x] 5.1 Build `HeraDetailView.swift`: a new dual-pane container, distinct from `DetailView.swift`, hosting two task-bound panels side by side
- [x] 5.2 Left pane always binds to the active orchestrator's coordinator task; right pane binds to the selected role's task
- [x] 5.3 When the selected role's `kind` is `coordinator`, swap the right pane's content to a read-only roster-list details region (reusing the roster row rendering from the old `HeraTab`) instead of a terminal
- [x] 5.4 Mount `HeraDetailView` in place of `DetailView` only while the sidebar is in Hera mode; confirm `DetailView`'s flat-mode behavior is untouched
- [x] 5.5 Verify tests from 1.2 (dual-pane scenarios) pass

## 6. Retire the old toolbar Hera toggle

**Depends on:** Stages 4, 5

- [x] 6.1 Remove the toolbar's Hera toggle control and `HeraTab.swift`'s standalone mount point
- [x] 6.2 Confirm no dangling references remain (build clean)
- [x] 6.3 Verify the REMOVED-requirement migration path in `specs/macos-app/spec.md` matches actual behavior (sidebar toggle is the only way in)

## 7. Documentation and wrap-up

**Depends on:** Stages 2-6

- [x] 7.1 Document the `internal/hera/model` package extraction and the REST-vs-TUI live-state-source distinction as a gotcha in `context/knowledge/gotchas/hera-view.md` or `daemon-rpc.md` (whichever fits) and `gotchas/macos-app.md` — the package-extraction gotcha was already documented in `hera-view.md` by an earlier stage; added the REST-vs-TUI live-state-source bullet there plus a new `HeraTreeBuilder`/`HeraDetailPaneResolver` pure-logic + single-poll-loop entry in `gotchas/macos-app.md`
- [x] 7.2 Update the README's Reference section for the new `/api/hera` fields if it documents REST fields at that level of detail (check first; skip if it doesn't) — updated the `GET /api/hera` row (line ~733) with `kanban_status`, `bridge_parent_orch_id`/`bridge_parent_role_id`, `subtree_needs_input`, `needs_input`; no other README text described the retired toolbar toggle specifically
- [x] 7.3 Run `make test` (daemon/Go side), `make mac-build`, and `make mac-test`; fix any failures — all green (the 2 known env-contamination false-failures in `internal/agent` confirmed and excluded per this session's own leaked `ARGUS_*` vars)
- [x] 7.4 `openspec archive add-mac-hera-rail-toggle` (or the manual merge-and-move fallback) on the change branch before merge, per this repo's CLAUDE.md — archived via CLI as `2026-08-22-add-mac-hera-rail-toggle` (UTC-dated by the CLI's own clock; local date was still 2026-08-21 at archive time)
