## Why

Claude's five-hour window can reach 96% while routing sees only weekly usage. Backend-only tiers also cannot use an idle personal Claude account, and guidance to pin Claude bypasses backend overflow.

## What changes

- Route on the fuller valid five-hour or seven-day Claude window, preserving each window's freshness and reset validity.
- Add optional tier accounts to TOML, DB persistence, and TUI Settings; resolve and stamp backend/account together.
- Apply account routing when a backend is explicit; constrain coordinators to Claude-capable tiers while allowing account overflow.
- Log routing decisions and invalid account skips through uxlog and slog.
- Reserve explicit Claude guidance for actual Claude-only needs.
- Probe every configured Claude tier account in the background using its CLAUDE_CONFIG_DIR and stripping inherited auth overrides; each account's fresh bridge reading skips its hidden probe.
- Preserve nonblank explicit/inherited account pins while allowing blank parent accounts to route.

## Capabilities

### New capabilities

None.

### Modified capabilities

- `usage-budget-routing`: Independent session/week windows and account-isolated measurements.
- `backend-tier-routing`: Account-aware selection and validation, including constrained selection.
- `agent-execution`: Routed account precedence at creation.
- `hera-coordination`: Claude-only tier selection for coordinator creation and clearer spawn guidance.
- `settings-view`: Editable optional tier accounts.
- `tui-shell`: Fullest valid Claude window in the usage readout.

## Impact

Changes usage parsing, bridge aggregation, routing, task creation, coordinator spawning, DB tier storage, TUI Settings/status, builtin Hera instructions, README Reference, and routing gotchas. No new dependencies or REST endpoints.

## Non-goals

- Named follow-up `web-macos-routing-settings-parity`: web/macOS tier editing and usage readouts remain outside this change; daemon routing applies uniformly to all creation clients.
- Named follow-up `named-codex-usage-probes`: named Codex tiers remain unknown until an account-scoped rollout reader exists; they never borrow the default account's usage.
