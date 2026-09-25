## Why

Argus provisioned embedded skills into the user's normal `~/.codex/skills` directory. That made Argus-only workflows visible in every Codex session, even outside Argus. Claude already scopes its embedded skills to Argus launches through `--add-dir` pointing at an Argus-managed workspace.

## What Changes

- Give Argus-launched Codex sessions an isolated `CODEX_HOME` under `~/.local/share/argus/codex-home`, where the embedded skills are materialized.
- Link the user's existing Codex config, authentication, session files, system skills, and other installed skills into that home. Keep SQLite state in its normal location for session capture and resume.
- Leave ordinary Codex sessions' skill directory untouched. Preserve colliding user skills and protect linked system skills.
- Allow sandboxed Codex processes to write to the isolated home.

## Capabilities

### Modified Capabilities

- `skill-provisioning`: isolated Codex skill materialization and preservation of ordinary Codex skills.
- `agent-execution`: child-only `CODEX_HOME` override and SQLite state continuity.
- `sandbox-execution`: narrow write access to the isolated home.

## Impact

`internal/skills/builtin.go`, `internal/agent/agent.go`, `internal/agent/sandbox.go`, their tests, and the supervisor spawn surface version. The user's normal Codex home remains the location for existing config, authentication, and SQLite state.
