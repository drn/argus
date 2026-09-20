## Context

`ScreenRenderer.InputDraft` reconstructs the composer from x/vt cells but previously extracted text from the style-free rendered string. Claude Code's empty-composer example prompt is faint-styled, so it became indistinguishable from typed input.

## Goals / Non-Goals

**Goals:**

- Use retained x/vt cell attributes to classify a faint-only composer as empty.
- Continue recognizing normal and wrapped typed drafts.

**Non-Goals:**

- Match placeholder phrasing or alter Claude Code rendering.
- Interpret arbitrary non-faint visual decoration as placeholder text.

## Decisions

- Inspect `SafeEmulator.CellAt` attributes for the draft cells, rather than matching wording. `AttrFaint` is the terminal SGR semantic Claude Code uses for dim text and survives emulator rendering.
- Return an empty draft only when every non-space draft cell is faint; any non-faint character remains authoritative typed content.

## Risks / Trade-offs

- [A user intentionally types entirely faint-styled text] → classify it as placeholder. This is not produced by ordinary typed input and is safer than routinely preserving a phantom draft.

## Migration Plan

No migration is needed. The corrected renderer classification takes effect on the next notifier reconciliation.
