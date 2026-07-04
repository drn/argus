## Context

`diligence-profiles` (archived as `add-diligence-profiles`) shipped `internal/profiles`: named TOML
profiles map each of 13 canonical archetypes to a single, fixed `{model, effort, window}` triple.
Resolution runs daemon-side in `agent.ResolveModel` (called from `BuildCmd`), fails open on any miss
(missing archetype, invalid profile, model not valid for the backend), and injects `--model` — never a
hard error. The resolved pick is exported as `ARGUS_PROFILE`/`ARGUS_ARCHETYPE`/`ARGUS_MODEL` and rendered
in the TUI plan view (`AppliedModel`/`AppliedEffort`).

Two things are true today that this change addresses:

1. **`effort` is schema-only.** `profiles.Archetype.Effort` is parsed, validated (`low`/`medium`/`high`),
   and displayed (`hera_tiering.go` → `RoleView.AppliedEffort`) — but `BuildCmd` never injects it. There is
   no effort knob anywhere in argus today (confirmed by grep: no `--effort` injection site exists).
2. **A single fixed triple can't express a non-monotone model×effort grid.** A strong model at low effort
   can beat a weaker model at high effort for a given job; a static table can't say "pick whichever of
   these fits, but don't go above this ceiling." An *ordered menu* of pairs can.

This change (Aaron's "bounded model-menu selection" idea) lets an archetype's value be either the
existing single pair (unchanged) or an ordered menu of `{model, effort}` pairs, cheapest-first, and moves
the final tier choice to spawn time, where the job's actual context lives — while keeping the menu a
governance *ceiling*, not a suggestion a worker can talk itself out of.

**Confirmed during discovery** (not assumed):

- `claude --effort <level>` is a real CLI flag accepting `low`/`medium`/`high`/`xhigh`/`max` (5 levels) —
  wider than `profiles.ValidEfforts`'s current 3. Source: `claude --help`.
- `claude` also exposes live in-session slash commands `/model` and `/effort` (confirmed via strings in
  the installed CLI binary: `"/effort medium"`, `"/effort"`, `"/model, /effort"`). A running session's tier
  can be changed without a restart — but only by something writing those keystrokes into its PTY from
  *outside* the process; a model cannot invoke a slash command on itself from within its own tool-call
  turn (slash commands are intercepted client-side from literal terminal input, not from assistant output).
- `codex` has no dedicated `--effort` flag; the closest equivalent is `-c model_reasoning_effort=<level>`
  (a generic config override), and no live retier command is confirmed for it.
- `mcp__argus__profile_resolve` (built by sibling worker `2a-xvendor-review` on this same branch,
  `argus/model-tiering`) is the tool a running agent will use to read a profile body, including a menu
  array — not yet present on this worktree as of design time (`grep` came back empty).

## Goals / Non-Goals

**Goals:**

- An archetype's TOML value may be a single pair (today, unchanged) or an ordered `menu` of pairs.
- Actually wire effort to a spawn: widen the enum to 5 levels and inject it per-backend (`--effort` for
  claude; `-c model_reasoning_effort=<level>` for codex).
- The **spawner** picks — direct `hera_spawn_worker`/`task_create` calls and plan-DAG gater materialization
  both default to the menu's cheapest entry; an explicit spawner override is validated as a menu member.
- The menu is a **bound envelope**: an off-menu explicit pick is corrected to the cheapest entry and
  logged — never silently honored, never a hard spawn failure (consistent with the existing fail-open
  philosophy).
- No profile, no archetype, or a scalar (non-menu) archetype ⇒ **no gating at all** — the existing
  unconditional `task.Model`/`task.Effort` override behavior is untouched in every case that isn't a
  resolved menu.
- A spawned worker whose archetype resolved to a menu announces its (cheapest, default) pick and the full
  menu to its coordinator via `hera_send`, then proceeds immediately — **non-blocking**.
- A coordinator MAY retier a live, bound worker via a new `hera_retier` tool, which writes `/model` (and,
  if changed, `/effort`) into the worker's PTY through the existing idle-gated single-writer delivery
  primitive (`internal/hera/service.go`) — re-validated against the same envelope.
- Coordinator/spawner selection guidance ("default cheapest; climb only when blast radius/ambiguity/low
  verifiability justify it; log a one-line rationale") lives in the in-repo hera skill.

**Non-Goals:**

- The `[panel]` opaque reviewer-block seam — untouched, owned by `2a-xvendor-review`.
- An automatic job-difficulty classifier. This change builds the plumbing, the bounds, and the logging;
  the judgment of *which* menu entry to pick stays with the coordinator (and, secondarily, the worker's
  self-reported signal in its check-in message).
- Live retier for codex/pi/custom backends — no live slash-command equivalent is confirmed for them.
  `hera_retier` targets claude-backend tasks only in v1; calling it against a non-claude backend returns an
  explicit unsupported error (never a silent no-op) so the coordinator knows to act some other way (e.g.
  `hera_send` the worker directly).
- A blocking check-in handshake. The worker never waits for a reply before starting real work.
- Per-menu-entry `window` overrides. `window` (200k/1m context) stays an archetype-level field regardless
  of whether `menu` is set — it doesn't vary with the difficulty-driven model/effort pick the menu exists
  to express, and doubling the struct for it isn't justified.

## Decisions

### D1 — TOML shape: an explicit `menu` sub-field, mutually exclusive with the scalar fields

```toml
# scalar (unchanged) — one fixed pair
[archetype.docs]
model = "haiku"

# menu (new) — ordered, cheapest first
[archetype.code_slice]
menu = [
  { model = "sonnet", effort = "high" },
  { model = "opus",   effort = "low" },
]
```

`Archetype` gains `Menu []MenuOption` (`MenuOption{Model, Effort string}`). `menu` and the archetype's own
top-level `model`/`effort` are mutually exclusive — `Validate` rejects an archetype table that sets both
(ambiguous authoring) as a conformance error, alongside the existing "unknown archetype"/"invalid
effort"/"unknown model" errors, in the same aggregated-error-list style. A `menu` with fewer than 2 entries
is also rejected (a 1-entry "menu" is just the scalar form, authored badly). `window` stays a top-level
Archetype field either way (see Non-Goals).

**Alternative considered:** array-of-tables (`[[archetype.code_slice]]` repeated blocks) reads as more
idiomatic TOML but needs a custom table-vs-array decode step and complicates the "is this archetype a
menu" check at every call site. An explicit `menu = [...]` field keeps `Profile.Archetype` a plain
`map[string]Archetype` with one new field — decided against array-of-tables for this simplicity.

**Alternative considered:** always a list (no scalar/menu duality) — rejected; it forces a breaking
rewrite of every existing profile file's syntax, including the 3 shipped seeds, for no benefit to
archetypes that never vary.

Menu **ordering is an authoring convention, not a machine-checked invariant** — there's no absolute cost
oracle across arbitrary configured-backend models, so "cheapest first" can't be validated beyond "at least
2 well-formed entries." Documented in the README/seed comments; a badly-ordered menu just makes the
default pick wrong in practice, not a crash (see Risks).

### D2 — Effort enum widens to 5 levels, matching `claude --effort`

`profiles.ValidEfforts` becomes `low`/`medium`/`high`/`xhigh`/`max`. This is the real enum the `claude` CLI
accepts (confirmed above) — keeping the old 3-level enum would permanently forfeit claude's two highest
reasoning tiers as a selectable option, defeating the point of wiring the knob at all.

### D3 — Per-backend effort injection in `BuildCmd`, mirroring `--model`

Claude backends: inject `--effort <level>` (skipped if the command already names `--effort`, mirroring the
existing `hasModelFlag`/`hasPermissionFlags` "hand-edited command wins" precedent — add `hasEffortFlag`).

Codex backends: inject `-c model_reasoning_effort=<level>`. Codex's exact accepted effort values are not
confirmed by `codex --help` (it only documents the generic `-c key=value` override mechanism) — this is
verified for real during the Stage 1 tests, and if codex's enum differs from the 5-level claude enum, a
translation/clamp step is added then (tracked as an Open Question below, not a blocker to designing the
claude path now).

pi/unknown/custom backends: no effort injection in v1 (no confirmed flag or override mechanism) — mirrors
today's scoping of `IsClaudeBackend`-gated flag injection for permission-mode.

### D4 — Effort threads through the same fields Model/Archetype/Profile already use

- `model.Task` gains `Effort string` (`json:"effort,omitempty"`), same shape as `Model`.
- `agent.CreateInput` gains `Effort`.
- New DB columns: `tasks.effort TEXT NOT NULL DEFAULT ''`, `hera_roles.effort TEXT` — idempotent
  `ALTER TABLE ... ADD COLUMN`, same pattern as the three columns the base capability already added.
- `hera_spawn_worker` / `task_create` MCP tools gain an optional `effort` param mirroring `model`.

### D5 — `ResolveModel` resolves model and effort together, as a pair

Model and effort must move together (especially for a menu pick — "opus:low" is a pair, not two
independent choices). `agent.ResolveModel`'s signature changes to also return the resolved effort, and
`ResolvedProfile` gains an `Effort` field. Precedence for **each of** model and effort independently:
`task.{Model,Effort}` override → profile's resolved pick for the archetype → project/backend default → ""
(no flag) — but when the archetype resolves to a **menu**, "the profile's resolved pick" is determined by
D6 below, not a flat per-field lookup.

Call sites (`BuildCmd`, `hera_tiering.go`'s `resolveHeraTier`) are updated together — a small, closed set,
both already touched by the base capability's chunk 3.

**Alternative considered:** a parallel `ResolveEffort` function, symmetric with `ResolveModel`. Rejected —
it re-splits a pair that must be validated and picked together for the menu case, recreating the exact
non-monotone-grid problem this change exists to solve.

### D6 — Governance: menu-membership enforcement, scoped tightly to when a menu actually exists

Enforcement triggers **only** when all three hold: the task carries an archetype; a profile resolves
(loads + validates) for the effective profile name; and that archetype's value in the resolved profile
`len(Menu) > 0`. Any other case (no archetype, invalid/missing profile, or a scalar archetype) is
**ungated** — `task.Model`/`task.Effort` overrides behave exactly as they do today, unconditionally.

Within a resolved menu:

- **Both** `task.Model` and `task.Effort` explicitly set (a full spawner-picked pair) → validate the pair
  is a menu member. Match → honor it. No match → substitute the cheapest (first) entry, `uxlog` a
  `[profiles]` warning naming the rejected pair and the substitution (mirrors the existing
  "not valid for backend; falling through to default" line) — never a hard spawn failure.
- Only **one** of `task.Model`/`task.Effort` set (a partial override) → the explicit field is honored
  as-is, no membership check (today's plain override behavior); the *unset* field defaults to the cheapest
  menu entry's value for that field — not a search for "an entry matching the given field," which would
  invent fuzzy matching semantics this design doesn't need. A coordinator that wants an enforced pair uses
  the full-override form above.
- **Neither** set → default to the cheapest (first) menu entry. This single code path covers both a
  direct `hera_spawn_worker` call with no override *and* plan-DAG gater materialization (which has no live
  coordinator making a real-time decision) — no special-casing needed between the two spawn paths.

### D7 — Non-blocking worker check-in, a hera-skill convention (not new MCP surface)

A worker whose archetype resolved to a menu (checked via `profile_resolve`, not a new env var — see
below) sends its coordinator a `hera_send` early — "operating at `{model}:{effort}` (cheapest option);
menu is `[...]`; confirm or retier" — then proceeds with real work immediately. It does not poll or block
waiting for a reply. The coordinator may act at any later point via `hera_retier` (D8) if it disagrees.
This is documented as a required convention in the in-repo hera skill for any menu-resolved spawn, not
code-enforced — the same class of soft enforcement as other hera process conventions (e.g. "status is
required on every send").

The worker determines "was my archetype a menu" by calling `profile_resolve` itself (2a's tool) rather
than a new env-var marker — this keeps "menu or not" single-sourced from the one place that already reads
the full profile body, instead of duplicating that signal into a second channel. `ARGUS_EFFORT` is still
added to the existing `ARGUS_PROFILE`/`ARGUS_ARCHETYPE`/`ARGUS_MODEL` env-export trio (now a quartet,
exported together or not at all, same as today) so a worker always knows its *own* resolved pick without
an extra round-trip, even before calling `profile_resolve` for the full menu.

### D8 — `hera_retier`: a new coordinator-only MCP tool for live mid-flight retiering

New tool under the existing native `hera_*` surface (`internal/mcp/hera.go`), coordinator-only (mirrors
`hera_send`'s coordinator-must-supply-`to` pattern): `hera_retier(cwd, orchestrator, role, model, effort)`.
Re-resolves the target task's archetype/profile live (not cached) and validates the requested pair against
the same menu envelope as D6 (reject/clamp to cheapest on violation, logged the same way). On a claude-
backend target: writes `/model <model>\n`, then — only if effort is actually changing — `/effort
<level>\n`, into the target's PTY via the **existing** idle-gated single-writer delivery primitive in
`internal/hera/service.go` (the same mechanism `hera_send` already uses for reliable message delivery) —
no new write path, no new busy/idle race to design. On a non-claude-backend target: returns an explicit
"retier not supported for backend %q" error rather than silently doing nothing.

## Risks / Trade-offs

- **Menu order isn't mechanically cost-verified** → validation only checks well-formedness (≥2 entries,
  each individually valid), not that entry 1 is actually cheaper than entry 2. Mitigation: seed profile
  examples model correct ordering; README documents the convention as author responsibility.
- **Codex's reasoning-effort config key/enum may not match claude's 5-level enum** → verified for real
  during Stage 1 tests; a mismatch gets a translation/clamp step then, not designed blind now.
- **`ResolveModel`'s signature change ripples to its call sites** → small, closed set (`BuildCmd`,
  `hera_tiering.go`), both already touched by the base capability — updated together in one stage.
- **`hera_retier` writes into a worker's PTY out-of-band** → reuses the *existing* idle-gated
  single-writer delivery primitive rather than a new write path, so it inherits the same
  never-inject-into-a-busy-session guarantee `hera_send` already relies on.
- **A worker skipping the check-in convention** → soft enforcement only (hera skill documentation, not
  code). Mitigation: the coordinator's hera skill guidance treats a missing check-in on a menu-resolved
  spawn as a smell to flag, same as other soft process conventions.

## Migration Plan

Entirely additive — no migration shim needed (repo's single-user breaking-changes policy). New TOML field
(`menu`), new DB columns (`tasks.effort`, `hera_roles.effort`, both idempotent `ADD COLUMN`), new optional
MCP params (`effort`), one new MCP tool (`hera_retier`). Existing scalar-only profiles and existing spawns
keep working byte-identical to today — `Menu` absent is the entire existing code path, untouched.

Build on `argus/model-tiering` (this worktree's base branch). Before implementing D7/D8's worker-side
`profile_resolve` call, confirm `2a-xvendor-review`'s `profile_resolve` tool has landed on that branch
(rebase to pick it up) or explicitly sequence with the coordinator — do not implement against a tool
signature not yet visible in code.

## Open Questions

- Codex's exact `model_reasoning_effort` accepted values (its docs don't enumerate them the way
  `claude --help` does) — resolved empirically during the Stage 1/injection tests.
- Whether `hera_retier` should extend to pi backends if/when pi exposes an equivalent live command —
  explicitly future work, not blocking this change.

## Future Work

- **`hera_plan_node`/`hera_plan`'s per-node schema has no `effort` param, unlike `hera_spawn_worker`.**
  This change (D4) gives `hera_spawn_worker` both an optional `model` and an optional `effort` param.
  `hera_plan_node`/`hera_plan` (`internal/mcp/hera_plan.go`) already expose an `archetype` param for a
  planned node but have no equivalent `effort` param — an asymmetry flagged by `2a-persist` during
  implementation. This is intentionally **not** addressed here: proposal.md and the hera-coordination
  delta scope this change to `hera_spawn_worker`'s effort param only, and a planned node's effort today
  always resolves per the Menu-based governance defaults (D6) — the cheapest menu entry, or an explicit
  `hera_retier` after the node materializes into a live worker. A future change can close this gap by
  adding `effort` to the plan-node schema if a coordinator ever needs to pre-specify a planned node's
  effort at authoring time, before it has a live binding to retier.

## Acceptance criteria

**Schema (D1):**
- it should accept an archetype authored as a single scalar pair (unchanged behavior)
- it should accept an archetype authored as an ordered `menu` of 2+ pairs
- it should reject an archetype that sets both a scalar `model`/`effort` and a `menu`
- it should reject a `menu` with fewer than 2 entries
- it should validate each menu entry's model/effort exactly as the scalar path does today

**Effort knob (D2/D3/D4):**
- it should accept `low`/`medium`/`high`/`xhigh`/`max` as valid effort values
- it should inject `--effort <level>` for claude-backend spawns when a level resolves
- it should inject `-c model_reasoning_effort=<level>` for codex-backend spawns when a level resolves
- it should inject no effort flag for pi/unknown/custom backends
- it should skip injection when the backend command already names an effort flag/override
- it should persist a task's resolved effort in the `tasks.effort` column
- it should mirror archetype/effort onto `hera_roles.effort` for planned nodes

**Resolution + governance (D5/D6):**
- it should resolve model and effort together as a single paired outcome
- it should leave `task.Model`/`task.Effort` overrides fully ungated when no archetype, no valid profile,
  or a scalar (non-menu) archetype is in play
- it should honor an explicit full (model, effort) override that matches a menu entry
- it should substitute the cheapest menu entry (logged) when an explicit full override does not match any
  menu entry
- it should treat a partial (model-only or effort-only) override as today's plain override, with no
  menu-membership check
- it should default to the cheapest menu entry when neither model nor effort is explicitly set, for both
  direct spawns and plan-DAG gater materialization

**Worker check-in (D7):**
- it should export `ARGUS_EFFORT` alongside the existing `ARGUS_PROFILE`/`ARGUS_ARCHETYPE`/`ARGUS_MODEL`
  trio, together or not at all
- it should NOT block a worker's start on any coordinator reply

**Live retier (D8):**
- it should let a coordinator request a live retier of a bound worker role by model+effort
- it should validate the requested pair against the same menu envelope as spawn-time, substituting the
  cheapest entry (logged) on a violation
- it should deliver the retier via the existing idle-gated single-writer PTY delivery primitive, never a
  new write path
- it should return an explicit unsupported error for a non-claude-backend target, never a silent no-op
