## Why

Hera plan-DAG workers already get per-archetype model selection: `agent.ResolveModel` resolves `task.Archetype` against the project's bound diligence profile at spawn time. Claude's native sub-agent dispatch (the `Agent`/`Task` tool, used for in-context pipeline stages that don't need their own worktree/branch/PR — a migration stage, a review pass, a CI-fix loop) has no equivalent path: a coordinator or skill dispatching a sequence of native sub-agents mapped to archetypes gets no per-stage model selection and silently falls back to the caller's own default model for every stage, regardless of what the project's profile says that archetype should run at.

`mcp__argus__profile_resolve` already resolves the bound profile from any `cwd`/task — it is not hera-specific — and the `hera-spawn-review` skill already proves the underlying pattern works (it resolves `profile_resolve`'s `panel` block and threads `model=` into `Agent()` calls for review finders). But that pattern is hand-rolled once, scoped only to the `review` archetype's `[panel]`, and undocumented as a general convention the other twelve archetypes could reuse.

While confirming the archetype-entry JSON is directly usable as "model + effort for archetype X," a live call to `profile_resolve` turned up a real wire-format bug: `profiles.Archetype`/`profiles.Rigor` have no `json` struct tags, so the tool emits PascalCase keys (`"Model"`, `"Effort"`, `"ReviewPasses"`) instead of the lowercase/snake_case (`model`, `effort`, `review_passes`) the base `diligence-profiles` spec already documents. A Go-side round-trip test masks this (`encoding/json` unmarshal matches keys case-insensitively), but any non-Go consumer reading the raw JSON — exactly the native-dispatch use case this change targets — hits the wrong casing.

## What Changes

- **Fix `profiles.Archetype` / `profiles.Rigor` JSON field casing** — add `json` tags so `profile_resolve` emits the documented lowercase/snake_case keys (`model`, `effort`, `window`, `review_passes`, `gating`, `security_spot_check`) instead of the current PascalCase. No known consumer depends on the current casing (`hera-spawn-review` only reads the untyped `panel` map, never `archetype`/`rigor`).
- **NEW `archetype-native-dispatch` capability** — a documented convention, generalizing the pattern `hera-spawn-review` already uses, for resolving an archetype's model (and, where the dispatch mechanism accepts it, effort) for Claude's native sub-agent dispatch: call `profile_resolve` once per pipeline, build an archetype→`{model, effort}` map from the returned JSON, and thread `model=` into each `Agent()`/`agent()` call — falling back cleanly (no override, caller's own default) when the profile doesn't resolve, when a specific archetype key is absent or empty, or when the resolved model isn't one of the four values native in-session dispatch accepts (`opus`/`sonnet`/`haiku`/`fable`, mirroring `internal/review.knownInSessionModels`). Effort is threaded only where the dispatch mechanism accepts an effort parameter (documented as a real, current gap for the built-in `Agent` tool, which has no effort parameter as of this change).
- **Ship a small reusable skill** (`.claude/skills/resolve-archetype-model/SKILL.md`) that any coordinator or pipeline skill loads before dispatching a sequence of native sub-agents mapped to archetypes, so the convention has one authoritative, testable home instead of being re-derived ad hoc per skill (as `hera-spawn-review` already had to).
- **Document the pattern** in `context/knowledge/gotchas/orchestration.md` and add `profile_resolve` (currently undocumented there) plus the new skill to the README's MCP tools reference.

Non-breaking in practice (the casing fix corrects an unreleased/unused wire detail; no external consumer is known to depend on the current PascalCase). Lands on `argus/model-tiering` (this workstream's long-lived integration branch, not yet merged to `master`), matching every sibling chunk in this effort.

## Capabilities

### New Capabilities

- `archetype-native-dispatch`: the convention (and shipped skill) for resolving a diligence profile's per-archetype model/effort for Claude's native sub-agent dispatch (the `Agent`/`Task` tool and `Workflow`'s `agent()`), as distinct from hera worker spawn.

### Modified Capabilities

- `diligence-profiles`: fixes the `Agent-facing profile resolution` requirement's JSON field-naming contract so archetype/rigor entries actually use the lowercase/snake_case keys the requirement already documents.

## Impact

- **argus-Go**: `internal/profiles/profiles.go` (add `json` tags to `Archetype`/`Rigor`); `internal/mcp/profiles_test.go` (strengthen the existing passthrough test to assert on raw wire casing, not just case-insensitive unmarshal).
- **Skills** (argus repo `.claude/skills/`): new `resolve-archetype-model` skill. Does not modify `hera`, `hera-plan`, or `hera-spawn-review`.
- **Docs**: `context/knowledge/gotchas/orchestration.md` (new bullet), `README.md` (MCP tools table gains a `profile_resolve` entry; the diligence-profiles reference section cross-references the new skill).
- **Not touched**: `internal/agent` (hera worker spawn resolution is unchanged — this change only extends the *native-dispatch* consumer path), `internal/review` (panel grammar unchanged).
