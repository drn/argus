## Context

PR #990 added capture → verified clear → clean notice → guarded restore for a stable abandoned draft. PR #991 added a bounded fallback and a permanent taint for a clear that never confirms. Neither covers this case: a clear that confirms cleanly, every single time, because the "draft" being preserved is the notifier's own prior restore. `restoreCapturedDraft` writes the captured content back into the composer; that write re-seeds `deliveryInput`'s stability check, so the next delivery to the same task observes it as a brand-new abandoned draft and repeats the whole cycle. Ground truth: task 1790033812279653000 (an idle/complete coordinator) logged six "delivery draft restored" events within 47 minutes, the last confirmed clear on the very first attempt with zero unconfirmed retries — proof the loop does not depend on clear-confirmation timing at all.

## Goals / Non-Goals

**Goals:**

- Stop a restored draft from being captured and restored again by a later, unrelated delivery to the same task.
- Never permanently block a real human draft: the breaker must only suppress the one re-observation immediately caused by the notifier's own write.

**Non-Goals:**

- Changing composer classification, the stability window, or the unconfirmed-clear taint from PR #991.
- Deduplicating restores across different content — only an exact (whitespace-normalized) match is suppressed.

## Decisions

- Track `lastRestoredDraft` per taskID (not per delivery) on `Notifier`, set whenever `restoreCapturedDraft` succeeds. It must outlive the delivery that produced it because the recapture happens on a *different* deliveryID.
- When `deliveryInput` reaches the stable-draft decision, compare the observed draft against the stored marker using the same whitespace-compacted comparison `composerContainsText` already uses, so soft-wrap differences can't defeat it.
- On a match: route through the same clear-and-submit-without-restore handling already used for a recognized notifier notice, rather than inventing a new payload path.
- Consume the marker unconditionally the first time it is checked, regardless of match outcome, so it can only ever suppress the one re-observation directly downstream of a restore.

## Risks / Trade-offs

- [A human retypes the exact same words the notifier just restored, before any other stable-draft observation happens for that task] → That one retype is treated as self-restored content and not preserved across the next notice. Accepted: the marker is one-shot and per-task, so this is a narrow window, and the content itself is never lost — it's cleared and the queued notice still delivers; only the (already-typed, still-visible-until-Ctrl+U) draft preservation is skipped once.

## Migration Plan

- No migration is required; delivery state is in-memory only.

## Open Questions

- None.
