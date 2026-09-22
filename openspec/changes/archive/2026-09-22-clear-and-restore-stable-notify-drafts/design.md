## Context

Reliable pane delivery currently recognizes a stable non-notice composer as abandoned, then submits the existing text plus an annotation and the new notice. This protects the draft but makes every recipient parse an avoidable discard instruction. The existing renderer already classifies a faint-only Claude Code placeholder as empty, and that classification must remain authoritative.

## Goals / Non-Goals

**Goals:**

- Deliver the notice alone after the existing non-notice stability gate succeeds.
- Preserve a stable real draft as unsent composer input after an acknowledged notice submission.
- Never capture or restore a faint-only placeholder.
- Never restore over input that appeared during the submit window.
- Retain the existing five-minute deadline and nine-total-Enter-attempt safety bounds.

**Non-Goals:**

- Changing the stability window or unknown-composer fallback.
- Retrying restoration after a skipped or failed restore.
- Changing stale notice-only recovery, which may still use its annotation fallback when Ctrl+U cannot be confirmed.

## Decisions

- Capture the renderer's stable non-notice draft, issue Ctrl+U, and require the existing clear observation before writing the clean notice. A failed clear leaves the delivery pending; appending to an uncleared human draft would violate clean delivery.
- Restore only after the existing post-CR acknowledgment succeeds. The restore is one system-origin text write with no carriage return, so it remains an unsent draft.
- Immediately before restore, re-read the recognizable composer and require it to be empty. If it is unknown or non-empty, skip restoration and emit a WARN diagnostic named `delivery restore skipped: composer state changed`. Preventing new input corruption takes priority over preserving a stale draft.
- Keep the attempt counter exclusively on standalone CR writes. Clear and restore are bounded by the delivery deadline and do not bypass or consume the nine-submit-attempt ceiling.
- Reuse `ScreenRenderer.InputDraft` unchanged. Its faint-only classification returns an empty draft, so placeholders stay on the normal empty-composer path and have no captured state to restore.

## Risks / Trade-offs

- [The composer changes after notice submission] → Re-read it before restore and drop the captured draft instead of writing over new content.
- [Ctrl+U is not consumed] → Do not inject the notice; leave the delivery pending for a later safe reconcile.
- [Restoring can fail after a successful notice] → Log the write failure; the message remains submitted and the stale draft is intentionally not retried.
- [Unacknowledged Enter repeats] → Existing deadline and nine-Enter ceiling continue to bound all CR retries.

## Migration Plan

- Deploy with the existing notifier state model; no data migration is required.
- Monitor daemon logs for `delivery restore skipped` and restore-write failures after rollout.
- Roll back by reverting this change; queued durable messages remain unaffected.

## Open Questions

- None.
