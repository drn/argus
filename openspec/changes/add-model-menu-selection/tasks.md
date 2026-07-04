# Tasks: add-model-menu-selection

**Design doc:** `openspec/changes/add-model-menu-selection/design.md`

**Branch target:** `argus/model-tiering` (never master).

Each top-level group (`## N`) is one implementation-fan-out unit for the `menu-exec` sub-DAG. Groups
follow TDD (write failing tests first, then implement to green). `**Depends on:**` lines drive the
blocking edges; groups with the same dependency run in parallel. `make pre-pr` must pass on the
integrated result before ralph-review.

## 1. Profile schema v2 (menu form, validation, seed example)

- [x] 1.1 Write failing tests for: an archetype authored as a `menu` parses into an ordered
  `[]MenuOption`; a scalar archetype parses unchanged; a menu + scalar fields on the same archetype is
  rejected; a menu with fewer than 2 entries is rejected; each menu entry validates model/effort exactly
  like the scalar path; the widened 5-level effort enum (`low`/`medium`/`high`/`xhigh`/`max`) accepts all
  five and rejects an out-of-enum value. (`specs/diligence-profiles/spec.md` → "Profile structure and
  archetypes", "Profile validation")
- [x] 1.2 Add `Menu []MenuOption` to `profiles.Archetype` and a `MenuOption{Model, Effort string}` type
  (`internal/profiles/profiles.go`); widen `ValidEfforts` to the 5-level enum.
- [x] 1.3 Extend `Validate` (`internal/profiles/validate.go`) to: reject an archetype setting both scalar
  fields and `menu`; reject a `menu` with < 2 entries; validate each menu entry's model/effort with the
  same per-field checks as the scalar path, aggregated into the existing all-errors report.
- [x] 1.4 Update `overlay` (`internal/profiles/load.go`) so `extends` inheritance correctly overlays a
  child's `menu` field (declared-field detection via `md.IsDefined`, mirroring the existing per-field
  scalar overlay) — write a failing test for a child overriding a parent's menu first.
- [x] 1.5 Rewrite the `default` seed profile's `code_slice` archetype as a menu (at least two ordered
  `{model, effort}` pairs, cheapest first); add/update the seed test asserting it validates and is
  menu-shaped. (`specs/diligence-profiles/spec.md` → "Seed profiles")

## 2. Effort persistence (DB columns, task/CreateInput threading)

**Depends on:** none (parallel with Stage 1)

- [x] 2.1 Write failing tests for: `tasks.effort` round-trips; `hera_roles.effort` round-trips and
  propagates at materialization; existing rows default to empty after the column adds.
  (`specs/data-persistence/spec.md` → "Archetype and profile-binding columns")
- [x] 2.2 Add `tasks.effort` and `hera_roles.effort` columns to `internal/db/schema.go` (idempotent
  add-column-if-missing, matching the existing `tasks.archetype`/`hera_roles.archetype` pattern); extend
  the tasks store and `db/hera.go` role read/write.
- [x] 2.3 Add `Effort string` to `model.Task` (`internal/model/task.go`) and to `agent.CreateInput`
  (`internal/agent/create.go`), threaded the same way `Model`/`Archetype`/`Profile` already are.

## 3. Resolution, governance, and per-backend effort injection

**Depends on:** Stage 1, Stage 2

- [x] 3.1 Write failing tests for the menu governance rules: matching full (model, effort) override
  honored; non-matching full override substituted with the menu's first entry and logged; partial
  override (only one field set) honors the set field and defaults the other from the cheapest entry, no
  membership check; neither set defaults to the cheapest entry; a scalar (non-menu) archetype is never
  gated, regardless of override. (`specs/diligence-profiles/spec.md` → "Menu-based archetype resolution
  and governance")
- [x] 3.2 Write failing tests for paired model+effort resolution precedence (task override → profile pick
  → project/backend default → none) and for per-backend injection: `--effort <level>` for Claude-style
  backends, `-c model_reasoning_effort=<level>` for codex, no flag for pi/unknown/custom, and no
  double-injection when the backend command already names an effort flag/override.
  (`specs/diligence-profiles/spec.md` → "Profile-aware model resolution")
- [x] 3.3 Write failing tests for env export: `ARGUS_EFFORT` joins the existing
  `ARGUS_PROFILE`/`ARGUS_ARCHETYPE`/`ARGUS_MODEL` trio, all four exported together or omitted together.
  (`specs/agent-execution/spec.md`, `specs/diligence-profiles/spec.md` → "Profile environment injection")
- [x] 3.4 Change `agent.ResolveModel`'s signature to also resolve and return effort (paired with model);
  implement the menu-governance rules from 3.1 inside it (or a sibling function it calls).
- [x] 3.5 Update `BuildCmd` (`internal/agent/agent.go`) to inject the resolved effort per-backend
  (mirroring the existing `--model` injection: `hasEffortFlag` guard, backend-family branch) and to
  export `ARGUS_EFFORT` alongside the existing trio.
- [x] 3.6 Update the one other `ResolveModel` call site, `internal/tui/hera_tiering.go`
  (`resolveHeraTier`), for the new signature; extend `RoleView`/plan-view rendering if a menu-resolved
  node's readout needs to say "menu" rather than a single fixed value (check current
  `AppliedModel`/`AppliedEffort` rendering in `internal/tui/planview` for what "resolved at spawn time,
  not author time" means for a never-materialized planned node).

## 4. Hera spawn effort param + hera_retier tool

**Depends on:** Stage 2, Stage 3

- [x] 4.1 Write failing tests for: `hera_spawn_worker` accepts and passes through an `effort` param; an
  off-menu (model, effort) pair at spawn is substituted with the cheapest entry and logged.
  (`specs/hera-coordination/spec.md` → "hera_spawn_worker creates a born-bound worker transactionally")
- [x] 4.2 Add the optional `effort` param to `hera_spawn_worker` (MCP tool schema + `internal/agent` spawn
  path), passed through to `CreateAndStart` the same way `model` already is.
- [x] 4.3 Write failing tests for `hera_retier`: non-coordinator caller rejected; a matching pair is
  delivered as `/model`/`/effort` PTY writes via the existing idle-gated delivery primitive; an off-menu
  pair is substituted and logged; a non-Claude-style backend target returns an explicit unsupported
  error; an unchanged effort is not re-sent. (`specs/hera-coordination/spec.md` → "hera_retier retiers a
  live, bound worker")
- [x] 4.4 Implement `hera_retier` (`internal/mcp/hera.go`): coordinator-only auth check, live
  re-resolution of the target's archetype/profile (reusing the Stage 3 governance function, not a cached
  value), and delivery through the existing `internal/hera/service.go` idle-gated single-writer PTY-write
  primitive — no new write path.

## 5. Documentation (hera skill conventions, README, gotchas)

**Depends on:** Stage 3, Stage 4

- [x] 5.1 **Pre-check:** confirm `mcp__argus__profile_resolve` (built by sibling worker
  `2a-xvendor-review` on `argus/model-tiering`) is present on this branch before writing 5.2's check-in
  convention text — rebase to pick it up, or coordinate sequencing with `coord`, if it hasn't landed yet.
  (Not present at check time — grep of `internal/mcp/*.go` came back empty; flagged to `coord` via
  `hera_send`, who did not direct a hold, so documented anyway, referencing the tool by name as a forward
  reference to land alongside `2a-xvendor-review`'s work.)
- [x] 5.2 Add the spawner selection convention (default cheapest; climb only when blast radius, ambiguity,
  or low verifiability justify it; log a one-line rationale) and the non-blocking worker check-in
  convention (call `profile_resolve` to detect a menu-resolved archetype; announce the cheapest pick + the
  full menu via `hera_send`; proceed immediately, never block) to the in-repo hera skill.
- [x] 5.3 Document the menu TOML form, the 5-level effort enum, and the `hera_retier` tool in the README
  Reference appendix (profiles section + MCP tools table).
- [x] 5.4 Record non-obvious gotchas in `context/knowledge/gotchas/misc.md` (menu governance fail-open
  scope, the `/model`/`/effort` live-retier mechanism and its Claude-only scope, the OpenSpec
  single-physical-line requirement-text gotcha hit while authoring this change's deltas).
- [x] 5.5 Add a "Future Work" note to `design.md`'s Open Questions section documenting the
  `hera_plan_node`/`hera_plan` vs `hera_spawn_worker` effort-param asymmetry (flagged by `2a-persist`
  during implementation; intentionally out of this change's scope).

## 6. Review pass (ralph-review)

**Depends on:** Stage 1, Stage 2, Stage 3, Stage 4, Stage 5

- [ ] 6.1 Run `/ralph-review` against the implemented work vs the deltas; auto-fix confident issues, park
  questions. (This is a `menu-exec` sub-DAG node, blocked on all impl groups.)

## 7. Spec audit, archive, and report

**Depends on:** Stage 6

- [ ] 7.1 Run `/spec-audit` against the `add-model-menu-selection` deltas; confirm coverage; ensure
  `openspec validate add-model-menu-selection --strict` passes; archive the change **on the branch** (base
  specs updated atomically with the implementation). NO per-chunk GitHub PR and NO `iris_gh_pr_create`:
  the mini-pipeline's ralph-review + spec-audit nodes ARE the review. On completion, `iris_push` the final
  branch and `hera_send` the branch name + a plain-language "how it works" summary to `coord`, who
  advances `argus/model-tiering`.
