## Context

The Board currently clips subjects and wraps bodies at arbitrary rune boundaries. The inbox already has a display-column word wrapper with control stripping.

## Goals / Non-Goals

Goals: readable expanded items, visible unread markers, preserved inbox behavior and cursor-follow scrolling.

Non-goals: general markdown rendering, async work or REST changes. Web and macOS Board views remain the parked coordinator-board parity follow-up; neither currently has a Board view.

## Decisions

- Export the existing wrapper from widget and retain inbox sanitization at its call sites.
- Build display lines before scrolling so multi-line headers contribute to selectedStart and offset bounds.
- Parse paired bold markers before wrapping and carry their styles into painted cells. Only bullet prefixes receive extra indentation; unknown markdown stays literal.
- Keep header continuations bold and reverse only the selected first line. Reserve unread marker columns on the first header line.

## Risks / Trade-offs

- Narrow panes can be smaller than a header prefix or wide rune: clamp indentation and rely on cell-aware clipping to protect borders.
- Markdown support is intentionally small: unmatched bold markers and other syntax remain literal.
