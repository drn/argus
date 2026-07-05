## Why

Hera coordinators are long-lived — unlike a disposable worker, a coordinator persists across an entire multi-stage orchestration and personally accumulates every token it reads, delegates, or relays along the way. In practice this means coordinators routinely grow into hundreds of thousands of tokens of carried context before anyone notices, and there is currently no way to know how bloated a coordinator has gotten, no habits pushing it to stay lean, and no way to reset a bloated or wedged one without losing its place — bounce-recovery was deliberately not ported from the old external hera plugin into native argus hera.

## What Changes

- A new `argus hera-report` subcommand, registered as a global Claude Code `Stop` hook, stamps a live `context_size` value into `task_meta` on every turn for coordinator-role sessions, and repeats a "reach a safe seam, call for a reboot" nudge once a configurable budget is crossed.
- A new `HeraConfig.CoordinatorContextBudget` config field (`config.toml`, default `200000`).
- `hera_status` gains two optional parameters, `handoff_note` and `request_recycle`, so a coordinator can record a short distilled handoff note and signal recycle intent in the same call it already makes routinely.
- A new `recycle_coord` primitive: kills a coordinator's session and restarts it on the *same* task/worktree/branch/binding, seeded with its original mission, current plan-DAG state, and any handoff note — all injected directly into the new session's opening prompt, no tool calls required to reconstruct context. Reachable two ways: self-service (coordinator-requested, daemon waits for genuine idle) and a human-forced rail keybinding with a confirm modal (immediate, for a coordinator that's actually wedged and can't ask for anything itself).
- The coordinator spawn orientation (`HeraCoordinatorOrientation`) and the shared `.claude/skills/hera/SKILL.md` gain a small discipline spec: default to a small behavioral footprint, low default reasoning effort (escalate for real judgment calls), a sharpened delegation rule (native sub-agents for investigation, `hera_spawn_worker` only for work needing its own worktree/branch/PR — "delegate with prejudice, but don't be dumb about it"), pointers not payloads in messages, and a distillate-harvest pass before winding down.
- **Explicitly rejected, documented in design.md so it isn't re-litigated:** a `PreToolUse` hook bridging a worker's `AskUserQuestion` calls into hera messaging. Not actionable (the coordinator's response doesn't depend on why a worker went quiet) and would go stale the moment a human resolves the prompt directly.
- **No changes** to `role_status`'s five values or to the `hera_messages` schema — a proposed richer status enum (`escalated`/`review`/`shipped`) was evaluated and rejected as mostly redundant with existing `done`/`ready_to_close` behavior; the one genuinely new concept (`escalated`, carrying `decision_fork`/`impasse`) is encoded as a `tldr` convention, not a schema change.

## Capabilities

### New Capabilities

- `coordinator-context-management`: the context-budget Stop hook and `context_size` signal, the coordinator-discipline spawn orientation, and the `recycle_coord` primitive (both trigger paths and the seed-prompt assembly).

### Modified Capabilities

- `hera-coordination`: `hera_status` gains optional `handoff_note` and `request_recycle` parameters.
- `hera-view`: a new rail keybinding + confirm modal on a coordinator row to force an immediate recycle.
- `config-management`: `HeraConfig` gains `coordinator_context_budget`.

## Impact

- **New code:** `cmd/argus/hera_report.go` (or similar) for the `hera-report` subcommand; a daemon-side recycle mechanism (task kill/restart + seed-prompt assembly); a rail keybinding + confirm modal.
- **Modified code:** `internal/config/config.go` (`HeraConfig`), `internal/mcp/hera.go` (`hera_status` params), `internal/agent/hera_spawn.go` (`HeraCoordinatorOrientation`), `.claude/skills/hera/SKILL.md`.
- **Modified data:** new `task_meta` keys under the existing `hera` namespace (`context_size`, `handoff_note`, a pending-recycle flag) — no schema migration, uses the existing sidecar table.
- **External dependency:** a one-time manual step for the user — registering the `Stop` hook in their global `~/.claude/settings.json` (argus cannot write to that file on the user's behalf).
- **No breaking changes.** Everything is additive; existing coordinators and workers are unaffected until they next recycle or until the config field is set.
