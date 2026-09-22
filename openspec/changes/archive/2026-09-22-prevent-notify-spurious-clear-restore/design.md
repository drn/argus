## Context

The bounded unconfirmed-clear fallback prevents deadline loss after three failed checks. A delivery that fails one or two checks can still receive a later transient empty observation and take the clean restore path, writing a false-positive captured frame into the composer.

## Goals / Non-Goals

**Goals:**

- Treat a first failed clear confirmation as permanent evidence that the captured draft is suspect.
- Preserve the existing three-attempt fallback for continuously unconfirmed clears.

**Non-Goals:**

- Altering composer classification or changing the clear-attempt limit.

## Decisions

- Once the per-delivery unconfirmed-clear counter is non-zero, any later clear path submits the existing annotation payload and discards the restoration snapshot.
- Continue issuing the normal Ctrl+U write; only clean submission and restoration are disallowed after tainting.

## Risks / Trade-offs

- [A real draft has one delayed clear observation] → Its original text is not restored, but the queued notice is delivered safely and no arbitrary pane text is written into a live composer.

## Migration Plan

- No migration is required; delivery state is in-memory only.

## Open Questions

- None.
