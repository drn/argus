## 1. Fix

- [x] 1.1 Add `Settled` to the cleared marker and re-arm a fingerprint-less settled marker when a fingerprint is available
- [x] 1.2 Log the re-arm decision via uxlog

## 2. Tests

- [x] 2.1 Unit test in `internal/agent` for the marker behavior
- [x] 2.2 Real-bytes regression test through `detectNeedsInputSticky` (fixtures under `internal/tui/testdata/needsinput`)

## 3. Docs

- [x] 3.1 Gotcha in `context/knowledge/gotchas/events.md` and index count bump
- [x] 3.2 Archive: delta merged into `openspec/specs/idle-detection/spec.md`
