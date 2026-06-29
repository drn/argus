# Tasks: add-diligence-profiles

**Design doc:** `openspec/changes/add-diligence-profiles/design.md`

**Branch target:** `argus/model-tiering` (never master).

Each top-level group (`## N`) is one implementation-fan-out unit for the `profiles-exec` sub-DAG. Groups
follow TDD (write failing tests first, then implement to green). `**Depends on:**` lines drive the
blocking edges; groups with the same dependency run in parallel. `make pre-pr` must pass on the
integrated result before ralph-review.

## 1. Profiles package core (load, extends, validate, seeds, CLI)

- [x] 1.1 Write failing tests for: loading `~/.argus/profiles/<name>.toml`; in-repo
  `.argus/profiles/<name>.toml` precedence + source reporting; `extends` overlay (recursive, child
  overrides only set fields); `default` resolution target. (scenarios in
  `specs/diligence-profiles/spec.md` → "Profile file format and discovery", "Profile inheritance")
- [x] 1.2 Write failing tests for validation: unknown archetype, out-of-enum effort/window, unknown
  model, backend-contributed model accepted, extends cycle, structurally-valid `[panel]` accepted,
  report-all-errors. ("Profile structure and archetypes", "Profile validation",
  "Reviewer-panel forward-reference seam")
- [x] 1.3 Implement `internal/profiles`: types (Profile, per-archetype entry, rigor, opaque panel),
  TOML loader with dual-location discovery + in-repo precedence + source tag, recursive `extends`
  resolution with cycle guard.
- [x] 1.4 Implement validation: canonical 13-archetype set; effort∈{low,medium,high};
  window∈{200k,1m}; model ∈ union(built-in `KnownModels` ∪ each `cfg.Backends[*].Models`); extends-cycle
  detection; `[panel]` structural-only check; aggregate all errors.
- [x] 1.5 Author the three seed profiles (`default`, `lean`, `customer_grade`) from the §2 archetype→model
  table (Fable absent), as repo example files (not auto-installed); add tests asserting each seed
  validates and `default` covers every archetype.
- [x] 1.6 Add the `validate` CLI affordance in `cmd/argus` (load+resolve+validate a named profile, report
  errors / confirm valid + source, non-zero on failure); NOT wired into build/CI/Make. Test the
  command's pure logic.

## 2. Persistence and config binding

**Depends on:** none (parallel with Stage 1)

- [x] 2.1 Write failing tests for: `tasks.archetype` round-trip; `projects.profile` round-trip;
  existing-rows-default-empty after column add; `config.Project.Profile` parse + default-when-absent.
  (`specs/data-persistence/spec.md`, `specs/config-management/spec.md`)
- [x] 2.2 Add columns `tasks.archetype`, `projects.profile`, `hera_roles.archetype` to `internal/db/schema.go`
  (+ idempotent add-column-if-missing for existing local DBs); extend INSERT/scan in `db/projects.go`,
  the tasks store, and `db/hera.go` role read/write.
- [x] 2.3 Add `Profile string \`toml:"profile"\`` to `config.Project` (`internal/config/config.go`); load
  + overlay handling; resolve empty→`default`.

## 3. Resolution, spawn-layer archetype, and env injection

**Depends on:** Stage 1, Stage 2

- [x] 3.1 Write failing tests for the resolution chain: task override wins; profile model applied by
  archetype; invalid-for-backend model falls through; missing/invalid profile → no `--model`; no
  archetype → profile skipped. (`specs/diligence-profiles/spec.md` → "Profile-aware model resolution")
- [x] 3.2 Write failing tests for `CreateAndStart` carrying `archetype` onto the task, and for env export
  (`ARGUS_PROFILE`/`ARGUS_ARCHETYPE`/`ARGUS_MODEL` present on resolution, omitted otherwise).
  (`specs/agent-execution/spec.md`)
- [x] 3.3 Extend `ResolveModel` (`internal/agent/agent.go`) to thread the resolved profile:
  `task.Model → profile[task.Archetype].model (backend-valid) → project/backend default → no model`.
- [x] 3.4 Add the optional `archetype` param to `agent.CreateAndStart`, persist onto the task; wire the
  daemon-side profile resolution + env export (`ARGUS_PROFILE`/`ARGUS_ARCHETYPE`/`ARGUS_MODEL`) into
  command construction (resolution runs daemon-side, outside the sandbox).

## 4. Hera + plan-node archetype propagation

**Depends on:** Stage 2, Stage 3

- [x] 4.1 Write failing tests: `hera_spawn_worker` archetype passthrough + `code_slice` default;
  planned-node archetype persisted; planned archetype propagates onto the materialized task.
  (`specs/hera-coordination/spec.md`, `specs/task-orchestration/spec.md`, `specs/data-persistence/spec.md`)
- [x] 4.2 Add `archetype` to `hera_spawn_worker` (MCP tool + `internal/hera`), pass to `CreateAndStart`,
  mirror onto the role; default `code_slice` when omitted, `orchestrator` for coordinators.
- [x] 4.3 Add `archetype` to plan-node authoring (planned-role persistence) + carry it through gater
  materialization into the task.

## 5. TUI surfaces (selectors, settings list, plan-view readout)

**Depends on:** Stage 3

- [x] 5.1 Write failing/smoke tests: new-task form shows Profile + Archetype selectors (defaults: bound
  profile / `(none)`); coordinator prompt omits archetype selector; selected archetype rides the task;
  settings project view offers only valid on-disk profiles and persists the name; plan/DAG node shows
  archetype + applied model and warns on missing/invalid profile.
  (`specs/forms-and-modals/spec.md`, `specs/settings-view/spec.md`, `specs/hera-view/spec.md`)
- [x] 5.2 Add Profile + Archetype cycling selectors to `internal/tui/newtaskform.go` (beside Backend/Model);
  hide archetype for new-coord; return profile override + archetype to the spawn caller. No new keybinding
  (selectors reuse the existing Tab + ◀/▶ idiom), so no help-modal/README change required.
- [x] 5.3 Add the validated profile select-list to the Settings project view (`internal/tui/projectform.go`):
  list on-disk profiles, only valid selectable, persist name only.
- [x] 5.4 Add the per-node archetype + model/effort readout and the missing/invalid-profile warning to
  `internal/tui/planview` (stamped off-thread via `HeraPage.SetTierResolver`). Smoke tests assert render.

## 6. Documentation and in-repo skill profile-awareness

**Depends on:** Stage 1

- [x] 6.1 Document the formal archetype list (13) + the model-naming convention in the README Reference
  appendix; add a profiles section (file location, `extends`, `validate`, seed profiles).
- [x] 6.2 Add a profile-awareness section to the in-repo hera/DAG skill (`.claude/skills/hera*`):
  reading `ARGUS_PROFILE`/`ARGUS_ARCHETYPE`, what the archetype means for the agent. (Reviewer-panel
  consumption is explicitly deferred to `2a-xvendor-review`.)
- [x] 6.3 Record non-obvious gotchas in `context/knowledge/gotchas/*.md` (profile resolution fail-open,
  in-repo precedence, daemon-side resolution vs sandbox, `[panel]` deferred seam) per the docs rule.

## 7. Review pass (ralph-review)

**Depends on:** Stage 1, Stage 2, Stage 3, Stage 4, Stage 5, Stage 6

- [x] 7.1 Run `/ralph-review` against the implemented work vs the deltas; auto-fix confident issues, park
  questions. (This is a `profiles-exec` sub-DAG node, blocked on all impl groups.)

## 8. Spec audit

**Depends on:** Stage 7

- [ ] 8.1 Run `/spec-audit` against the `add-diligence-profiles` deltas; confirm coverage; ensure
  `openspec validate add-diligence-profiles --strict` passes; archive the change **on the branch** (base
  specs updated atomically with the implementation). NO per-chunk GitHub PR and NO `iris_gh_pr_create`:
  the mini-pipeline's ralph-review + spec-audit nodes ARE the review. On completion, `iris_push` the
  final branch and `hera_send` the branch name + summary to coord, who advances `argus/model-tiering`.
