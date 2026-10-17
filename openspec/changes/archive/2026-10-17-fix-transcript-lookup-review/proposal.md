## Why

Review found that transcript lookup can falsely declare a saved conversation missing when Claude uses a physical worktree path or a backend configuration override. Recovery must preserve those conversations.

## What Changes

- Match transcript lookup to launch configuration and both logical and physical worktree paths.
- Cover inspection failures, recovery identity propagation, and daemon revival with regression tests.
- Add daemon-visible logs and document supervisor upgrade requirements.
- Back off deferred Hera kicks to 60 seconds, recover explicit live revives fresh when unsaved, and preserve resume identity on inspection uncertainty, as directed by the coordinator during review.

## Capabilities

### New Capabilities

### Modified Capabilities

- `agent-execution`: Transcript lookup matches the effective launch directory and worktree identity.

## Impact

Agent, daemon, REST and TUI availability checks; additive RPC identity handling remains unchanged across web and macOS clients.
