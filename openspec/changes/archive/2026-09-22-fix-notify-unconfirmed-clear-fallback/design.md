## Context

The stable-draft sequence introduced clean delivery by capturing a draft, confirming Ctrl+U cleared it, submitting the notice, and restoring the draft. Its failed-clear branch currently remains pending indefinitely. Some static scrollback frames are classified as stable composer content but cannot respond to Ctrl+U, making that branch a delivery-loss loop.

## Goals / Non-Goals

**Goals:**

- Deliver a queued notice after a small, bounded number of unconfirmed stable-draft clear attempts.
- Retain the clean restore path for drafts that confirmably clear.
- Reuse the existing annotated-preservation payload when fallback is necessary.

**Non-Goals:**

- Changing composer recognition, draft stability timing, or placeholder classification.
- Altering stale notice-only recovery or the total Enter-attempt ceiling.

## Decisions

- Store an unconfirmed stable-clear counter on each delivery, alongside the existing total Enter counter, so the bound survives reconcile ticks.
- Permit two clean-clear attempts. On the third unconfirmed attempt, use the existing `captured draft + annotation + notice` payload and proceed through normal submission acknowledgment.
- Treat fallback content as notifier-generated stale content on later retries through the existing annotation recognizer, preventing annotation growth.

## Risks / Trade-offs

- [A real draft clears slightly late] → Two complete confirm windows retain the clean path before fallback.
- [A false-positive composer frame is not editable] → The third attempt submits the annotated payload rather than allowing deadline loss.
- [Fallback submission remains unacknowledged] → Existing standalone-Enter and deadline safety bounds remain in force.

## Migration Plan

- No migration is needed; delivery retry state exists only in memory.
- Monitor the new fallback diagnostic after rollout.
- Revert this change to restore the prior behavior if needed.

## Open Questions

- None.
