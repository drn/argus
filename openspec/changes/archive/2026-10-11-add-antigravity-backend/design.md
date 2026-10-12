## Context

Per-backend behavior is scattered as boolean branches (`isCodex`, `isPi`, `isOpencode`) through BuildCmd and a dozen call sites, and duplicated as name lists in JS and Swift.

## Decisions

### D1. Two layers: static kind (leaf) + behavioral family (agent)
`internal/backendkind` is import-free of argus internals (config, api, tui and agent can all import it) and holds only static facts: kind, binaries, label, skill prefix. `agent.Family` embeds it and adds behavior, including function-valued fields (capture, resume, permission flags, prelaunch, global inject) that need agent/config/inject imports. `config` cannot import `agent`, which is why the split exists.

### D2. Custom backends get an explicit family
An unrecognized command resolves to `customFamily` (Claude-style session pinning + `--resume`, no Claude flags, no capture). This encodes today's implicit fallthrough instead of relying on `!isCodex && !isPi && ...`.

### D3. Byte-identical commands for existing backends
The refactor preserves every existing BuildCmd output, including flag ordering. The existing exact-string test suite is the guard; no test expectation for claude/codex/pi/opencode changes.

### D4. Antigravity config is global by necessity
agy reads only `$HOME/.gemini/config` — no env override (checked binary strings: only `GEMINI_API_KEY`, `AGY_ACCOUNT`, internal vars). MCP and skills therefore use the same daemon-startup global-injection path opencode's MCP uses, idempotent and preserving unrelated keys. Argus builtin skills self-gate, so their presence in a non-Argus agy session is inert beyond listing.

### D5. Antigravity recaptures every exit
agy can start a new conversation in-session; resuming the newest conversation for the worktree matches Claude's `/clear` handling. Generalizing RefreshResumeSessionID to `Family.RecaptureEveryExit` gives agy the same hera-worker resume correctness Claude has.

### D6. workspace_uris matching
The column is a text list of workspace URIs. Parse as a JSON array when it is one, else split on commas/whitespace; strip `file://`, URL-decode, and compare canonical paths against the canonical worktree. Opened read-only (`mode=ro`) like opencode capture.

## Risks

- Conversation behavior could not be exercised end-to-end without a signed-in Google account; capture is tested against a fixture DB with the verified schema. Verification step in tasks.md.
