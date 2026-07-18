# Proposal: Diligence profiles

## Why

Argus resolves a task's model with a single `task.Model → backend default` chain, so every spawned
agent — planner, CI-loop, security reviewer — gets the same model regardless of the job's leverage or
verifiability. Diligence profiles let an operator bind a project to a named, on-disk preset that routes
model (and carries effort/window + rigor flags + a deferred reviewer-panel seam) **per archetype**, so
premium spend flows up the tree and cheap models cover verifiable, high-volume work — with rigor varying
per project (lean for dogfooded tooling, heavy for customer-facing code).

## What Changes

- **New `diligence-profiles` capability:** TOML file-per-profile presets at `~/.argus/profiles/<name>.toml`
  (in-repo `.argus/profiles/<name>.toml` takes precedence), with `extends` inheritance, a `default`
  profile, per-archetype `model`/`effort`/`window`, `[rigor]` flags, and an opaque `[panel]` block.
- **Profile validation:** known archetypes only, enum `effort`/`window`, model names from the union of
  built-in aliases and every configured backend's `models` list, `extends`-cycle detection, and
  structural-only checking of `[panel]`. Exposed as a `validate` CLI affordance + programmatic API.
- **Profile-aware model resolution:** `ResolveModel` extends to
  `task.Model → profile[archetype].model → project default → backend default`, falling open to **no
  `--model`** when the bound profile is missing/invalid or the profile model is invalid for the worker's
  backend.
- **Archetype at the spawn layer:** a new optional `archetype` carried by `agent.CreateAndStart` and
  stored on the task, settable by any spawn path (new-task form, hera worker, freelance) via an archetype
  select-list on every new-agent prompt **except new hera coordinator**; `hera_spawn_worker` gains an
  `archetype` param and plan-DAG planned nodes carry it.
- **Project→profile binding (name only):** persisted on `config.Project` (new `Profile` field) and the
  `projects` DB table (new `profile` column). Bound via a **validated select-list in the Settings project
  view** (only valid profiles selectable) and overridable per-spawn via a modal Profile cycler.
- **Profile env injection:** `ARGUS_PROFILE`/`ARGUS_ARCHETYPE`/`ARGUS_MODEL` exported to the spawned
  agent (mirroring `ARGUS_TASK_ID`) so the in-repo hera/DAG skill is profile-aware.
- **Plan/DAG view surfacing:** each node shows its archetype + applied model/effort, and a warning
  decorates a project pointing at a missing/invalid profile.
- **Docs:** README archetype list + model-naming convention; in-repo hera/DAG skill profile-awareness.
- **Seed profiles:** `default`, `lean`, `customer_grade` from the §2 archetype→model table.

Not in this change (deferred): the `[panel]` composition **grammar** and its execution (owned by the
sibling `2a-xvendor-review` chunk — here `[panel]` is a structurally-validated forward reference); an
`mcp__argus__profile_resolve` MCP tool; wiring `effort`/`window` into backend CLI flags; per-agent
telemetry. **Fable is treated as absent** (seeds use the §2 "now" column).

## Capabilities

**New Capabilities**

- `diligence-profiles` — profile file schema/loading/inheritance, validation + allow-list, archetype
  taxonomy, profile-aware model resolution, profile env injection, CLI validate, seed profiles.

**Modified Capabilities**

- `agent-execution` — archetype carried by `CreateAndStart` onto the task; profile env vars exported.
- `forms-and-modals` — Profile + Archetype selectors on new-agent prompts (not new coordinator).
- `config-management` — `config.Project.Profile` binding field.
- `data-persistence` — `tasks.archetype`, `projects.profile`, `hera_roles.archetype` columns.
- `settings-view` — project profile selection from a validated on-disk profile list.
- `hera-coordination` — `hera_spawn_worker` gains an `archetype` param.
- `task-orchestration` — plan-DAG planned nodes carry an archetype, propagated at materialization.
- `hera-view` — plan/DAG view archetype + model/effort readout and missing-profile warning.

## Impact

- **Code:** new `internal/profiles` package (load/extends/validate/resolve); `internal/agent`
  (`ResolveModel` chain, `CreateAndStart` archetype param, env export); `internal/config` (`Project.Profile`);
  `internal/db` (3 columns + scans); `internal/tui/newtaskform` (Profile + Archetype selectors);
  `internal/tui/settings` (validated profile select-list); `internal/tui/planview` + `internal/tui/hera`
  (readout + warning); `internal/mcp` / `internal/hera` (`hera_spawn_worker` archetype, plan-node archetype);
  `cmd/argus` (`validate` subcommand).
- **Schema:** three nullable/default columns; no data migration (single-user policy).
- **Docs:** README Reference (archetype list, model-naming convention), in-repo hera/DAG skill.
- **Branch:** lands on `argus/model-tiering`, never master.
- **Dependencies:** the `[panel]` field reconciles with `2a-xvendor-review` when it lands; no hard
  code dependency in this change.
