---
name: resolve-archetype-model
description: >-
  Resolve a diligence profile's per-archetype model (and, where the dispatch mechanism accepts it,
  effort) for Claude's NATIVE sub-agent dispatch (the Agent/Task tool, or Workflow's agent()) — as
  opposed to hera worker spawn, which already gets this via internal/agent.ResolveModel. Use when a
  coordinator or pipeline skill dispatches a sequence of in-context sub-agent stages that map to
  archetypes (e.g. a migration stage ~ code_slice, a review pass ~ review, a CI-fix loop ~ ci_loop)
  and wants each stage to run at the project's configured model/effort instead of silently
  inheriting the caller's own default. Generalizes the pattern hera-spawn-review already uses for
  the review archetype's [panel] block to the other twelve archetypes. NOT for hera worker spawn
  (already handled by hera_spawn_worker's archetype param) — this is for work that stays in-session
  with no worktree/branch/PR of its own.
---

# resolve-archetype-model — archetype→model resolution for native sub-agent dispatch

## 1. What this is, and is not

This is the native-dispatch counterpart to hera worker spawn's archetype resolution. A hera worker
gets its model from the project's bound diligence profile automatically (`hera_spawn_worker`'s
`archetype` param drives `internal/agent.ResolveModel` at spawn time). A native sub-agent — spawned
via the `Agent`/`Task` tool, or via a `Workflow` script's `agent()` — has no such path: it silently
runs at whatever model the calling session inherits, regardless of what the project's profile says
that kind of work should run at.

This skill is the convention that closes that gap. It is **not** a new MCP tool — `profile_resolve`
already returns everything needed in one call. It is not specific to review panels —
`hera-spawn-review` already does exactly this pattern for the `review` archetype's `[panel]` block;
this skill generalizes it to any archetype a pipeline's stages map to.

## 2. Resolve once per pipeline

Call `mcp__argus__profile_resolve(cwd=$PWD)` **exactly once** per pipeline or session, not once per
stage — the response already carries every archetype's entry. Build a local map from it:

```
archetypes = resolved.archetype   # {} if resolved.resolved == false
```

The response shape (per `internal/mcp/profiles.go`):

```json
{"resolved": true|false, "name": "...", "source": "...",
 "archetype": {"code_slice": {"model": "sonnet", "effort": ""}, "review": {"model": "opus", "effort": ""}, ...},
 "rigor": {...}, "panel": {...}, "errors": [...]}
```

Field names are lowercase/snake_case (`model`, `effort`, `window`) — read them by exact key, no
case-normalization needed.

## 3. Fail-open fallback

Never treat a miss as an error — always fall back to the dispatch mechanism's own default model:

- **`resolved: false`** (no profile, invalid profile, malformed `[panel]`) — every stage in the
  pipeline dispatches with no model override.
- **A specific archetype absent from `archetype`, or present with an empty `model`** — only that
  stage falls back; other stages whose archetypes ARE present still get their resolved model. A
  profile author may legitimately leave some archetypes unset.

## 4. The in-session model gate (mandatory before dispatch)

A profile's archetype `model` is validated against the union of **every configured backend's**
models — it may legitimately name a codex model, not just a Claude one. Claude's native sub-agent
dispatch only runs **in-session Claude models**. Before threading a resolved model into a dispatch
call, check it against the same four values `hera-spawn-review` already checks finders against
(mirrors `internal/review.knownInSessionModels`):

```
knownInSession = {"opus", "sonnet", "haiku", "fable"}
```

- **Model is one of these four** → forward it: `Agent(model=<resolved>)` or, for a `Workflow`
  script, `agent(prompt, {model: <resolved>})`.
- **Model is anything else** (a foreign backend's model name) → do **not** forward it. Dispatch with
  no model override and emit a loud, visible note — e.g. `[resolve-archetype-model] archetype
  "code_slice" resolved to a non-in-session model ("gpt-5-codex") — dispatching with no model
  override.` Never silently drop the mismatch and never pass the value through hoping it works;
  an invalid `model` value on the `Agent` tool is a hard error, not a graceful fallback.

## 5. Effort — only where the mechanism accepts it

An archetype's `effort` field (`low`/`medium`/`high`) is a real, validated part of the profile, but
whether it can be *applied* depends entirely on the dispatch mechanism:

- **Claude's built-in `Agent`/`Task` tool has no effort parameter as of this writing.** Check the
  tool's current schema before assuming otherwise — if it gains one later, thread `effort=` the same
  way as `model=`, gated the same way. Until then, effort is unusable here: omit it, and don't imply
  in a report that it was applied.
- **`Workflow`'s `agent()` accepts `opts.effort`** (`'low'|'medium'|'high'|'xhigh'|'max'` — a strict
  superset of a profile's three-value enum). When dispatching through a `Workflow` script, thread the
  resolved `effort` straight into `opts.effort` — no gate needed beyond "non-empty."

This mirrors the already-documented Fable-effort gotcha in `hera-spawn-review` (§12): a real
capability gap in the current tooling, not a design choice — don't let a report claim effort was
honored by a mechanism that has no way to honor it.

## 6. Worked example

```
resolved = profile_resolve(cwd=$PWD)
models = resolved.archetype if resolved.resolved else {}
knownInSession = {"opus", "sonnet", "haiku", "fable"}

def modelFor(archetype):
    entry = models.get(archetype, {})
    m = entry.get("model", "")
    if m and m not in knownInSession:
        note(f'[resolve-archetype-model] archetype "{archetype}" resolved to a non-in-session '
             f'model ("{m}") — dispatching with no model override.')
        return None
    return m or None

# Agent tool (no effort parameter available):
Agent(prompt=migration_prompt, model=modelFor("code_slice"))          # e.g. "sonnet", or omitted
Agent(prompt=review_prompt,    model=modelFor("review"))              # e.g. "opus", or omitted

# Workflow script's agent() (effort IS available):
entry = models.get("ci_loop", {})
await agent(ci_fix_prompt, {
    model: modelFor("ci_loop"),
    effort: entry.get("effort") or undefined,   # omit rather than pass an empty string
})
```

## 7. Gotchas

- **One `profile_resolve` call per pipeline, not per stage.** The whole point of returning every
  archetype's entry in one response is to avoid N round-trips for an N-stage pipeline.
- **The in-session gate is not optional.** Skipping it means an archetype tuned for a codex worker
  can silently break (or worse, silently misbehave) a native `Agent` call the first time a profile
  author points that archetype at a foreign-backend model.
- **Don't claim effort was applied when it wasn't.** The `Agent` tool's lack of an effort parameter
  is a real, current limitation — state it plainly in any report, the same way `hera-spawn-review`
  documents its Fable-effort gap rather than silently ignoring it.
- **This skill does not modify `hera`, `hera-plan`, `hera-review`, or `hera-spawn-review`.** It is a
  standalone reference; `hera-spawn-review` is prior art for this same pattern (already resolves
  `profile_resolve`'s `panel` block and gates finders the same way), not something this skill wraps
  or depends on.
