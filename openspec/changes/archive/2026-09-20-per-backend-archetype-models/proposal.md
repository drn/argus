## Why

Archetype profiles currently use one Claude-style model alias for every backend. A Codex worker can therefore receive `--model opus` through an explicit Hera override and fail before it starts.

## What Changes

- **BREAKING** Replace each archetype's single `model` field with backend-keyed model entries.
- Resolve an archetype's model for the task's actual backend, preserving fail-open fallback when that backend has no entry.
- Reject profile entries against their named backend's model set rather than a cross-backend union.
- Validate explicit task-model overrides before model flags are injected, so an unsupported override cannot start an invalid CLI command.
- Make `profile_resolve` backend-aware and update its consumers and shipped seed profiles.

## Capabilities

### New Capabilities

- None.

### Modified Capabilities

- `diligence-profiles`: backend-specific profile models, backend-aware resolution and validation, and safe explicit overrides.
- `mcp-server`: backend-aware `profile_resolve` output for profile consumers.
- `hera-coordination`: worker spawns cannot pass an unsupported explicit model to their backend.

## Impact

Changes the profile TOML and `profile_resolve` JSON contract, profile loading/overlay/validation, agent model resolution, three embedded seed profiles, related skills, tests, and the diligence-profiles gotcha documentation.
