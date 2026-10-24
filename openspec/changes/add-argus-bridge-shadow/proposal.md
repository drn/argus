## Why

Almost everything argus knows about a live Claude session (busy/idle, waiting-on-you, context size, plan limits, session id) is inferred by scraping the terminal screen, with a long tail of debounce layers and recorded bugs. A Claude Code mod running inside the session can report the same facts as structured events. Phase 1 of the "argus bridge" (see `docs/argus-bridge-proposal.md`) ships that mod in report-only shadow mode, so argus gets hard numbers on how often screen-reading is wrong before any behavior switches over (phases 2, 3a, 3b build on this).

## What Changes

- New Claude Code mod `argus-bridge`, source in `internal/bridgemod/mod/argus-bridge/`, embedded with `go:embed` and materialized content-addressed under `~/.argus/`. It is inert unless `ARGUS_TASK_ID` is set. It posts structured events (`session.start`, `turn.start`, `turn.complete`, `needs_input`, `cleared`, `session.measure`, `heartbeat`) to the daemon over loopback REST.
- `agent.BuildCmd` adds `--plugin-dir <materialized dir>` for backend families that declare the new `Family.BridgeMod` capability (Claude only), gated by a new `[bridge] enabled` config switch (default ON; phase 1 changes no behavior).
- The daemon writes `~/.argus/api-port` atomically when the REST API binds, so a mod in a session that outlives a daemon bounce can rediscover the port.
- New authenticated endpoint `POST /api/tasks/{id}/bridge/events`; the daemon keeps the latest bridge state per task in memory.
- Shadow comparator: where the daemon computes scraper verdicts (idle watcher, needs-input watcher), it compares them to the bridge state of the same task, logs persistent disagreements via uxlog (`[bridge]` prefix, after a settle window) and keeps per-signal agree/disagree counters, exposed at `GET /api/bridge/shadow`.
- Nothing consumes bridge state to change behavior in this phase.

## Capabilities

### New Capabilities
- `argus-bridge`: the mod, its event schema and fail-open delivery, the daemon-side event ingest and per-task state, and the shadow-mode comparison and counters.

### Modified Capabilities
- `agent-execution`: BuildCmd injects the bridge mod via `--plugin-dir` for families with the capability.
- `rest-api`: bridge event ingest, per-task bridge state, and shadow stats endpoints.
- `daemon-lifecycle`: the daemon publishes the bound API port to `~/.argus/api-port`.
- `idle-detection`: scraper idle and needs-input verdicts are compared against bridge state in shadow mode.
- `config-management`: new `[bridge]` section with an `enabled` switch.

## Impact

- Code: `internal/bridgemod` (embed + materialize), `internal/bridge` (state, shadow comparator, wire types), `internal/agent/backends.go` + `agent.go`, `internal/api` (routes, handlers, idle watcher hook), `internal/daemon/daemon.go` (port file), `internal/config` + `internal/db/config.go`.
- `BuildCmd`'s spawn surface changes, so the supervisor spawn-surface digest is re-recorded (see `context/knowledge/gotchas/ci-gates.md`).
- Docs: README Reference REST table, `context/knowledge/gotchas/bridge.md` + index row, `context/knowledge/backends.md` matrix row.
- No change for Codex and other non-Claude backends. No behavior change anywhere in phase 1.
