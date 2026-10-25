## Context

The tool-call interval includes execution after hook approval. The STEP 0 observer mod was rejected at validation and live load by 2.1.310: first-party `anthropic` telemetry hooks are reserved for built-in plugins. Therefore exact telemetry integration is unavailable and the mod remains unchanged.

The throwaway mod registered `on('telemetry.log', { to: 'anthropic' }, ...)`, read only `event` and `props.toolName`, and always returned `next(e)` unchanged. `claude plugin validate` exited 1; a live `claude -p` run with `--plugin-dir`, Bash `ask`, and `sleep 3` printed the same admission failure before completing the command. The rejection was: "its hooks stand on the telemetry stream anthropic ... which is for the plugins built into the CLI; name the collector on each telemetry hook". Neither `DISABLE_TELEMETRY` nor `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC` was exported in the worker shell. No first-party properties were forwarded or retained.

## Goals / non-goals

- Preserve live bridge busy state and source selection through uncertain permission intervals.
- Corroborate only permission needs-input whose confidence is not exact; missing confidence remains conservative for older reports.
- Keep REST, TUI, web, macOS, push and shadow consumers consistent without new wire fields or settings.
- Non-goal: telemetry tracking. If admitted in a future engine, event correlation would require FIFO by toolName because the first-party properties lack tool_use_id, with heuristic fallback when no telemetry is observed.

## Decisions

- A pure bridge resolver copies state and ANDs uncertain permission input with the raw scraper input. Exact permissions and question/plan state remain authoritative.
- Cache the raw scraper needs-input snapshot under a server mutex for BridgeStates. Do not corroborate from previously overridden runner needs-input, which would feed exact dialog state back into future permissions.
- Compute this tick's needs-input before using the resolved bridge state for idle events. BridgeStates exports resolved states, and TUI set overrides use these directly.
- Shadow compares the same resolved state, retaining the permission diagnostic bucket only for uncertain permission intervals.
- Source logging remains attached to liveness changes, with no permission-specific logs or source transitions.

## Risks / trade-offs

- Scraper lag delays true fallback permission detection; existing settling rules remain intact.
- Snapshots before the first scraper tick conservatively suppress uncertain permission input. Busy/idle still comes from the bridge.
- No telemetry signal is shipped until the engine admits user hooks on that stream.
