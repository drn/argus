## Why

Host inspection confirmed Claude's per-user daemon is transient and can be spawned by an Argus agent while serving background sessions across projects. Escalated width repair must preserve that shared daemon and its hosted sessions even when their ancestry leads through the task.

## What Changes

- Exclude Claude daemon processes and their entire subtrees from escalation cleanup.
- Keep ordinary helper/dev-server cleanup and creation-identity guards.
- Log preserved shared roots and publish the changed supervisor stream identity.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `terminal-rendering`: Escalation respects shared Claude daemon ownership.

## Impact

Agent process inspection, descendant selection, supervisor stream surface, regression tests and gotchas. No new dependencies or wire fields.
