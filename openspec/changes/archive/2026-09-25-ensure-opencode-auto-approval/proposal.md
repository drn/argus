## Why

Argus launches OpenCode without `--auto`, so permission rules that resolve to `ask` can pause an agent even though Argus already isolates it in a worktree and applies its configured process sandbox. Changing only the seeded backend command would not affect backend commands already stored in SQLite.

## What Changes

- Add OpenCode's `--auto` flag when building every recognized OpenCode command, including new and resumed sessions and existing configured backend commands.
- Keep an existing `--auto` flag from being duplicated.
- Preserve OpenCode's explicit `deny` rules; `--auto` only approves requests that would otherwise ask.
- Leave non-OpenCode commands unchanged.

## Capabilities

### Modified Capabilities

- `agent-execution`: OpenCode launch commands use auto approval.
- `config-management`: the seeded OpenCode command stays bare while launch-time auto approval is documented separately.

## Impact

Changes `agent.BuildCmd`, its command-building tests, and the OpenCode integration note. All three frontends reach the same daemon launch path; no REST field or client change is needed.

## Non-Goals

- Changing the stored backend command, OpenCode's config file, or Argus's sandbox policy.
- Overriding explicit OpenCode permission denials.
