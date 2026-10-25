## Why

Permission dialogs often show no (?) in the Hera rail. The bridge reports them, but as `pending-or-running`: the mod cannot tell a dialog waiting for the person from an approved tool that is now executing. So the daemon shows them only when the screen scraper agrees, and the scraper misses dialogs on busy screens (live: review-71 sat on a Bash dialog for minutes with scraper=false, bridge=true; 26 settled disagreements in the permission shadow bucket).

Claude Code draws a `(ctrl+b to run in background)` pill under a foreground tool call while it executes, and raises it to mods as the `ToolProgress` render component carrying the call's `tool_use_id`. A dialog that is still waiting has no pill. That is the missing "now running" signal.

## What changes

- The mod watches `ToolProgress` renders and marks that call as running. A running call never qualifies as a permission dialog, and one that already qualified is cleared.
- Permission needs-input from this mod carries the new confidence `pending` (asked, still pending, never seen running) instead of `pending-or-running`.
- The daemon treats `pending` permission input as authoritative, like `exact`: shown without scraper corroboration. `pending-or-running` and missing confidence (older mods, until their sessions restart) keep today's corroboration.

## Capabilities

### New capabilities

None.

### Modified capabilities

- `argus-bridge`: mod needs-input derivation (running signal, `pending` confidence) and the corroboration rule.

## Impact

Embedded Claude mod (`hooks/tracker.ts`, `hooks/register.ts`, tests), `internal/bridge` confidence constants and `UncertainPermission`, bridge gotchas, and the supervisor spawn surface digest (mod files ship in the supervisor binary). Taking effect needs a supervisor restart and restarted Claude sessions. TUI, web and macOS read the same daemon verdicts, so no client change.
