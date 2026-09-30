## Why

Argus Web renders an image artifact's `<img>` at `max-width:100%` in both the paneled viewer and the full-screen "Open" overlay (`makeArtifactNode`, `internal/api/static/index.html`). A screenshot or diagram larger than the viewport is scaled down with no way to inspect it at native resolution — the user can only shrink the browser window's effective DPI by zooming the whole page, which also scales all the chrome around it.

## What Changes

- Add click/tap-to-zoom to image artifacts: clicking or tapping a rendered image toggles it between "fit to pane" (current behavior, unchanged default) and "actual pixel size," scrollable within its container to pan. A second click/tap, or navigating away from the artifact, resets it back to fit.
- Apply this in the one shared rendering path (`makeArtifactNode`'s `art.type === 'image'` branch) so both the paneled viewer and the full-screen overlay get it identically, per the existing "single source of truth for artifact rendering" rule.
- Add a visible affordance (cursor change and a small hint) so the interaction is discoverable, since nothing like it exists in the app today.
- No change to audio/video/HTML/PDF/text/markdown artifact rendering, to the artifact list/download endpoints, or to the blob-vs-token URL handling per type.

## Design Boundaries

- Toggle-zoom only, not continuous pinch/scroll zoom or drag-pan-at-arbitrary-zoom-level — picked to keep the change small and to work identically with mouse and touch with no gesture-tracking code.
- No new REST endpoint, DB field, or manifest change — this is purely a static-asset (HTML/CSS/JS) change to the existing image rendering branch.
- **TUI**: unaffected by design. The TUI's artifact browser (`openspec/specs/session-artifacts/spec.md`, "Preview or open a registered artifact from the TUI") already hands image artifacts to the OS's own image viewer, which has its own zoom; there is no in-TUI image rendering to add zoom to.
- **macOS app**: has no artifact browser at all yet (a named gap from the TUI artifact browser change). This change doesn't widen that gap since it adds no wire-level capability — it stays a web-only presentation change until the native artifact UI itself is built.

## Capabilities

### Modified Capabilities

- `session-artifacts`: image artifact rendering in Argus Web gains a zoom toggle.

## Impact

- `internal/api/static/index.html`: `makeArtifactNode`'s image branch, `.artifact-img`/`.artifact-img-wrap` CSS, `SW_VERSION` bump in `internal/api/static/sw.js` (shell asset changed).
- No Go code, REST route, or schema changes.
- Relevant gotcha file update (`context/knowledge/gotchas/web-remote.md`) noting the zoom-toggle behavior lives in the same `makeArtifactNode` single-source-of-truth function as sandbox/blob handling.

## Approval

Per `AGENTS.md`, implementation starts only after this change is approved. The proposal and delta spec are review material; no behavior has changed yet.
