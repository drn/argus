---
tags: [argus]
audience: [shared]
---
## Hera multi-agent coordination (argus sandboxes)

If `ARGUS_TASK_ID` is unset and `$PWD` is not under `~/.argus/worktrees/`, ignore this section.

Inside an argus sandbox, coordinating a *team* of agent sessions runs through **hera**'s `mcp__argus__hera_*` tools — never hand-roll it, and don't use bare `TaskCreate` for coordinated work.

- **If you are spawned as a hera worker, or you hold (or are creating) a coordinator binding: load the `hera` skill in full before coordinating.** The tool schemas alone don't convey the role model, messaging, or decision rules.
- **Pick the right tool for the work:**
  - Ephemeral in-session fan-out (research, review, parallel reads that return to you) → Claude's native sub-agents, NOT hera.
  - Work whose unit must be its own argus session (own worktree / PR / sandbox / long-running) → hera workers via `hera_spawn_worker`.
  - Those units have dependencies among them (one needs another's output, or a required ordering)? → a **plan-DAG**: load the `hera-plan` skill and author planned nodes wired by blocking edges. **Internal dependencies are the clean signal to plan a DAG** — decide it yourself when the dependency is obvious; only ask the human when it's genuinely unclear the work even warrants multi-session orchestration.
