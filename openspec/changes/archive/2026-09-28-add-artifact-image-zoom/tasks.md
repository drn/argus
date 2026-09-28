## 1. Implementation

- [x] 1.1 In `makeArtifactNode`'s `art.type === 'image'` branch, add click/tap-to-toggle zoom state (fit ↔ actual size) with the container scrollable at actual size.
- [x] 1.2 Add a discoverability affordance (cursor + small hint) so the click target is obvious; reset zoom state when the artifact view is left/switched.
- [x] 1.3 Verify identical behavior in both the paneled viewer and the full-screen overlay (both consume `makeArtifactNode`).
- [x] 1.4 Bump `SW_VERSION` in `internal/api/static/sw.js` since a shell asset changed.

## 2. Documentation

- [x] 2.1 Add the zoom-toggle behavior to `context/knowledge/gotchas/web-remote.md`'s session-artifacts section, noting it lives in `makeArtifactNode`.

## 3. Verification and archive

- [x] 3.1 Manually exercise the feature in a browser (desktop click and, if feasible, a touch/mobile viewport) for both viewer presentations; check for regressions in existing artifact types.
- [x] 3.2 Run `make pre-pr`.
- [x] 3.3 Archive this OpenSpec change on the same branch before opening or updating a PR, merging the delta into the base spec.
