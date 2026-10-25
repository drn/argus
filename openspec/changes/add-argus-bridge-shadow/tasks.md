> Archiving: Aaron runs `openspec archive add-argus-bridge-shadow` before merge, after he has dogfooded this change. Do NOT archive from the implementing session.

## 1. Spec and plumbing

- [x] 1.1 Write proposal, design, delta specs and tasks; `openspec validate --all --strict` passes
- [x] 1.2 Add `[bridge] enabled` config (`config.BridgeConfig`, default true, DB key `bridge.enabled`) with tests

## 2. Daemon: bridge package

- [x] 2.1 `internal/bridge`: wire types, `Hub` per-task state (seq/boot dedupe, liveness, session id/model/measure), with tests
- [x] 2.2 `internal/bridge`: `Shadow` comparator (settle window, counters, uxlog `[bridge]` lines), with tests
- [x] 2.3 `internal/api`: `POST /api/tasks/{id}/bridge/events`, `GET /api/tasks/{id}/bridge`, `GET /api/bridge/shadow`, with tests
- [x] 2.4 Hook the comparator into `idleWatcherTick` and `detectNeedsInputTick` (observation only), with tests
- [x] 2.5 Daemon writes `~/.argus/api-port` atomically after the API binds, with tests

## 3. Mod and loading

- [x] 3.1 Mod source: manifest, hooks, pure logic modules (`tracker`, `reporter`, `measure`), `claude plugin test` suites, tsconfig; passes `claude plugin validate`, `tsc -p`, `claude plugin test`
- [x] 3.2 `internal/bridgemod`: go:embed + content-addressed materialization + test seam, with tests
- [x] 3.3 `Family.BridgeMod` capability (Claude only) and BuildCmd `--plugin-dir` injection gated by config; re-record the supervisor spawn-surface digest per `gotchas/ci-gates.md`

## 4. Docs and verification

- [x] 4.1 Gotchas file `context/knowledge/gotchas/bridge.md` + index row; backends matrix row; README Reference REST table
- [x] 4.2 Live end-to-end run: `claude --plugin-dir` in tmux with a real `ARGUS_TASK_ID`; record shadow counters
- [ ] 4.3 `make pre-pr` clean; open PR "Argus bridge phase 1: shadow-mode session reporting"

## 5. Review fixes (independent review of #1068)

- [x] 5.1 Hooks never await delivery (background single-flight drain); hung-fetch tests
- [x] 5.2 Materialize race: never remove an existing hash dir; concurrency test; location moved to `~/.argus/cache/bridge-mod`
- [x] 5.3 New `boot` resets busy/needs-input in the hub; test
- [x] 5.4 Materialize/ingest failures also log via slog (daemon/supervisor never init uxlog)
- [x] 5.5 Mod forgets the cached port on a non-2xx other than 401; daemon removes `api-port` on clean shutdown (only if still its own)
- [x] 5.6 Test gaps: observation-does-not-alter-verdicts, inert-reads/heartbeat, >1MiB body, custom family capability
- [x] 5.7 Permission-kind confidence tag and separate `needs_input_permission` counters; subagent asks reported; spec drift fixes; frontend-parity Non-Goal
