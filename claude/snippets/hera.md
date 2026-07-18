---
tags: [argus]
audience: [shared]
---
## Hera multi-agent coordination (argus sandboxes)

If `ARGUS_TASK_ID` is unset and `$PWD` is not under `~/.argus/worktrees/`, ignore this section.

Being inside an argus sandbox is not, by itself, a reason to use hera — most argus sessions are plain
solo tasks that should stay solo. The rules below apply only once this session already has evidence of
being hera-managed:

- it was **spawned as a hera worker** (its prompt carries the orientation prefix naming the coordinator
  + orchestrator), or
- it already **holds, or is actively creating** (e.g. via `hera_new_orchestrator`), a
  coordinator/freelance binding.

**With that evidence:** coordinating a *team* of agent sessions runs through **hera**'s
`mcp__argus__hera_*` tools — never hand-roll it, and don't use bare `TaskCreate` for coordinated work.

- **Load the `hera` skill in full before coordinating.** The tool schemas alone don't convey the role
  model, messaging, or decision rules.
- **Pick the right tool for the work:**
  - Ephemeral in-session fan-out (research, review, parallel reads that return to you) → Claude's native sub-agents, NOT hera.
  - Work whose unit must be its own argus session (own worktree / PR / sandbox / long-running) → hera workers via `hera_spawn_worker`.
  - Those units have dependencies among them (one needs another's output, or a required ordering)? → a **plan-DAG**: load the `hera-plan` skill and author planned nodes wired by blocking edges. **Internal dependencies are the clean signal to plan a DAG** — decide it yourself when the dependency is obvious; only ask the human when it's genuinely unclear the work even warrants multi-session orchestration.

**No such evidence?** This is a bare argus task the human is driving directly — don't assume hera and
don't self-promote into a coordinator just because the work could be split up. At most, mention hera as
an available option for multi-session work (e.g. "this could be split into a hera team with its own
worktrees/PRs per stage if you want — say so, or I'll keep it in this session") and only act on it if
they opt in.
