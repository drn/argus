## Context

The macOS companion app's Hera support today is a toolbar toggle (`HeraTab.swift`) that replaces the whole detail pane with a flat, all-orchestrators roster — polled every 5s, click-to-select-task already wired. It doesn't show nesting (a worker role whose bound task is itself another orchestrator's coordinator — a "bridge"), doesn't group by kanban status, and isn't part of the sidebar/primary-nav surface.

The user wants the sidebar itself to switch into a real Hera rail — nested tree, matching the TUI — plus a genuine coordinator/agent split-pane detail view and a way to see a coordinator's own details, all mirroring the TUI's native Hera view (`internal/tui/hera`).

**Discovery finding — this is a partial rewind of prior deliberate scope-cutting, not new ground.** `openspec/specs/rest-api/spec.md`'s existing `GET /api/hera` requirement already documents why nesting isn't exposed today: *"The handler MUST source all data from the database and MUST NOT import the TUI Hera package (to keep tview out of the API binary)"* and its scope note explicitly says *"Reproducing that recursive walk here would require importing `internal/tui/hera`... A true cross-orchestrator recursive total for REST/remote-mode consumers is a named follow-up, not shipped in this change."* That follow-up was left open because `internal/tui/hera`'s nesting/bridging logic (`model.go`) shares a Go package with its tview/tcell rendering code (`details.go`, `panes.go`) — importing any part of the package into `internal/api` would pull tview/tcell into the daemon binary.

Investigation for this change confirms `model.go` itself (`BuildModel`, `RoleView`, `OrchView`, `bridgeIndex`, `canonicalParents`, `coordBridgeParentOf`, `BridgeSubtree`) imports only `errors, fmt, sort, strconv, time, internal/db, internal/model, internal/uxlog` — **zero tview/tcell**. The coupling is purely a package-boundary artifact, not a real logic entanglement. Moving `model.go`'s contents into a new package (`internal/hera/model`) that both `internal/tui/hera` and `internal/api` import resolves the blocker mechanically, finishing the deferred follow-up rather than reinventing it.

Investigation also confirmed the TUI's coordinator/agent split is **literal and simultaneous**, not a mode switch: `page.go`'s `Draw()` splits the right area evenly into a coordinator pane (always the current orchestrator's own terminal) and an agent pane (the selected worker's terminal) — except when the *selection itself is a coordinator*, in which case the agent-pane slot swaps from "terminal" to a stacked roster region (`detailsMode`). This is confirmed behavior, not an assumption, and this change mirrors it as such.

## Goals / Non-Goals

**Goals:**

- The mac app sidebar can switch into a nested Hera tree (orchestrators → nested sub-orchestrators via bridges → their roles), grouped by kanban status, mirroring the TUI's actual structure — not a flat approximation.
- Selecting a role in Hera mode shows a genuine dual-pane detail view: the current orchestrator's coordinator pane + the selected role's agent pane, side by side — matching the TUI's real geometry.
- Selecting a coordinator swaps its pane to a roster-list details region (v1: list only, no plan-DAG graph — see Non-Goals).
- The existing toolbar `HeraTab` toggle is retired; the sidebar mode is the only way to see the Hera roster going forward (no duplicate UI for the same data).
- `GET /api/hera` gains the nesting/bridge fields needed to build this, computed server-side via the newly-shared `internal/hera/model` package — finishing the pre-existing "named follow-up" in the rest-api base spec.

**Non-Goals (deferred, named follow-ups):**

- The plan-DAG graph inside a coordinator's details region — v1 shows the roster list only (the same data `HeraTab` already renders), per user decision. Graph rendering has no existing mac-app precedent and is a materially bigger lift.
- Any Hera mutation (spawn worker, send message, plan-node edit) — stays TUI-only, per the existing documented parity gap. This change is read-only navigation, same as today's `HeraTab`, just relocated and enriched.
- The pre-existing `subtree_cost_usd` recursive-rollup follow-up (rest-api spec's scope note) — the same package extraction happens to unblock it, but fixing it is out of scope here to avoid scope creep; left for whoever picks it up next.
- Web SPA parity for the enriched `/api/hera` response — named as a follow-up per this repo's frontend-parity rule (same precedent as `hera mutations are TUI-only` and the keybinding-parity change's Claude-session-switcher gap).
- Simplifying the TUI to consume the new shared package's REST-shaped output instead of calling `BuildModel` directly — the TUI's local-mode path is unaffected by this change; no forced refactor there (Chesterton's Fence — it isn't broken).

## Decisions

### D1: Extract `model.go` into `internal/hera/model` (tview-free); both the TUI and the daemon's REST handler import it

Move `RoleView`/`OrchView`/`Model`/`BuildModel` and the bridging helpers as-is into a new package. `internal/tui/hera` updates its imports (mechanical, byte-identical behavior — this is a move, not a rewrite). `internal/api/hera.go`'s handler calls the same `BuildModel` the TUI does, so nesting is computed once, not reimplemented in Swift — avoiding drift between two independent tree-building implementations.

**Alternative considered:** re-derive bridging in Swift from the flat `/api/hera` data. Rejected — it would duplicate ~300 lines of nontrivial matching logic in a second language, with no mechanism to keep the two in sync as the TUI's bridging logic evolves.

### D2: `GET /api/hera` gains nesting + needs-input fields, additively

Add (per orchestrator) a nullable `bridge_parent_orch_id`/`bridge_parent_role_id` pointer (null when top-level, set when nested under another orchestrator's worker-bridge role) and a `subtree_needs_input` boolean rollup; add (per role) a `needs_input` boolean. The mac app assembles the tree client-side from these pointers rather than the daemon shipping a pre-nested JSON structure — keeps the response shape close to today's (additive fields, not a restructure) and doesn't force the daemon to guess the client's preferred tree shape.

**Risk:** `BuildModel`'s live-state parameters (`needsInput`, `sessionIdle`, `sessionRunning`, `sustainedActive`) are currently fed from the TUI App's in-memory sets, not a daemon-authoritative store. → **Mitigation**: source `needsInput`/`sessionIdle`/`sessionRunning` from the same idle-detection signal that already backs `GET /api/tasks` and the SSE events stream (daemon-authoritative, not TUI-session-scoped) — verify field-for-field parity with the TUI's maps during implementation. `sustainedActive` only affects a rail-dimming nuance in the TUI (not correctness); if no daemon-authoritative equivalent exists, it's acceptable to pass `false` for all roles in the REST response for v1 and note the cosmetic gap.

### D3: Retire `HeraTab`; the sidebar mode subsumes it entirely

Remove the toolbar Hera toggle and `HeraTab.swift`'s standalone mount point. Its existing data-fetch and `selectHeraTask` wiring get reused inside the new sidebar tree view, not thrown away.

### D4: New `HeraDetailView` dual-pane container, mirroring the TUI's real geometry

A new SwiftUI view — two side-by-side panels, each hosting a `TerminalTab`-equivalent bound to a different task: left = the active orchestrator's coordinator task, right = the selected role's task, OR (when the selection is itself a coordinator) a roster-list details region in place of the right panel's terminal. Mounted in place of the single-task `DetailView` whenever the sidebar is in Hera mode. The existing single-task `DetailView`/`TabView` is untouched for the flat Tasks mode — this is a parallel container, not a modification of it.

### D5: Kanban grouping and fold state are mac-app-local, matching the TUI

Kanban section grouping uses the existing `kanban_status` field (already in the wire model, not yet decoded in `Models+Hera.swift` — trivial add). Fold/expand state lives in the mac app's view state only, not persisted server-side or synced across clients — same as the TUI.

## Risks / Trade-offs

- **[Risk]** Moving `model.go` changes an import path used throughout `internal/tui/hera`. → **Mitigation**: mechanical move with a mechanical import-path update; the TUI's existing test suite (`internal/tui/hera/*_test.go`) must still pass unchanged, which is the regression guardrail.
- **[Risk]** The new REST fields touch a base spec (`rest-api`) that other clients (web SPA, TUI-over-remote) also read. → **Mitigation**: additive fields only (D2), nothing existing changes shape; the frontend-parity rule's named-follow-up mechanism covers the web SPA gap explicitly rather than silently.
- **[Risk]** `sustainedActive` may not have a clean daemon-authoritative source (see D2's risk). → **Mitigation**: documented fallback (pass `false`), scoped as a cosmetic gap, not a blocker.

## Open Questions

- Exact JSON field names for D2 are decided during implementation (tasks.md), constrained only by "additive, doesn't rename or restructure existing `/api/hera` fields."
- Whether `sustainedActive` has a daemon-authoritative equivalent is resolved during implementation; either answer is acceptable per D2's documented fallback.

## Acceptance criteria

**Daemon (D1, D2):**

- It should return `bridge_parent_orch_id`/`bridge_parent_role_id` (null when top-level) for every orchestrator in `GET /api/hera`.
- It should return `subtree_needs_input` for every orchestrator and `needs_input` for every role in `GET /api/hera`.
- It should compute these fields using the same bridging logic the TUI uses (via the shared `internal/hera/model` package), not a separate reimplementation.
- It should leave every existing `GET /api/hera` field and its meaning unchanged.

**Mac app sidebar (D3, D5):**

- It should show a toggle that switches the sidebar between the flat task list and a nested Hera tree.
- It should group top-level orchestrators in the Hera tree by kanban status.
- It should render a nested orchestrator under its bridge parent's role, not as a separate top-level entry.
- It should remove the toolbar's standalone Hera toggle and `HeraTab` mount point once the sidebar mode ships.
- It should preserve fold/expand state locally across a refresh poll, without persisting it server-side.

**Mac app detail view (D4):**

- It should show a dual-pane view when a role is selected in Hera mode: the active orchestrator's coordinator pane on one side, the selected role's agent pane on the other.
- It should replace the agent-pane side with a roster-list details region (not a terminal) when the selection is itself a coordinator.
- It should leave the existing single-task `DetailView` behavior unchanged when the sidebar is in flat Tasks mode.
