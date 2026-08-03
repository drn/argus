## Context

`hera_revive` (`internal/mcp/hera.go:toolHeraRevive`, add-hera-revive) already gives a coordinator a pull/on-demand way to inspect one bound role's live session state and, based on it, restart a dead session, kick a stuck one, or leave it alone — via the shared, PTY-free-testable `internal/hera.ReviveRole` decision function. `hera_send` (`toolHeraSend`) resolves an explicit `to` recipient and hands off to `s.heraSvc.Send` with no session-liveness awareness at all. The gap this closes is purely "call the existing primitive automatically at the right moment in `hera_send`" — no new gating logic, no new MCP tool, no change to `ReviveRole` itself.

## Goals / Non-Goals

**Goals:**

- A coordinator's `hera_send` to an explicit, different recipient transparently revives that recipient first, using the exact same outcome set and gating order `hera_revive` already uses.
- The revive attempt can never block, delay meaningfully, or fail the message send. Every failure mode (no live binding, lookup error, revive error, reviver not wired) is a silent or log-only skip.
- The coordinator can tell, from the `hera_send` response alone, whether the recipient was dead/stuck/fine — without a second round-trip.

**Non-Goals:**

- No change to `internal/hera.ReviveRole`, its outcomes, or its gating order — this change is a new CALL SITE only.
- No auto-revive for the worker/freelance default-to-coordinator send path. A worker/freelance's default recipient is "the active coordinator" — a live coordinator is already `ReviveRole`'s own `skipped_coordinator_live` case, so attempting it would almost always no-op; more importantly, only a coordinator has the authority to revive a role it coordinates (`hera_revive` itself is coordinator-only), and a worker/freelance sender is not the coordinator of its own coordinator.
- No auto-revive when the recipient equals the caller's own role (self-send is already rejected by `heraSvc.Send` on other grounds; the revive attempt is skipped before that rejection is even reached).
- No behavior change to `hera_revive` itself, and no removal of the standalone tool — this is additive.

## Decisions

**D1 — Gate on caller kind + explicit `to` + non-self, mirroring `hera_revive`'s own authority check.** `hera_revive` rejects non-coordinator callers outright (`caller.role.Kind != db.HeraKindCoordinator`). Auto-revive-on-send reuses the same authority boundary rather than inventing a softer one: only `caller.role.Kind == db.HeraKindCoordinator` triggers an attempt, and only when the recipient was resolved via an explicit `to` (the coordinator explicitly named a role it coordinates) — never the worker/freelance default-route path, which resolves a coordinator, not a role the sender coordinates. `toRole.ID != caller.role.ID` guards the degenerate self-target case, matching `hera_revive`'s explicit own-role rejection (here expressed as a skip rather than an error, since `hera_send` self-sends are already invalid on other grounds and should fail with the existing self-send error, not a revive-related one).

**D2 — Placement: after recipient resolution, before `s.heraSvc.Send`, never blocking the send.** The attempt sits strictly between resolving `toRole` and calling `Send`. Every exit from the attempt — no live binding, a lookup error, a revive error, `s.heraRevive == nil` — falls through to the unconditional `Send` call. This is a deliberate asymmetry from `hera_revive` (a standalone tool where a revive failure IS the whole point and must surface as an error): here, revive is a courtesy side-effect of send, and send's own success/failure semantics must stay exactly as they are today.

**D3 — Reuse `heraReviveOutcomeMessage` and the same log line verbatim; no new outcome vocabulary.** The `hera_send` response's `- **revive**: <outcome>` line and the `slog.Info("[hera] revive", ...)` call use the identical rendering/logging helpers `toolHeraRevive` already has, so an auto-triggered revive is indistinguishable in logs and in outcome vocabulary from a manual one. This also means no new documentation of outcome semantics is needed beyond a cross-reference to the existing `hera_revive` requirement.

**D4 — Error handling granularity: `ErrHeraNotFound` is the expected/common case (Info/Debug at most), any other lookup or revive error is a Warn.** A recipient with no live binding (planned node, never spawned, ended) is not a bug — it's the everyday case of messaging a role that hasn't materialized yet or has wound down. Logging it above Debug/Info would spam the daemon log on every ordinary send to such a role. A different lookup error, or a `heraRevive` call error, is unexpected and worth a Warn — mirroring how `toolHeraSend`'s own status-apply soft-fail (D1 in make-hera-plan-living) already logs a Warn on failure while proceeding.

## Risks / Trade-offs

- **[Risk] A coordinator sending many messages to the same already-fine recipient pays a `HeraLiveBindingByRole` lookup (and, when there IS a live binding, a full `ReviveRole` gate evaluation — `IsAlive`/`IsIdle`/`BlockedOnPrompt`/`HasPendingRestart`) on every single send.** → Mitigation: these are the same checks `hera_revive` already performs synchronously per call, and `hera_send` already does comparable per-call DB work (role resolution, rate-limit check, inbox-cap check). No new I/O class is introduced; this is not expected to be a meaningful cost at hera's message volumes.
- **[Risk] A coordinator that intentionally messages a role it does NOT want woken (e.g. deliberately leaving a paused role parked) now wakes it as a side effect of sending.** → Mitigation: this is the intended behavior per the mission — the whole point is removing the need for a separate `hera_revive` call before messaging a sleeping child. `ReviveRole`'s own gating already protects the cases that matter (a live coordinator, a busy role, a role blocked on a prompt are all left untouched) — the only case actually revived-by-side-effect is a role that was dead or genuinely stuck, which is exactly the case a coordinator sending it a message wants alive anyway.
- **[Risk] Silent skip on `ErrHeraNotFound` could mask a genuine naming/resolution bug (e.g. a role that SHOULD have a live binding but doesn't due to a bug elsewhere).** → Mitigation: this is unchanged risk surface — `hera_send` already tolerates sending to a role with no live binding today (the message is durably stored regardless; only best-effort doorbell delivery is affected), so auto-revive attempting and silently skipping adds no new failure mode beyond what already exists.

## Open Questions

None — the mission brief fixes the design; see the brief's explicit numbered "Behavior to add" list for the exact call sequence.
