## Context

Argus currently resolves a task's model through a single two-step chain in `ResolveModel`
(`internal/agent/agent.go:94`): the per-task override (`task.Model`) if set, else the backend's
default model. There is no notion of *what kind of job* a task is, so every spawned agent — a
high-leverage planner, a mechanical CI-loop, a security reviewer — defaults to the same model. The
Fable-strategy synthesis (`01-synthesis.md` §2/§11/§12, `02-plan.md` D2) established that the win is
*reallocation, not addition*: spend premium up the tree (plan/route/review), save it down the tree
(verifiable, high-volume code/CI work). That requires routing model choice **per archetype**, and
varying **rigor** (review passes, gating, security spot-check, reviewer-panel composition) **per
project** — heavy for customer-facing code with no dogfooding loop, lean for daily-driven personal
tooling.

This change introduces **diligence profiles**: named, on-disk presets that describe, per archetype,
the model + effort + context-window plus process/rigor flags and a (deferred) reviewer-panel spec,
with a `default` profile and `extends` inheritance. A project points at a profile *by name*; argus
resolves the bound profile at spawn and feeds the per-archetype model into the existing `ResolveModel`
chain, exports the resolution to the agent's environment so the in-repo hera/DAG skill is
profile-aware, surfaces the resolved archetype + model/effort in the plan/DAG view, and warns there
when a project points at a missing or invalid profile.

**Constraints:**

- This workstream lands on the feature branch **`argus/model-tiering`** (master + the sibling
  env-map work), **never master**. PR/merge target is `argus/model-tiering`.
- Single-user repo (breaking changes are fine; no backwards-compat or migration shims — see CLAUDE.md
  "Breaking Changes Policy"). Column adds are plain schema edits.
- Specs are LOCAL DOCS only; nothing in CI/Make/Go build reads them. The quality gate stays
  `make pre-pr`.
- Profiles must work inside the argus sandbox, where global `~/.argus/` reads can EPERM — so anything
  the agent must read at runtime travels by environment, not by file-read; resolution + env-export
  happen daemon-side at spawn (outside the sandbox).

## Goals / Non-Goals

**Goals:**

- A profile is a named, on-disk TOML file (one file per profile) describing per-archetype
  `model`/`effort`/`window`, `[rigor]` flags, and a `[panel]` reviewer block, with `extends`
  inheritance and a `default` profile.
- The DB stores **only** the project→profile-NAME reference (no profiles table, no profile bodies).
- The **Settings pane** project view presents a **validated select-list of on-disk profiles**; only
  profiles that pass validation are selectable, and the chosen name is persisted as the project's
  binding.
- `ResolveModel` extends to `task.Model → profile[archetype].model → project default → backend.Model`.
- A task's **archetype** is a first-class, optional property set at the **argus spawn layer**
  (`CreateAndStart`), so it works for any spawned agent — new task, hera worker, freelancer — via an
  archetype select-list on every new-agent prompt **except new hera coordinator**. Hera/plan spawns set
  it programmatically. Stored on the task; resolution reads `task.Archetype`.
- A `validate` affordance (CLI + programmatic) flags unknown archetypes, out-of-enum
  `effort`/`window`, model names outside the union allow-list, and `extends` cycles.
- The **plan/DAG view** shows, per node, the selected **archetype** and the **applied model/effort**,
  and warns when a project points at a missing or invalid profile.
- At runtime, a missing/invalid bound profile falls back to passing **no** `--model` (the agent uses
  the user's own CLI default) — never a hard failure.
- The resolved profile/archetype/model are exported to the spawned agent's environment
  (`ARGUS_PROFILE`, `ARGUS_ARCHETYPE`, `ARGUS_MODEL`) so the in-repo hera/DAG skill is profile-aware.
- Documented: the formal archetype list + the model-naming convention (README), and the in-repo
  hera/DAG skill's profile-awareness section.
- Seed `default`, `lean`, and `customer_grade` profiles from the §2 archetype→model table.

**Non-Goals:**

- **No profiles table / no profile bodies in the DB.** The DB holds the project→name reference only.
- **The `reviewer_panel`/`[panel]` composition GRAMMAR is owned by the sibling `2a-xvendor-review`
  chunk, not this one.** Here the profile merely *holds* an opaque panel block and validation checks
  only that it is structurally well-formed (present, right shape) — this change does NOT define the
  compose-by-lens grammar, the model/lens roster, or the Opus-synthesizer contract. Clean typed seam;
  reconciled when 2a's design lands.
- **No `mcp__argus__profile_resolve` MCP tool** and **no execution of the reviewer panel** in this
  change — both are deferred to the review-fan-out sibling chunk. This change only exports
  `profile`+`archetype`(+`model`) by env; the skill's *self-awareness* is enough now.
- **No Fable.** Fable is treated as absent (pulled from market); seed profiles use the §2 "Model (now)"
  column. The "Fable (if it returns)" notes survive only as documentation.
- **No per-agent telemetry / cost-in-the-rail** (that is D1, a separate chunk).
- **`effort`/`window` are schema-only** beyond the readout: carried, validated, exported, and displayed
  in the DAG view, but only `model` is wired into `ResolveModel`. Wiring effort into each backend's CLI
  flags is out of scope (YAGNI; documented field).

## Decisions

### D-FILE: TOML, file-per-profile

A profile is one TOML file at `~/.argus/profiles/<name>.toml`, optionally `.argus/profiles/<name>.toml`
in the project repo (gitignored or checked-in, operator's choice). In-repo files take precedence over
the global dir for the same name (so a repo can pin its own profile); the global dir is the fallback
library.

```toml
# ~/.argus/profiles/lean.toml   (or .argus/profiles/lean.toml in-repo)
extends = "default"

[archetype.brainstorm]
model  = "opus"
effort = "high"
window = "200k"

[archetype.code_slice]
model = "sonnet"

[archetype.ci_loop]
model = "haiku"

[rigor]
review_passes       = 1
gating              = false
security_spot_check = false

[panel]
# OPAQUE forward-reference: 2a-xvendor-review owns this block's grammar.
# This change validates only that it is structurally well-formed.
reviewers = ["opus"]
```

- **Why over a single `profiles.toml`:** per-archetype nesting in one file gets deep; sharing one
  profile in-repo means carrying the whole file. File-per-profile is self-contained and clean to check
  in / gitignore per project.
- **Why over YAML:** argus config is already TOML (`config.toml` overlay); a second format means two
  parsers and two mental models.

### D-PANEL-SEAM: `[panel]` is a deferred typed extension point

`2a-xvendor-review` owns what a reviewer panel *is* (compose-by-lens, model/lens roster, the
Opus-synthesizer contract). This change therefore treats `[panel]` as an opaque, forward-referenced
block: the loader parses and retains it, and `validate` checks only **structural** conformance (it is a
table of the expected coarse shape). It does **not** validate panel semantics or lock the grammar. When
2a lands, that one field's validation + consumption is reconciled; nothing else in the schema depends
on it.

### D-VALID: union allow-list; warn-and-fallback at runtime

`validate` checks a profile conforms:

- **Known archetypes only** — the 13 canonical names (below). Unknown table → error.
- **Enum `effort`** ∈ {`low`,`medium`,`high`} and **enum `window`** ∈ {`200k`,`1m`}.
- **Model names** ∈ the **union** of built-in aliases (`KnownModels("claude")` = opus/sonnet/haiku;
  `KnownModels("codex")` = gpt-5-codex/gpt-5) **and** every configured backend's `models` list (so the
  existing codex backend's models, and any future foreign reviewer added to config, are valid without
  an argus code change).
- **`extends` cycle detection** — the inheritance chain must terminate; a cycle is an error.
- **`[panel]`** — structural well-formedness only (see D-PANEL-SEAM).

At **runtime**, an absent or invalid bound profile does **not** hard-fail a spawn: argus logs the
problem (uxlog), surfaces a plan/DAG-view warning, and passes **no** `--model`. Validation is the *loud
surface* (CLI + Settings select-list gating + DAG warning); resolution *fails open*.

### D-ARCHETYPE: a task-level property set at the argus spawn layer

Archetype is **not** hera-only. It is an optional property of the spawned task, set at the single
fresh-task creation chokepoint `agent.CreateAndStart`, so every spawn path can set it:

- **New-task form** and any **new-worker** prompt expose an **Archetype select-list**; **new hera
  coordinator** does not (a coordinator is always the `orchestrator` archetype).
- **Freelancers** and manual tasks can set it via the same form.
- **`hera_spawn_worker`** gains an `archetype` param, passed straight through to `CreateAndStart`.
- **Plan-DAG planned nodes** carry archetype on the `hera_roles` row (intent, before any task exists);
  the gater copies it into `CreateAndStart` at materialization.

Storage: a new `tasks.archetype` column is the authoritative resolution key; `ResolveModel` reads
`task.Archetype`. `hera_roles.archetype` holds the planned-node intent and mirrors the live value for
display. Selector default is `(none)` → no profile consult → fall through to project/backend default.
Hera/plan spawns set explicit archetypes; sensible programmatic defaults are coordinator→`orchestrator`,
worker→`code_slice`.

**Canonical archetypes (13):** `brainstorm`, `orchestrator`, `big_build`, `code_slice`, `bug_fix`,
`review`, `security_review`, `synthesis`, `spec_audit`, `ci_loop`, `verify`, `recovery`, `docs`.

- **Why push to the spawn layer:** hera leverages `CreateAndStart`, so putting archetype there makes it
  work everywhere an agent is spawned (Aaron's review note) instead of only on hera roles.
- **Why not infer from role-kind / role-name:** role-kind can't distinguish review/CI/security workers
  (they'd collapse to one model); name-keyword inference is brittle and silently mis-resolves.

### D-BINDING: project→profile name on two surfaces, stored as a name only

The binding is persisted on the project entity in both `config.Project`
(`internal/config/config.go:190`, new `Profile string` TOML field) and the `projects` DB table
(`internal/db/schema.go:31`, new `profile TEXT NOT NULL DEFAULT ''` column; INSERT/scan in
`internal/db/projects.go` extended). Two surfaces write it:

- **Settings pane (persistent):** the project view shows a select-list of on-disk profiles, each
  validated; only valid profiles are selectable. Selecting one persists the project's default binding.
- **Per-spawn modal cycler (override):** the new-agent modal shows the project's bound profile as the
  default and lets the operator override it for that one spawn.

Only the **name** is ever stored; the body stays on disk.

### D-VIEW: plan/DAG view shows archetype + model/effort, and warns on bad profile

The plan/DAG view (and Hera rail) renders, per node, the node's **archetype** and the **resolved
model/effort** applied to it (Aaron's review note), and decorates a node/project with a **warning** when
the bound profile is missing or invalid. These render client-side (argus relaunch needed to observe).

### D-INJECT: env vars at spawn

At spawn, argus exports `ARGUS_PROFILE=<name>`, `ARGUS_ARCHETYPE=<archetype>`, and `ARGUS_MODEL=<model>`
into the agent's environment, mirroring the existing `ARGUS_TASK_ID` export. The in-repo hera/DAG skill
reads these for self-awareness. Env over a prompt-injected block (no token cost, re-queryable,
sandbox-safe) and over an MCP resolve tool (no new tool / round-trip; the MCP `profile_resolve` tool
carrying the full rigor/panel body is the review-fan-out chunk's job). The vars are omitted entirely
when no profile resolves.

### D-SCOPE: one OpenSpec change, executed as a mini-pipeline sub-DAG, targeting `argus/model-tiering`

One OpenSpec change / one capability landing whole (the pieces share the schema + validation types;
splitting ships dead code or cross-PR type deps; argus's archive-in-same-PR rule wants it atomic).
**Execution** (per coord/Aaron) runs as a hera sub-orchestrator `profiles-exec` (base
`argus/model-tiering`) authoring one atomic plan-DAG: implementation fan-out (one node per `tasks.md`
group, parallel where independent) → ralph-review (blocked on all impl) → spec-audit (blocked on
ralph-review). Merge/PR target is `argus/model-tiering`, never master.

## Risks / Trade-offs

- **Sandbox can't read `~/.argus/profiles/` at runtime** → resolution + env-export happen daemon-side at
  spawn (outside the sandbox); the agent reads only the exported env vars or the in-repo
  `.argus/profiles/` inside its worktree.
- **A profile naming a model invalid for the worker's backend** (e.g. `opus` on a codex worker) →
  resolution validates the candidate against the worker's resolved backend models before use; otherwise
  it falls through to project/backend default (same fail-open path), never passing a bad `--model`.
- **`effort`/`window` are display + schema only** → consumers arrive with the skill/review chunk; avoids
  speculative per-backend flag plumbing.
- **New columns `tasks.archetype`, `projects.profile`, `hera_roles.archetype`** → plain nullable/default
  adds; existing rows read as empty/NULL → no archetype / unbound → resolve via defaults / fall-open. No
  migration shim (single-user policy).
- **In-repo `.argus/profiles/` shadowing a global profile of the same name** → precedence is in-repo
  first; `validate` and the Settings select-list report which source a name resolved from.
- **`[panel]` seam churn when 2a lands** → contained to one field's validation + the loader's retained
  block; nothing else depends on panel semantics, so reconciliation is local.

## Migration Plan

- Schema: add `tasks.archetype`, `projects.profile`, and `hera_roles.archetype` columns (direct
  `CREATE TABLE` edits + idempotent add-column-if-missing for existing local DBs; defaults cover
  existing rows; no data migration).
- Config: `config.Project` gains `Profile`; absent = unbound = resolves the `default` profile (or, if no
  `default` profile file exists, fail-open to no model).
- Ship the three seed profiles (`default`, `lean`, `customer_grade`) as documented examples in the repo
  (not auto-installed into `~/.argus/profiles/`); the operator copies/authors their own. Rollback = drop
  columns / remove files; no external state.

## Alternatives considered

- **Single `profiles.toml`** (rejected): deep table paths, awkward in-repo sharing.
- **YAML file-per-profile** (rejected): second config format alongside TOML.
- **Fixed hardcoded model allow-list** (rejected): forces an argus release to add a model; the union
  reuses the per-backend `models` precedent.
- **Archetype on the hera role only** (rejected per Aaron's review): wouldn't cover manual/freelance
  spawns; pushing it to `CreateAndStart` makes it universal.
- **Profiles table in the DB** (rejected by the settled shape): duplicates the canonical on-disk file
  and invites drift.
- **Resolve/execute the panel here** (deferred): the panel runtime belongs with 2a-xvendor; this change
  leaves a typed seam.

## Discovery findings

- `ResolveModel(task, backend)` at `internal/agent/agent.go:94` already has `cfg` reachable via its
  caller chain (the resolver above it reads `cfg.Projects`/`cfg.Backends`), so the profile lookup can be
  threaded without a signature earthquake. `KnownModels`/`BackendModels` (`agent.go:109`/`123`) give the
  built-in allow-list and per-backend override list.
- `config-management` already defines a **projects map** and a **per-backend `models` list** — precedent
  for both the project→profile field and the union allow-list.
- `agent.CreateAndStart` is the single transactional fresh-task chokepoint all spawn paths
  (new-task, hera worker, freelance) route through — the right home for the archetype param.
- `hera_roles` (`schema.go:461`) already carries discriminator columns (`node_kind`, `cancelled_at`) —
  `archetype` follows the same nullable-column pattern; planned nodes are roles with no task.
- The new-task/coord modal renders Backend + Model selectors at `internal/tui/newtaskform.go:1438-1442`;
  Profile + Archetype selectors slot in beside them.
- The Settings view (`internal/tui/settings…`, capability `settings-view`) is the home for the
  persistent project→profile select-list.
- The plan view (`internal/tui/planview`) + Hera rail (`internal/tui/hera/…`) render client-side; the
  per-node archetype/model readout and the missing-profile warning are render concerns there.
- The branch already carries 1a's env-map change (it modified `llm-backends` + `agent-execution` base
  specs and `schema.go`); profile deltas reconcile against those.

## Acceptance criteria

**Profile file loading & inheritance**

- it should load a profile from `~/.argus/profiles/<name>.toml`
- it should prefer an in-repo `.argus/profiles/<name>.toml` over the global file of the same name
- it should resolve `extends` by overlaying the child's fields onto the parent's, recursively
- it should resolve unmapped projects to the `default` profile

**Validation**

- it should reject a profile naming an unknown archetype table
- it should reject an `effort` or `window` value outside its enum
- it should reject a model name not in the union of built-in aliases and configured backend models
- it should accept a model name contributed by a configured backend's `models` list
- it should reject an `extends` chain that contains a cycle
- it should accept a structurally well-formed `[panel]` block without validating its grammar
- it should report which source (in-repo vs global) a profile name resolved from

**Model resolution**

- it should resolve `task.Model` first when set, ignoring the profile
- it should resolve `profile[archetype].model` when the task has no override and a valid bound profile
- it should fall through to the project/backend default when the profile model is not valid for the
  worker's resolved backend
- it should pass no `--model` (CLI default) when the bound profile is missing or invalid
- it should not consult any profile for a task carrying no archetype

**Archetype (spawn layer)**

- it should store an explicit `archetype` passed to `CreateAndStart` on the task
- it should offer an archetype select-list on the new-task and new-worker prompts
- it should not offer an archetype select-list on the new hera coordinator prompt
- it should accept an `archetype` param on `hera_spawn_worker` and persist it to the spawned task
- it should copy a planned plan-DAG node's archetype onto the task it materializes
- it should default the selector to `(none)`, meaning no profile is consulted

**Binding surfaces**

- it should present a validated select-list of on-disk profiles in the Settings project view
- it should allow selecting only profiles that pass validation
- it should persist the selected profile name (not its body) on the project
- it should default the per-spawn modal cycler to the project's bound profile and allow a per-spawn
  override

**Env injection**

- it should export `ARGUS_PROFILE`, `ARGUS_ARCHETYPE`, and `ARGUS_MODEL` when a profile resolves
- it should omit the profile env vars when no profile resolves

**Plan/DAG view surfacing**

- it should show each node's selected archetype and applied model/effort in the plan/DAG view
- it should show a warning in the plan/DAG view when a project points at a missing or invalid profile

**CLI**

- it should expose a `validate` affordance reporting all conformance errors for a named profile

## Open Questions

- None blocking. (Effort/window consumption and the reviewer-panel runtime/grammar are deliberately
  deferred — the latter to `2a-xvendor-review`, which owns the `[panel]` grammar.)
