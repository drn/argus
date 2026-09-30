## Context

Reliable notification normally captures a stable composer draft, verifies Ctrl+U cleared it, submits a clean bracketed notice, and restores the user draft after acknowledgment. If clearing cannot be confirmed, the fallback writes a preservation payload at the live cursor. The cursor can be at either edge of the draft, so positional words such as "preceding" and "following" are not reliable identifiers.

## Goals / Non-Goals

### Goals

- Keep the bracketed notice before the fallback annotation.
- Use wording that is correct regardless of cursor position.
- Continue detecting an already-injected fallback payload so retries replace it instead of growing it.

### Non-Goals

- Change cursor position before Ctrl+U or alter normal capture/restore sequencing.
- Change the clear-confirmation policy.

## Decisions

- Construct fallback text as the current bracketed notice followed by the annotation. The untouched user text remains wherever the cursor placed it, and the annotation refers to it by role rather than direction.
- Use a fixed annotation that says unsubmitted user input in the composer must not be acted on, only the bracketed notice is actionable, and the user should be asked to continue afterward.
- Update stale-payload classification to require a recognized Argus/Hera notice before the annotation after whitespace normalization.

## Risks / Trade-offs

- The fallback remains necessary when the editor does not visibly acknowledge Ctrl+U. Reordering makes retries depend on the revised recognition rule; focused unit coverage protects that coupling.
