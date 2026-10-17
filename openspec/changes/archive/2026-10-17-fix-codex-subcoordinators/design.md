# Design

- **Guard input.** The calling task's stamped `task.Backend` (set at creation by `CreateAndStart`) is
  resolved through `agent.ResolveBackend` and the command tested with `agent.IsClaudeBackend`. Reject
  only on a positively identified non-Claude command. Fail-open when `task.Backend` is empty, config is
  unavailable, or the backend cannot be resolved (legacy rows, bare tests) — a false reject strands a
  legitimate coordinator, a false accept is the status quo.
- **No override in the MCP path.** An agent-supplied flag would be the same discoverability trap; the
  operator route is the TUI form, which does not pass through this handler.
- **Ordering.** The check runs after task resolution and before the existing coordinator-self-promotion
  guard / orchestrator creation, so a rejection never leaves an orphan orchestrator.
- **Logging.** `slog` (the daemon's real logger; uxlog is a no-op there).
- **Subcoord timing (accurate description).** A subcoord node with no blockers is ready immediately and
  materializes on the gater's next poll tick; with blockers, when all reach role-status `done`.
- **Drift test.** Decode-struct json tags are reflected against the advertised schema properties.
