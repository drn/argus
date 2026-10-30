## Context

The board already renders wrapped lines with a clamped offset and supports PgUp/PgDn. Hera's details-region mouse routing skips the board entirely. The roster shares the same horizontal region, so routing must also test the board's vertical bounds.

## Goals / Non-goals

- Enable three-line wheel scrolling through a single tall item, independent of focus.
- Keep selection navigation following the selected header and preserve existing keyboard controls.
- Leave the InboxModal and other clients unchanged.

## Decisions

- Record the board's actual drawn rectangle and content viewport dimensions. Route wheels only inside it, retaining the page wrapper's existing focus guard.
- Reuse the draw-time clamp for content and viewport changes. Explicit scrolling cancels pending cursor follow; item navigation and expansion request cursor follow again.
- Retain existing PgUp/PgDn rather than add a new key.

## Risks / Trade-offs

- Stale bounds after a layout change could scroll hidden content: reset board bounds before drawing the details stack and gate dispatch on visible board mode.
- Wrapped content changes with width: recompute line count and clamp during every draw.
