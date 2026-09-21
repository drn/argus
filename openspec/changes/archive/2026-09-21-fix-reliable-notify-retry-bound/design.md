## Context

`ScreenRenderer.InputDraft` joins terminal rows with newlines. A doorbell is injected as one logical line, so a notice that visually wraps cannot satisfy a literal substring check. The current code also skips both composer and output-based acknowledgment when a composer is known but the injected text cannot be observed. Hera sends with default options, which apply the five-minute deadline; that is a time backstop but still permits many repeated submissions.

## Goals / Non-Goals

**Goals:**

- Treat visual terminal wrapping as formatting rather than a semantic change when verifying an injected notice.
- Preserve a safe acknowledgment path when a recognized composer cannot prove the injection.
- Ensure a delivery cannot submit Enter indefinitely even if future detection logic regresses.
- Prevent retries from recursively appending the abandoned-draft annotation.

**Non-Goals:**

- Change Hera message durability, queue order, or the default deadline.
- Infer a composer-state acknowledgment from unrelated output when a submitted draft was observed successfully.

## Decisions

- Compare composer and injected text after removing Unicode whitespace. This directly models terminal soft wraps at all widths without duplicating terminal reflow rules. Literal text remains the source for PTY writes.
- Use output advancement only when no submitted composer snapshot is available, whether the composer is unknown or known-but-unobservable. Once a draft is observed, retain the stronger composer-consumption requirement to avoid the previous false-positive acknowledgment.
- Count every standalone Enter write on the delivery and stop after nine total attempts. Nine permits three full existing retry windows while limiting an unknown failure to a small, finite number of possible repeated agent actions. The five-minute deadline remains a separate wall-clock backstop.
- Classify a draft containing the fixed abandoned-draft annotation followed by an Argus/Hera notice as notifier-generated stale content. A retry then clears/replaces it instead of appending another annotation.

## Risks / Trade-offs

- [Whitespace normalization could equate text differing only in whitespace] → it is used only to prove that notifier-injected notice text appeared in a renderer that adds soft-wrap whitespace; the submitted draft snapshot remains exact for post-Enter change detection.

- [Output fallback can be a weaker signal] → it is used only when no composer snapshot can establish a stronger signal, and the total-attempt ceiling limits repeated exposure if that fallback cannot succeed.

- [An annotated draft may contain abandoned user content] → it was already deemed stable and abandoned before annotation; clearing it on a retry avoids unbounded growth of a payload the notifier itself appended.

## Migration Plan

Deploy normally. Existing pending deliveries pick up the new verification and attempt ceiling on their next reconcile. Rollback is the prior daemon binary; no persisted schema or data migration is involved.
