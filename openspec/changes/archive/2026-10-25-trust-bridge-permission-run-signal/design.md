## Context

`tool.check` returning `ask` plus a still-pending `tool.call` after the 1.5s grace is the mod's only permission signal; `classic.PermissionRequest` is skipped when the org's managed plugin runs first. That interval covers the dialog AND the approved execution, so the daemon ANDs it with raw scraper input, which misses real dialogs on busy screens.

## Decisions

- **Running evidence comes from the `ToolProgress` render.** In 2.1.310 its one kind is `background_hint`, the `(ctrl+b to run in background)` pill drawn under a foreground call while it runs. The hook marks `e.props.tool_use_id` running and returns `next(e)` unchanged, so drawing is never altered. Transitions it causes are reported in the background; the render path never waits on the network.
- **New confidence `pending` rather than reusing `exact`.** The interval is not exact for tools that show no pill, and a distinct value lets the daemon keep corroborating reports from mods that predate this change (`pending-or-running`, or none) until their sessions restart.
- **Daemon trusts `pending` like `exact`.** Raw input still gates delivery and still feeds the separate permission shadow bucket only for uncertain confidences; `pending` counts under the headline `needs_input` signal.

## Risks / trade-offs

- An approved tool that draws no pill (MCP tools, web fetch, a subagent's call, a second call sharing another call's pill) keeps (?) until its `tool.call` completes. Accepted: a brief false (?) after approving beats a missed dialog.
- The pill appears a moment after execution starts, so (?) can linger for that moment after approval.
- A Bash call that finishes before its pill is drawn clears at `tool.call` post, as today.
