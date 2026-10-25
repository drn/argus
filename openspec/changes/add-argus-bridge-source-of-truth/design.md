## Context

Phase 1 provides a mod, a Hub, REST ingest/read endpoints and a scraper comparator. The approved proposal calls for bridge precedence with automatic fallback.

## Goals / Non-Goals

**Goals:** authoritative session state, account-isolated limits, direct context/cost measurement, and restart-safe fallback.

**Non-Goals:** phase 3 delivery, bands, toasts, slash commands or replacing the budget nudge. No new web/macOS controls: their existing daemon events receive the authoritative verdicts. In-process TUI remains scraper-based. Raw token-class totals cannot be reconstructed from session.measure and remain unavailable for bridge-only measurements.

Named-account headless fallback (probe per account) is a follow-up. Without a fresh matching bridge measurement, named accounts remain unknown and fail open; the default-account probe is never borrowed.

## Decisions

- Liveness requires a running Claude session, a matching session identity, an observation in the current process generation, and an event younger than 45 seconds. Restart/recycle cannot inherit freshness merely by reusing a transcript id. The bridge read endpoint exposes this same decision.
- Heartbeats carry busy/dialog snapshots so daemon restart can reconstruct state; metadata awaits serialize event creation in hook invocation order, while delivery remains a background single-flight queue. A previously retired boot cannot overwrite its replacement through a delayed post.
- Preserve the scraper computation for fallback and disagreement diagnostics. Override final idle and needs-input sets before publishing events, notifications and UI state. Idle means quiescent (not busy or awaiting input); needs-input remains a separate delivery gate.
- Daemon-connected TUI reads a batched authoritative snapshot off the UI thread. Missing or failed reads clear authority rather than freezing stale verdicts.
- Rate-limit freshness is independent of heartbeat freshness. Select the newest measured weekly window among live sessions on the requested normalized account; never use another account's reading. The existing headless probe is the fallback.
- Persist measurement accounting baselines on bindings so daemon bounces and duplicate posts cannot double-accrue. Cumulative cost increases accrue only their positive delta; a session change or decreased cumulative cost establishes a new counter epoch. Context uses reported tokens and percent. The Stop hook skips transcript accounting only when a live bridge has usable measurements.
- Permission-kind pending calls can continue executing after approval, so they temporarily use the scraper's verdicts. Question and plan kinds remain authoritative. Subagent dialogs are reported by phase 1's corrected mod; subagent turns never change main-loop busy.
- Cost source handoff is conservative: the first transcript after bridge accounting saves its raw-token offset without adding dollars; the first bridge reading after transcript accounting saves its cumulative USD baseline without adding dollars. Subsequent readings accrue normally. This can undercount the transition turn but cannot charge it twice (coordinator-approved tradeoff).

## Risks / Trade-offs

- [Scraper lag] → retain the comparator and all fallback debounce tests.
- [Buffered events from a prior process] → reject mismatched process/session identity.
- [Partial measurements] → do not erase valid fields or suppress fallback for absent data.

## Migration Plan

Deploy atop phase 1 and restart the supervisor for any changed mod wire fields. Disable bridge config to restore scraper authority. Keep both changes unarchived for Aaron's live run.

Verified locally with Claude Code 2.1.310 in tmux: real api.Server, Runner/BuildCmd and --plugin-dir, temporary Argus HOME/data, in-process runner. Question pending reported busy and exact needs-input; answering cleared it in both bridge and task REST state. Measurements reported context tokens/percent, USD and weekly rate limits. Disabling bridge.enabled removed live authority while posts continued; re-enabling restored it. Supervisor/TUI live dogfood remains pending; unit and race tests cover those client paths.

## Open Questions

None; implementation details may refine identity transport without weakening restart isolation.

