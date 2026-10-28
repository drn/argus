## Why

An agent session launched by Argus could not find binaries in `~/.local/bin` (e.g. `sketch`) on the Bash tool PATH, while resumed sessions sometimes could. Spawned sessions inherit the daemon/supervisor's PATH, which depends on how that process was started (launchd plist vs terminal vs older install).

## What Changes

- `BuildCmd` appends `~/.local/bin` to the PATH it passes to every spawned session when it is not already present. Inherited ordering is never changed.

## Capabilities

### Modified Capabilities

- `agent-execution`: adds a spawn-PATH requirement.

## Impact

`internal/agent/spawnpath.go`, `internal/agent/agent.go`. Shell init and Claude Code hooks (`CLAUDE_ENV_FILE`) are handled separately in the dots project.
