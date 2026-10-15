## Context

`BuildCmd` builds every agent's environment from `os.Environ()` via `spawnBaseEnv`, which today strips only `CLAUDE_CONFIG_DIR` and per-account auth overrides. The supervisor (which owns spawning) and the daemon inherit whatever environment they were started from. Starting either from inside Claude Code — `argus daemon restart`, `argus session-supervisor stop` followed by an auto-start, the TUI's skew-modal restart run from a Claude-hosted terminal — leaks that session's runtime markers into every agent.

Observed: Claude Code shows "Transcript saving is off — inherited CLAUDE_CODE_CHILD_SESSION marker · restart with CLAUDE_CODE_FORCE_SESSION_PERSISTENCE=1 to keep future transcripts". The same warning is visible in the committed usage-probe fixture `internal/usagebudget/testdata/usage_100x80.raw`.

## Goals / Non-Goals

**Goals:** agents never inherit a parent Claude Code session's identity or transcript policy; Claude agents always persist transcripts.

**Non-Goals:** scrubbing the daemon/supervisor's own process environment at startup (fixing the spawn boundary covers every agent regardless of how the parent was started); touching user-configured Claude settings variables (e.g. `CLAUDE_CODE_ENABLE_TELEMETRY`, `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS`), which are deliberate preferences, not session markers.

## Decisions

### D1. Strip an explicit denylist of per-session runtime markers

`CLAUDECODE`, `CLAUDE_CODE_CHILD_SESSION`, `CLAUDE_CODE_ENTRYPOINT`, `CLAUDE_CODE_EXECPATH`, `CLAUDE_CODE_SESSION_ID`, `CLAUDE_CODE_SESSION_ATTENDED`, `CLAUDE_CODE_MESSAGING_SOCKET`, `CLAUDE_CODE_MESSAGING_TOKEN`, `CLAUDE_PID`, `CLAUDE_EFFORT`.

These describe the *parent* session: its identity, PID, entrypoint, messaging channel (including a token that must not leak into other agents) and effort level. A prefix strip (`CLAUDE_*`) was rejected because it would also remove user-configured settings variables and backend `env_vars` pass-through. The list lives in one exported slice so the probe and `BuildCmd` share it. Backend `env_vars` mappings are appended after the base env, so an operator who deliberately sets one of these names per backend still wins (same precedent as auth-override stripping).

### D2. Force persistence for Claude agents

Claude-family spawns export `CLAUDE_CODE_FORCE_SESSION_PERSISTENCE=1`. D1 removes the cause; D2 is defense in depth against a marker reaching an agent by a path D1 does not cover (e.g. a future Claude Code marker name). Transcripts are a hard dependency of Argus resume and discovery, so there is no case where an Argus-spawned Claude agent should run without them.

### D3. The usage probe gets the same treatment

`startPTYSession` builds its own environment; it applies the same denylist and flag so the probe is never a child session either.

## Risks / Trade-offs

- **New Claude Code marker names** would not be stripped. Mitigated by D2 for the transcript symptom; the denylist is a single slice to extend.
- **Supervisor restart required to deploy**, which stops running agents. Schedule it at a quiet point; start it from a clean environment.

## Migration Plan

Deploy, then restart the supervisor and daemon from a shell without the markers (`env -u CLAUDECODE -u CLAUDE_CODE_CHILD_SESSION …`, or any non-Claude terminal) at a quiet point. Verify with `ps eww` that a freshly spawned agent has no `CLAUDE_CODE_CHILD_SESSION` and has `CLAUDE_CODE_FORCE_SESSION_PERSISTENCE=1`.

## Open Questions

None.

## Acceptance criteria

- It should not pass any listed parent-session marker to a spawned agent, for every backend.
- It should export `CLAUDE_CODE_FORCE_SESSION_PERSISTENCE=1` to every Claude-family agent.
- It should not export the persistence flag to non-Claude backends.
- It should keep user-configured Claude settings variables in the agent environment.
- It should let a backend `env_vars` mapping set a listed name explicitly.
- It should run the usage probe without the markers and with the persistence flag.
