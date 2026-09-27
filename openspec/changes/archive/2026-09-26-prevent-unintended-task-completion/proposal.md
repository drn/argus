## Why

Recent task completions came from agents calling `task_complete` after finishing work even when the user's request did not ask to change Argus status. Several Codex agents reached the local MCP HTTP endpoint when the tool was absent from their tool list. Completion changes the task's workflow state and blocks Enter from reopening it in the task list.

## What Changes

- In the supplied `argus-complete` skill and MCP tool description, require an explicit user request to mark the Argus task Complete. Finishing work, shipping a PR, or ending a response alone do not authorize that status change. If the completion tool is unavailable, the skill stops rather than finding another transport.
- Keep the existing behavior for deliberate completion via the TUI `s` key, MCP, REST, and macOS/web status controls, and for clean session exit.

## Capabilities

### Modified Capabilities

- `skill-provisioning`: the bundled completion skill expresses the explicit-request gate and no-fallback rule.
- `mcp-server`: the `task_complete` tool description reflects the same gate for direct tool users.

## Impact

- Bundled agent-facing skill, `task_complete` tool description, focused tests, and README Reference.
- No REST wire or frontend behavior change.
