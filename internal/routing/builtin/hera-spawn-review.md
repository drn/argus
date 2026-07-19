---
tags: [argus]
audience: [shared]
---
## Panel review orchestration (argus sandboxes)

If `ARGUS_TASK_ID` is unset and `$PWD` is not under `~/.argus/worktrees/`, ignore this section.

When asked for a panel or multi-reviewer pass over a diff — not just a single reviewer loop —
prefer the shipped `hera-spawn-review` skill over hand-rolling sub-agent fan-out. It resolves the
project's diligence-profile `[panel]` via `mcp__argus__profile_resolve`, spawns each configured
finder and lens as an in-session Claude sub-agent injected with its review instruction (defaulting
to `hera-review`, plus `hera-review-test-adversary` when named as a lens), synthesizes every
finder's output into one tagged-finding report with cross-vendor confidence voting, then runs
fix-verification and fixes findings until a round comes back clean.

This chunk runs Fable + Opus in-session finders only — a foreign finder id (e.g. `codex`) is a
reserved grammar slot the skill skips with a loud note rather than silently dropping it.

For a single reviewer instead of a panel, use `hera-review` directly; `hera-spawn-review` is its
hera-aware, multi-reviewer sibling.
