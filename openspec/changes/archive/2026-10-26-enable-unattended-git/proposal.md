## Why

Agent commits inherit personal 1Password signing settings and stall when Aaron cannot approve. Agent test fixtures also inherit those settings, making local tests depend on an interactive signing agent.

## What changes

- Add config.toml-only `git.agent_commit_signing`: `inherit` by default, or opt-in `unsigned` for spawned agent process trees.
- Inject only child-process command-scope Git configuration, preserving other configuration and leaving global/shared repository files unchanged.
- Isolate agent Git test fixtures from developer global/system configuration before fixture commits.
- Document restart/recycle requirements and unsigned feature-commit provenance.

## Capabilities

### New capabilities

- `agent-git-policy`: Explicit agent-session signing policy and hermetic agent Git fixtures.

### Modified capabilities

None.

## Impact

- Argus configuration/file overlay, common agent launcher, supervisor spawn surface, tests, README Reference, and gotchas.
- Frontend parity: common launcher applies equally to TUI/web/macOS. Settings controls in all three clients are deferred under the named follow-up `agent-git-policy-settings-controls`; this setting is config.toml-only with no REST change.
- Approved by Aaron through coordinator message 17669. GitHub authentication and async push/fetch are already shipped separately in iris PR #17 (`anutron/iris`, b2026c0), outside this change. Argus's 30-second plugin deadline remains unchanged. Aaron owns any iris credential opt-in; this worker does not apply it.
