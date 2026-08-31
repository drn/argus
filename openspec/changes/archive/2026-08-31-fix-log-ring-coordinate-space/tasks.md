# Tasks

## 1. Anchor helper

- [x] 1.1 Add a content-anchoring helper that locates a ring tail inside a log tail and reports how much of the ring the log already covers.
- [x] 1.2 Unit tests: exact match, log-runs-ahead, log-lags (partial coverage), no overlap, anchor too short.

## 2. Ring-wrap catch-up

- [x] 2.1 Red test: a session handle whose byte counter is offset from the log reproduces ancient-content injection through `renderLive`.
- [x] 2.2 Replace the absolute-offset read with an anchored read of the `newBytes` log bytes ending at the ring tail.
- [x] 2.3 Confirm the local-mode (zero-offset) path is byte-identical to the previous behavior.

## 3. Full-replay merge

- [x] 3.1 Red test: `ringTotal > logSize` on an offset handle splices non-contiguous bytes.
- [x] 3.2 Replace the `ringTotal - logSize` arithmetic with anchored merging; ring-only fallback when unreconcilable.
- [x] 3.3 Keep the BUG-079 no-ESC guard on the ring-only path.

## 4. Docs and gates

- [x] 4.1 Update `context/knowledge/gotchas/pty-terminal.md` and `hera-view.md`.
- [x] 4.2 `make pre-pr` green.
- [x] 4.3 Archive the change into the base spec before merge.
