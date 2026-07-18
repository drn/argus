---
tags: [argus]
audience: [shared]
---
## Archetype-based model resolution for native sub-agent dispatch (argus sandboxes)

If `ARGUS_TASK_ID` is unset and `$PWD` is not under `~/.argus/worktrees/`, ignore this section.

Dispatching a sequence of Claude's native sub-agents (the `Agent`/`Task` tool, or a `Workflow`
script's `agent()`) for stages that map to diligence archetypes (a migration stage ~ `code_slice`,
a review pass ~ `review`, a CI-fix loop ~ `ci_loop`)? Load the `resolve-archetype-model` skill
before dispatching — it resolves each stage's model (and, where the mechanism accepts it, effort)
from the project's bound diligence profile via `mcp__argus__profile_resolve`, instead of every
stage silently inheriting the caller's own default model. This is distinct from hera worker spawn,
which already resolves archetypes automatically at spawn time via `hera_spawn_worker`'s `archetype`
param — reach for this skill only for in-context work with no worktree/branch/PR of its own.
