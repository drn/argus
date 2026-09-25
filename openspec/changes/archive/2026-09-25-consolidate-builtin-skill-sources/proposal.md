# Consolidate builtin skill sources

## Why

Argus embeds and session-provisions its Argus-coupled skills from
`internal/skills/builtin/`, but the repository also carries nine same-named
copies under `.agents/skills/`. Those copies are no longer a distribution
mechanism: six are byte-identical, while `argus-resolve-model`, `hera`, and
`hera-plan` have drifted in both directions. They also expose intentionally
session-scoped builtins to ordinary repository sessions and project autocomplete.

Deletion alone would break `hera-spawn-review`, which read review and lens
instructions from the project mirror. The sources must first be reconciled and
review lookup moved to the current child session's native skill catalog.

## What Changes

- Make `internal/skills/builtin/<name>/SKILL.md` the **sole in-repo source** for
  Argus builtin skills. Project-local `.agents/skills/` remains available for
  genuinely project-specific skills, but no builtin is mirrored there.
- Merge the useful newer guidance from the drifted project copies into the
  canonical `hera`, `hera-plan`, and `argus-resolve-model` bodies while retaining
  newer embedded-only behavior such as blocking inbox waits and automatic
  fan-in branch prompts.
- Change `hera-spawn-review` to resolve `review_skill` and lens instructions
  by exact ID from the current child session's native skill catalog /
  catalog-advertised manifest path, rather than assuming a project mirror exists.
  Native source precedence keeps user-owned overrides selectable; a missing or
  unresolved ID still fails loudly before any finder spawns.
- Remove the nine project-local builtin directories from `.agents/skills/`,
  preserving `.agents/skills/update-agent-models/` as the one project-only
  maintenance skill.
- Add regression coverage that prevents an embedded builtin name from being
  mirrored under either project-skill root and prevents `hera-spawn-review` from
  reintroducing a hard-coded project-local instruction path.
- Update repository instructions, README/reference material, and the builtin
  skills gotcha to describe the single-source model and the complete ten-skill
  inventory.

## Non-Goals

- **Do not repair the pre-existing MCP schema drift for `hera_status=failed` or
  plan-node `kind`/`goal`.** The handlers and specifications already support
  those fields, but the advertised `tools/list` schemas omit them. That separate
  MCP-schema alignment is not caused by removing project skill mirrors and would
  require its own behavioral delta and schema tests.

## Capabilities

### New Capabilities

_None._

### Modified Capabilities

- `skill-provisioning`: the embedded builtin tree is the sole in-repo source;
  builtin bodies do not require project-local mirrors.
- `cross-vendor-review`: named review/lens instructions resolve from the current
  session's provisioned skill catalog, independent of repository-local copies.

## Impact

- **Canonical content:** `internal/skills/builtin/{argus-resolve-model,argus-schedule,hera,hera-plan,hera-spawn-review}/SKILL.md`.
- **Deleted mirrors:** the nine Argus/Hera directories under `.agents/skills/`;
  `.claude/skills` is the existing symlink, so no separate deletion is needed.
- **Tests:** focused source-structure and skill-contract tests in
  `internal/skills/builtin_test.go`, plus the existing provisioning suite.
- **Docs:** `AGENTS.md`, `README.md`, `context/knowledge/index.md`,
  `context/knowledge/gotchas/misc.md`, and the retained
  `.agents/skills/update-agent-models/SKILL.md` maintenance checklist.
- **Intended visibility change:** the Argus repository no longer lists builtin
  skills as project-local autocomplete entries or exposes them to ordinary
  manually-started repository sessions. They remain available in every
  Argus-launched child through the existing backend-specific provisioning.
- **No REST endpoint, MCP tool, keybinding, schema, dependency, or static web
  asset changes.** No service-worker version bump is required. OpenSpec files
  remain local documentation only; the quality gate remains `make pre-pr`.
