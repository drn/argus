## Why

Recent task completions came from two paths that do not reliably express the operator's intent. Agents called `task_complete` after finishing work even when the user's request did not ask to change Argus status; several Codex agents reached the local MCP HTTP endpoint when the tool was absent from their tool list. In the TUI, repeated `s` input advanced a task through `in_review` and into `complete` within about 0.2 seconds. Completion changes the task's workflow state and blocks Enter from reopening it in the task list.

## What Changes

- In the supplied `argus-complete` skill and MCP tool description, require an explicit user request to mark the Argus task Complete. Finishing work, shipping a PR, or ending a response alone do not authorize that status change. If the completion tool is unavailable, the skill stops rather than finding another transport.
- In the TUI Tasks list, advancing from `in_review` to `complete` opens a confirmation. The triggering `s` event never confirms it. Cancel leaves the status unchanged.
- Keep the existing behavior for deliberate completion via MCP, REST, and macOS/web status controls, and for clean session exit. Those are separate triggers; this change addresses the two paths observed in the recent audit.

## Capabilities

### Modified Capabilities

- `skill-provisioning`: the bundled completion skill expresses the explicit-request gate and no-fallback rule.
- `mcp-server`: the `task_complete` tool description reflects the same gate for direct tool users.
- `task-list-view`: completing through status advance requires confirmation.

## Impact

- Bundled agent-facing skill, `task_complete` tool description, TUI status advance and confirmation flow, focused tests, keybinding help text and README Reference.
- No REST wire change. Web and macOS already expose a deliberate status action rather than a repeated single-key advance; their status controls remain available.
