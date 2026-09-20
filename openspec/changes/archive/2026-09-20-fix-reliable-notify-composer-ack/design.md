## Context

The notifier previously treated any PTY output after CR as acknowledgment. A recipient can emit output while its composer remains unchanged, so that signal cannot prove the Enter was consumed. The notice-only fast path also assumed Ctrl+U cleared the composer after a successful write syscall.

## Goals / Non-Goals

**Goals:**

- Record a delivery as submitted only after the rendered composer no longer contains the submitted draft.
- Directly replace a stale notice only after a rendered-composer check confirms Ctrl+U cleared it.
- Keep existing retry, queueing, and annotated preservation behavior.

**Non-Goals:**

- Repair existing live recipient drafts or alter a recipient's editor bindings.
- Change message durability, deadlines, or queue ordering.

## Decisions

- Use `ScreenRenderer.InputDraft` against a fresh output tail as the authoritative post-CR acknowledgment. Output activity remains useful to wait for a redraw, but cannot alone acknowledge consumption.
- After Ctrl+U for a notice-only draft, inspect the composer again before writing the replacement. If it is not confirmed empty, route through the stable abandoned-draft annotation path so the old and new notices cannot be glued together.
- Preserve bounded standalone-CR retries; each retry requires a fresh composer-state check.

## Risks / Trade-offs

- [A renderer cannot identify the composer after a CR] → retain the delivery pending rather than infer success from streaming output.
- [A slow redraw delays confirmation] → use the existing bounded acknowledgment windows and retry only CR.

## Migration Plan

Deploy with the daemon normally. Pending deliveries use the stronger check on their next reconcile; no data migration or rollback action is needed.
