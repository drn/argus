---
tags: [argus]
audience: [shared]
---
## Archetype→model resolution for native sub-agent dispatch (argus sandboxes)

If `ARGUS_TASK_ID` is unset and `$PWD` is not under `~/.argus/worktrees/`, ignore this section.

When dispatching a sequence of in-session sub-agent stages that map to diligence-profile
archetypes (e.g. via the `Agent`/`Task` tool or a `Workflow` script's `agent()`) — as opposed to
spawning a hera worker, which already resolves its model from `hera_spawn_worker`'s `archetype`
param — follow the shipped `resolve-archetype-model` skill instead of silently inheriting your own
default model for every stage. It calls `mcp__argus__profile_resolve` once per pipeline, maps each
archetype to its configured model (falling back to the dispatch mechanism's own default when
unresolved), and substitutes the closest in-session Claude model when an archetype names a foreign
backend that native dispatch has no path to.

This does not apply to hera worker spawn, which already has its own resolution path.
