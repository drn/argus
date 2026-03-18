# Plan: Argus Codebase Architecture Improvement

**Date:** 2026-03-17
**Source:** Inline request: "review entire codebase and analyze structure/architecture. what would you improve. write a full plan"
**Status:** Draft
**Current Phase:** Phase 1

## Goal

Reduce architectural drag in Argus by separating UI orchestration from application logic, splitting persistence concerns into clearer layers, and hardening the daemon/session boundary so future features do not keep compounding complexity in `internal/ui` and `internal/db`.

## Background

Argus already has a sensible top-level package split:

- `cmd/argus` bootstraps the TUI and daemon entrypoints.
- `internal/agent` owns local process/session lifecycle and worktree/backend setup.
- `internal/daemon` and `internal/daemon/client` provide the persistent session layer.
- `internal/db` persists tasks, projects, backends, and config in SQLite.
- `internal/ui` renders Bubble Tea views and currently coordinates most workflows.

The codebase is healthy enough to change safely:

- `go test ./...` passes across all packages on 2026-03-17.
- Existing docs in `context/` already capture key architecture decisions and recent refactors.
- Package boundaries exist, but several responsibilities still leak across them.

The main structural pressure points found during review:

- `internal/ui/root.go` is the dominant coordinator at 1,985 lines and currently owns view state, task workflow rules, async command scheduling, daemon health handling, task persistence updates, and integration logic.
- `internal/db/db.go` and `internal/db/migrate.go` combine repository access, schema evolution, default seeding, and "repair old config on startup" behavior in one package.
- `internal/agent/agent.go` builds shell command strings directly and mixes backend resolution, prompt/session argument composition, sandbox wrapping, and execution strategy.
- `internal/daemon/client/handle.go` satisfies the same `SessionHandle` interface as in-process sessions, but some methods are no-ops or RPC-backed polling shims, which suggests the abstraction boundary is broader than the actual shared contract.

## Requirements

### Must Have

- Shrink the top-level UI coordinator into smaller application-facing components without regressing current Bubble Tea behavior.
- Separate database/repository logic from schema migration and startup "fixup" behavior.
- Clarify the session/runtime abstraction so remote and local session implementations do not need misleading no-op methods.
- Preserve the current user-facing workflow: task creation, worktree isolation, daemon persistence, reviews tab, and settings management.
- Keep the project green with package-level tests and targeted integration coverage for daemon lifecycle and task transitions.

### Should Have

- Introduce explicit architecture documentation for package ownership and data flow.
- Add service-level tests for task lifecycle decisions that currently live inside UI handlers.
- Reduce direct `exec.Command` usage from UI packages by routing shell interactions through narrower helpers/services.
- Improve observability and error handling around startup, resume, and daemon reconnection flows.

### Won't Do (this iteration)

- Rebuild the UI framework or replace Bubble Tea.
- Replace SQLite or redesign the full persistence model.
- Introduce heavy dependency injection frameworks or large third-party architecture libraries.
- Change core product behavior around worktrees, daemon ownership, or backend semantics unless required for correctness.

## Technical Approach

Refactor toward a clearer three-layer architecture:

1. Presentation layer
   `internal/ui` should handle rendering, keyboard/mouse events, and translation between Bubble Tea messages and application commands.

2. Application layer
   New service-style packages should own workflow orchestration such as task start/stop/resume, task completion transitions, daemon health/restart policy, and settings mutation.

3. Infrastructure layer
   `internal/db`, `internal/agent`, `internal/daemon`, `internal/github`, and git/worktree helpers should expose narrower interfaces that the application layer composes.

The design goal is not "maximum abstraction." It is to move domain decisions out of the UI and remove interfaces that currently hide materially different behaviors.

## Decisions

| Decision | Rationale |
|----------|-----------|
| Keep the existing package families (`agent`, `daemon`, `db`, `ui`) and refactor within them first | The current top-level split is understandable; the problem is responsibility density inside a few files, not total package sprawl |
| Introduce application services before further UI decomposition | The biggest current coupling is workflow logic in `internal/ui/root.go`; moving that logic first will make later view refactors safer |
| Split migration/bootstrap logic from runtime DB access | `internal/db` currently handles too many lifecycle concerns, making tests and future schema work harder |
| Narrow the session abstraction instead of growing it | `RemoteSession` currently implements methods like `AddWriter`/`RemoveWriter` as no-ops, which is a strong signal the shared interface is too wide |
| Prefer package-level architecture tests over end-to-end TUI snapshots | The highest-risk regressions are workflow and lifecycle rules, not visual layout rendering |

## Implementation Steps

### Phase 1: Document Current Architecture and Set Target Boundaries
**Status:** pending

- [ ] Create `context/research/architecture-map.md` — document the current runtime flow from `cmd/argus/main.go` through `ui`, `daemon`, `agent`, and `db`
- [ ] Add `internal/README.md` or `context/knowledge/architecture-boundaries.md` — define ownership for presentation, application, domain, and infrastructure responsibilities
- [ ] Capture dependency rules — `ui` may call application services, but not own lifecycle policy; infrastructure packages should not depend on `ui`
- [ ] Add a short "refactor guardrails" section — preserve behavior, package names, and user workflows during incremental changes

### Phase 2: Extract Task and Session Application Services
**Status:** pending

- [ ] Add `internal/app/tasks` (or similar) — move task start/stop/resume/finish rules out of [`root.go`](/Users/darrencheng/.argus/worktrees/argus/review-entire-codebase-analyze/internal/ui/root.go#L281)
- [ ] Move `startOrAttach`, post-exit status decisions, resume policy, and task-state persistence orchestration behind service methods
- [ ] Add a daemon health/restart coordinator service — move ping failure counting and restart policy out of [`root.go`](/Users/darrencheng/.argus/worktrees/argus/review-entire-codebase-analyze/internal/ui/root.go#L433)
- [ ] Replace direct `m.db.Get/Update/Tasks/Config` calls throughout `internal/ui/root.go` with narrower service calls
- [ ] Add focused tests for task lifecycle scenarios: fresh start, resume, quick exit, explicit stop, stream lost, daemon reconnect

### Phase 3: Split Persistence Into Store, Migration, and Bootstrap Layers
**Status:** pending

- [ ] Extract schema management from [`db.go`](/Users/darrencheng/.argus/worktrees/argus/review-entire-codebase-analyze/internal/db/db.go#L90) into dedicated migration files and versioned steps
- [ ] Move `seedDefaults` and backend `fixup` logic from [`migrate.go`](/Users/darrencheng/.argus/worktrees/argus/review-entire-codebase-analyze/internal/db/migrate.go#L42) into a clearer bootstrap/repair component
- [ ] Split runtime repositories by aggregate: tasks, projects, backends, config
- [ ] Introduce typed config accessors instead of stringly `config` key management for more settings paths
- [ ] Add migration tests that validate upgrade paths from older schemas and config states

### Phase 4: Tighten Runtime and Backend Execution Abstractions
**Status:** pending

- [ ] Refactor [`BuildCmd`](/Users/darrencheng/.argus/worktrees/argus/review-entire-codebase-analyze/internal/agent/agent.go#L70) into smaller units: backend resolution, argument composition, sandbox wrapping, and process launch spec construction
- [ ] Replace shell-string assembly where possible with structured command specs to reduce quoting complexity and backend edge cases
- [ ] Separate worktree/process helpers from UI utility files such as [`worktree.go`](/Users/darrencheng/.argus/worktrees/argus/review-entire-codebase-analyze/internal/ui/worktree.go) into non-UI infrastructure packages
- [ ] Move direct OS integrations (`open`, `tmux`, git branch cleanup) behind small adapter helpers so UI does not own subprocess policy
- [ ] Add tests for backend command generation across Claude-style and Codex-style backends, sandbox on/off, and bad config cases

### Phase 5: Narrow the Session/Daemon Contract
**Status:** pending

- [ ] Revisit [`SessionHandle`](/Users/darrencheng/.argus/worktrees/argus/review-entire-codebase-analyze/internal/agent/iface.go) and split it into smaller capabilities if needed: interactive IO, status inspection, output buffer access
- [ ] Remove no-op interface methods from remote session implementations such as [`RemoteSession.AddWriter`](/Users/darrencheng/.argus/worktrees/argus/review-entire-codebase-analyze/internal/daemon/client/handle.go#L154)
- [ ] Reduce repeated RPC polling in remote session getters by introducing cache invalidation or batched session status refresh where practical
- [ ] Add more explicit daemon contract tests around stream EOF, exit info delivery, reconnect behavior, and session status consistency
- [ ] Document which semantics are guaranteed locally vs remotely so future features do not assume hidden parity

### Phase 6: Decompose Large UI Files Into Screen Controllers and Pure Views
**Status:** pending

- [ ] Split [`root.go`](/Users/darrencheng/.argus/worktrees/argus/review-entire-codebase-analyze/internal/ui/root.go) into tab/screen controllers with minimal shared state
- [ ] Keep `root_views.go` as rendering-only and continue moving command/event logic out
- [ ] Extract reviews orchestration from `internal/ui/reviews.go` into a review service or controller, keeping view state local to the tab
- [ ] Standardize async command helpers for GitHub, git status, daemon logs, and file diff loading
- [ ] Add smoke tests per controller to validate message routing without relying on a monolithic root model test fixture

### Phase 7: Architecture Quality Gates
**Status:** pending

- [ ] Add file-size and dependency-direction checks in CI for the largest architectural hotspots
- [ ] Add package-level coverage goals for `internal/daemon`, `internal/daemon/client`, and new application service packages
- [ ] Add a lightweight architectural decision record whenever new cross-package boundaries are introduced
- [ ] Review logging strategy so UX logs, daemon logs, and user-visible errors are consistent and intentionally scoped

## Testing Strategy

- Keep `go test ./...` green after every phase.
- Add dedicated service tests for task lifecycle transitions before moving logic out of the UI.
- Add migration/bootstrap tests using temp SQLite databases for schema evolution and config repair cases.
- Add daemon/client contract tests for stream handling, exit info races, and reconnect behavior.
- Add backend command-generation tests for quoting, resume behavior, and sandbox wrapping.
- Add controller-level UI tests that validate message handling without requiring large end-to-end root model fixtures for every scenario.

## Risks & Open Questions

| Risk | Mitigation |
|------|------------|
| Refactors to `root.go` change user-visible workflow behavior | Extract lifecycle rules behind service tests first, then rewire the UI |
| Splitting `db` too aggressively creates churn without clarity | Start with migration/bootstrap separation, then move repositories by aggregate |
| Narrowing session interfaces breaks preview/agent view assumptions | Inventory actual call sites first and introduce capability interfaces incrementally |
| Structured command building may fight backend flexibility | Model command specs around existing backends first, keep an escape hatch for raw commands if truly needed |
| More packages may increase navigation cost | Prefer a few cohesive packages with clear ownership over many micro-packages |

- Should task lifecycle policy live in `internal/app/tasks` only, or should there also be a smaller domain package for pure transition rules?
- Should daemon health/restart policy become configurable, or remain internal behavior?
- Is the reviews tab important enough to deserve its own application package now, or should it wait until task/session refactors land?
- How much startup "auto-repair" of backend config should remain automatic versus being surfaced to the user?

## Dependencies

- Existing Bubble Tea model architecture in `internal/ui`
- SQLite schema and WAL behavior in `internal/db`
- Daemon/session runtime contracts in `internal/agent`, `internal/daemon`, and `internal/daemon/client`
- Existing architecture notes in `context/plans/daemon-architecture.md` and `context/research/daemon-lifecycle-flows.md`

## Errors Encountered

| Error | Attempt | Resolution |
|-------|---------|------------|
| `go test ./...` failed in sandbox because Go could not access the system build cache | Ran tests inside sandbox first | Re-ran with escalation; full suite passed |

## Estimated Scope

**Phases:** 7
**Tasks:** 29
**Files touched:** ~30-45
