## Why

Fan-in workers currently start on one blocker branch without learning the other blockers' branch names. They must synchronously ask the coordinator for information the gater already knows, so a busy or recycling coordinator can leave ready work stalled.

## What changes

- Add every fan-in blocker's resolved branch name to the materialized worker's initial prompt.
- Keep the existing single-branch worktree base selection and coordinator fan-in notice unchanged.
- Update the bundled Hera plan guidance to tell fan-in workers to use the supplied branch map rather than ask the coordinator for sibling branches.
- Document the materialization invariant in the orchestration gotchas.

## Capabilities

### New capabilities

None.

### Modified capabilities

- `task-orchestration`: A materialized fan-in worker receives all resolvable blocker branch names in its initial prompt.

## Impact

- `internal/heragater`: prompt construction and gater tests.
- `internal/skills/builtin/hera-plan/SKILL.md`: fan-in operating guidance.
- `context/knowledge/gotchas/orchestration.md`: non-obvious prompt and branch-resolution invariant.
