---
tags: [argus]
audience: [shared]
---
## Hera multi-agent coordination (argus sandboxes)

If `ARGUS_TASK_ID` is unset and `$PWD` is not under `~/.argus/worktrees/`, ignore this section.

Inside an argus sandbox, multi-agent coordination — bootstrapping an orchestrator, claiming or attaching a worker/freelance role, and messaging other roles — runs through **hera**'s `mcp__argus__hera_*` tools. When you need to spawn and coordinate other agent sessions, run a large multi-session project, or message another role, consult the `hera` skill for the tool surface, decision rules, and worked workflows. Don't hand-roll coordination.
