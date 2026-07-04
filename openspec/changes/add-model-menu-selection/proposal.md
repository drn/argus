## Why

`diligence-profiles` maps each archetype to one fixed `(model, effort)` pair, and `effort` has never
actually been applied to a spawn (validated and displayed, never injected). A single fixed pair can't
express that a strong model at low effort can beat a weaker model at high effort for the same job — and
picking the tier at profile-authoring time puts the decision where the job's actual context (blast radius,
ambiguity, verifiability) is *not* yet known. Letting an archetype carry an ordered menu of candidate
pairs, picked by the spawner at spawn time within a bounded ceiling, fixes both: it expresses the
non-monotone model×effort grid, and it puts the final call where the context actually is.

## What Changes

- `profiles.Archetype` gains an optional `menu` field: an ordered list of `{model, effort}` pairs,
  mutually exclusive with the existing scalar `model`/`effort` fields on the same archetype.
- `profiles.ValidEfforts` widens from 3 levels (`low`/`medium`/`high`) to the real 5-level `claude --effort`
  enum (`low`/`medium`/`high`/`xhigh`/`max`). No shipped profile sets an effort value today, so this
  widening has no live migration to perform.
- `BuildCmd` injects a resolved effort per-backend for the first time: `--effort <level>` for claude,
  `-c model_reasoning_effort=<level>` for codex, no injection for pi/unknown/custom.
- `agent.ResolveModel` resolves model and effort together as a pair, and applies menu-membership
  governance: an explicit spawner-picked pair is validated against a resolved menu (off-menu picks are
  substituted with the cheapest entry, logged, never a hard failure); no archetype, no valid profile, or a
  scalar archetype leaves the existing unconditional override behavior fully untouched.
- `model.Task`, `agent.CreateInput`, `tasks`, and `hera_roles` all gain an `Effort` field/column, mirroring
  the existing `Model`/`Archetype`/`Profile` threading.
- `hera_spawn_worker`/`task_create` gain an optional `effort` param.
- New `hera_retier` MCP tool: a coordinator retiers a live, bound claude-backend worker by writing
  `/model`/`/effort` into its PTY through the existing idle-gated delivery primitive, re-validated against
  the same menu envelope. Non-claude-backend targets get an explicit unsupported error.
- The in-repo hera skill documents the spawner selection convention (default cheapest, climb only when
  justified, log a one-line rationale) and the non-blocking worker check-in convention for menu-resolved
  spawns.
- Seed profiles gain a `menu`-shaped example archetype (`code_slice` is the natural candidate — difficulty
  genuinely varies there).

## Capabilities

### New Capabilities

(none — this extends the shipped `diligence-profiles` capability and its existing collaborators)

### Modified Capabilities

- `diligence-profiles`: archetype schema gains an ordered menu form; effort enum widens to 5 levels;
  model+effort resolve together as a pair; menu-membership governance at resolution time; seed profiles
  gain a menu example.
- `agent-execution`: `BuildCmd` injects a resolved effort per-backend; `CreateInput`/`CreateAndStart`
  thread `Effort` the same way they thread `Model`/`Archetype`/`Profile`.
- `data-persistence`: `tasks.effort`, `hera_roles.effort` columns (idempotent `ADD COLUMN`, no migration
  shim).
- `hera-coordination`: `hera_spawn_worker` gains an `effort` param with menu-envelope validation at spawn;
  new `hera_retier` tool for live mid-flight retiering of a bound worker.

## Impact

- Code: `internal/profiles/{profiles,load,validate}.go`, `internal/agent/{agent,create}.go`,
  `internal/model/task.go`, `internal/db/{schema,hera}.go`, `internal/mcp/hera.go`,
  `internal/tui/hera_tiering.go` (call-site update for `ResolveModel`'s new return shape),
  `internal/hera/service.go` (reused, not modified, for `hera_retier` delivery).
- Docs: in-repo hera skill (spawner selection + worker check-in conventions), README profile examples,
  `context/knowledge/gotchas/misc.md` diligence-profiles section.
- Dependency: the worker-side check-in calls `mcp__argus__profile_resolve`, built by a sibling in-flight
  worker (`2a-xvendor-review`) on this same branch — confirmed not yet landed on this worktree as of design
  time; implementation must confirm/rebase before wiring that call.
- No DB migration shim needed (single-user breaking-changes policy; all schema changes are additive
  `ADD COLUMN`s with safe defaults).
