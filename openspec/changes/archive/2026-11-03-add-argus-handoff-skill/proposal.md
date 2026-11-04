## Why

The `handoff` skill (context handoff to another agent thread, saved to the Argus KB and delivered via a new Argus task) lives only in the author's dotfiles. It drives `mcp__argus__kb_ingest`, `mcp__argus__task_create`, and `ARGUS_TASK_ID`, so it is argus-coupled and belongs in the embedded builtin set.

## What changes

- Add `internal/skills/builtin/argus-handoff/SKILL.md`, migrated from the dotfiles skill with the name and self-references renamed to `argus-handoff` per the builtin naming convention.
- Update the embedded-set enumeration in the base spec, README, and the builtin tests.

## Capabilities

### Modified capabilities

- `skill-provisioning`: embedded builtin set gains `argus-handoff`.

## Impact

No code changes; the set is derived by iterating the embedded tree. The dotfiles copy is left in place.
