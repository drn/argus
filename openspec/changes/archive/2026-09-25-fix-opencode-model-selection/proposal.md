## Why

Argus currently injects a resolved OpenCode model as a top-level `--model` flag. OpenCode v2's full interactive TUI rejects that flag (`Unrecognized flag: --model`), so selecting a backend default, per-task model, or profile model prevents the session from launching. The `opencode mini` subcommand does accept `--model`, but it ignores OpenCode's `--auto` flag, which Argus needs for unattended task execution.

OpenCode supports a child-only `OPENCODE_CONFIG_CONTENT` override in both its v1 and v2 configuration surfaces. Argus already uses that channel to add its session-scoped skills path, so the model can be delivered through the same native, per-process mechanism without changing the global OpenCode configuration or the full-TUI launch path.

## What Changes

- Deliver a resolved OpenCode model through the child process's `OPENCODE_CONFIG_CONTENT` `model` key instead of a top-level `--model` argument.
- Merge the model with Argus's existing session-scoped `skills` override while preserving valid inherited inline configuration and unrelated keys.
- Continue honoring an explicit `--model` already present in a configured OpenCode backend command; a hand-edited command wins and no inline model is added.
- Leave model delivery for Claude, Codex, and Pi unchanged.
- Keep malformed inherited inline configuration untouched and allow the OpenCode launch to continue without Argus's model override, matching the existing skills fallback; a valid-but-incompatible `skills` value is preserved and logged while the model still applies.

## Impact

- Affected capabilities: `agent-execution` (backend launch model delivery).
- Affected code: `internal/agent/agent.go`, `internal/agent/nonclaude_context.go`, and their tests; the OpenCode Reference appendix and the OpenCode gotcha entry.
- No schema, REST, or persisted-state change. Model precedence and validation remain in `ResolveModel`.
- All frontends use the same daemon/supervisor command builder, so no client-specific change is required.

## Non-Goals

- Changing the resolution precedence of task, profile, or backend-default models.
- Switching the default backend to `opencode mini` or changing `--auto` behavior.
- Rewriting the user's global `opencode.json`, project configuration, or provider credentials.
- Overriding a model explicitly supplied in a backend command or an explicit `env_vars` mapping for `OPENCODE_CONFIG_CONTENT`.
