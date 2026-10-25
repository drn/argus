## Context

Phase 2 supplies authoritative `Server.bridgeState(id, now)`, `Server.BridgeStates()`, task/batch REST reads and the `Daemon.BridgeStates` RPC. Both message families already share `notify.Notifier`. The approved proposal's spike demonstrates idle plugin submission preserves unsent drafts.

## Goals / Non-Goals

**Goals:** use the existing queue, cancellation and five-minute deadline; switch routing per new delivery without restarting sessions; avoid mid-turn or dialog submission; preserve legacy behavior behind either disabled setting.

**Non-Goals:** phase 3b bands/toasts/slash commands; non-Claude mods; moving recycle/rerender safety predicates to bridge authority (named follow-up); web SPA and macOS settings parity for Bridge controls (named follow-up). Keep all phase 1–3 changes unarchived for coordinated review.

## Decisions

- Resolve live authority exclusively through phase 2's server seam. A runtime callback from notify to that seam selects bridge routing; no second heartbeat/liveness calculation. Keep composer and terminal safety predicates unchanged for the PTY route.
- Offer only the active queued delivery. Claim, authorize submission, and acknowledgment operations share notifier serialization with PTY reconciliation. Identity includes launch generation and mod boot, preventing a retired claimant racing a new route. Reading Hera inbox or explicitly acknowledging task mail cancels either route.
- Pull after the main turn completes and on a gentle idle timer. Keep local busy and pending-dialog gates, recheck after asynchronous requests, and submit one message at a time using `$.prompt.submit({text})`, retaining plugin framing. Network work runs outside hook dispatch; hooks remain fail-open.
- Never double-submit: duplicate turns are worse than a missed doorbell because inbox storage is durable (coordinator decision, Hera message 16944). Append a short delivery-ID marker to submitted text. On claim expiry, inspect the resolved transcript JSONL, retrying briefly for async-write lag. Marker found means delivered; readable evidence positively covering the lease window with no marker permits one re-offer, then bounded PTY fallback. Unreadable or incomplete evidence means ambiguous/abandoned automatic submission, with uxlog and slog diagnostics and durable mail left unread. Later passes may resolve it. Session.start reconciles outstanding claims before new pulls. A stale claimant cannot authorize after fallback. Rejected alternatives: blind timeout fallback can double-submit; acknowledgment before submission can silently lose delivery with no evidence trail.
- `bridge.delivery` defaults true, follows config.toml-over-DB precedence, and is inert when `bridge.enabled` is false. Route selection is live; toggling does not restart sessions. Add both switches to a distinct extensible TUI Settings section, using existing toggle/persistence patterns and no new keys.
- Remote TUI toggles map both keys through the existing settings API's bridge partial-update section. Cache the effective response immediately (rather than the requested value), preserving config.toml precedence without waiting for the periodic snapshot refresh. Web/macOS controls remain out of scope.
- For bridge-delivery coordinators, enqueue the existing nudge text through the same shared notifier after the Stop hook's budget, increment and pending-recycle gates. Record last-nudged context at enqueue; the mod submits only while idle. Keep context stamps, request_recycle, pending-recycle behavior and forced recycle threshold intact. Suppress Stop-hook blocking only when the daemon accepts this live bridge-delivery route; otherwise retain the current nudge byte-identically. Rejected alternatives: `prompt.compose` is skipped by the managed `cc-plugin-sec-default` plugin (verified live, coordinator decision 16962); retaining `decision:block` can cause the original idle deadlock.

## Risks / Trade-offs

An absent marker while the original process is live is normally ambiguous: the engine could still hold a queued prompt. Exception (coordinator message 16957): a same-launch/session idle heartbeat after lease expiry plus 200ms flush allowance, together with complete transcript coverage of that window, proves the queue settled without this submission and permits the one re-offer/fallback sequence. Busy/dialog periods do not run the no-poll fallback timer.

- [Submit succeeds but acknowledgment is lost] → fenced authorization plus explicit recovery policy; never assume timeout proves no submission.
- [A bridge dies or settings change with work claimed] → expire unsubmitted claims and reject late authorization; maintain the original deadline.
- [Permission state is coarse] → conservative mod pending-dialog gating, with no terminal checks added to live-bridge delivery.
- [A hung network request stalls a hook] → bounded background single-flight polling, retaining phase 1 fail-open dispatch.
- [Budget POST commits but its response is lost] → let Stop complete and stamp cadence on ambiguous transport/5xx responses; never emit a second blocking nudge. Explicit disabled/rejected replies retain the legacy block. This may miss a budget notice when the POST did not reach the daemon, preserving the coordinator's no-double-submit priority.

## Migration Plan

Ship default on after isolated live checks and all gates. Roll back with `bridge.delivery=false`; this preserves bridge reporting while restoring legacy message delivery and budget nudge. `bridge.enabled=false` disables bridge authority altogether.

## Open Questions

None. The coordinator exported the virtual plugin-authoring skill and approved the transcript recovery boundary and idle budget-delivery alternative.

## Verification

Live tmux checks used an isolated temporary HOME/data directory, real api.Server, agent.NewRunner(nil), real BuildCmd and real Claude Code 2.1.310 with the materialization seam redirected to a temporary mod. Normal delivery started one plugin-framed turn while `UNSENT_DRAFT_3A` remained untouched. With delivery disabled, a subsequent notice was typed through the PTY. Hot-reloading a valid no-op mod stopped heartbeats; after authority expired the next notice used PTY fallback. A second harness running the final mod and a coordinator binding queued a budget notice through the budget endpoint and delivered its marker in an idle plugin turn. Both harnesses were shut down. No live daemon or supervisor was restarted; real sandbox load and supervisor/TUI dogfood remain on the coordinated post-restart list.

Review corrected two concurrency/recovery risks: ack replies lost after server completion must not stall later delivery, and validating/retiring a claim must be atomic with cancellation/re-registration. Native plugin tests drive busy and pending-question gates as well as the pure delivery protocol; transcript tests use temporary files and account-aware path resolution.
