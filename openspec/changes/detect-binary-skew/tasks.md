# Tasks: detect-binary-skew

**Design doc:** `openspec/changes/detect-binary-skew/design.md`

## 1. Tests

- [ ] 1.1 Write failing tests for the identity helper: `debug.ReadBuildInfo`-backed identity returns SHA+dirty when VCS present, short-hash fallback when absent (`internal/daemon` or a small `internal/buildid` pkg).
- [ ] 1.2 Write failing tests for `staleDecision`/supervisor split: decision is hash-based; differing VCS + equal hash ⇒ not stale; old-protocol supervisor (empty hash) ⇒ unknown, not stale.
- [ ] 1.3 Write failing tests for the pure `doctor` verdict function: healthy / restart-needed (same path, diff hash) / path-divergence (diff files) / unknown-row-degrades.
- [ ] 1.4 Write failing tests for the modal flow: supervisor checked on auto-start path; daemon check gated on pre-existing; supervisor restart requires a second confirm naming agent count; decline leaves supervisor running.
- [ ] 1.5 Confirm every `it should X` acceptance criterion in `design.md` maps to a failing test (Prove-It).

## 2. Identity model + cross-process relay

**Depends on:** Stage 1

- [x] 2.1 Add a build-identity helper (commit SHA + `modified` flag via `runtime/debug.ReadBuildInfo`) reused by daemon, supervisor, and TUI. — `internal/buildid`.
- [x] 2.2 Add `BinaryHash` + VCS fields to `daemon.HelloResp`; supervisor populates them at boot (hash its own resolved binary like the daemon does); bump `daemon.ProtocolVersion` 2→3 with a v3 history note.
- [x] 2.3 Add supervisor fields to `daemon.BootInfoResp` (`SupervisorPresent`, `SupervisorPath`, `SupervisorHash`, `SupervisorVCS`) + the daemon's own VCS; populate `BootInfo` by querying the supervisor's `Hello` at serve time (re-query, not cached at `New()`), feature-detecting an old supervisor (empty hash ⇒ present-but-unknown).
- [x] 2.4 Plumb the enriched `BootInfoResp` through `internal/daemon/client` and update the `daemon-client`/`fakedaemon` test doubles.

## 3. `argus doctor` command

**Depends on:** Stage 2

- [ ] 3.1 Write the pure verdict function: inputs = gathered identities (PATH `argus`, `~/.argus/argusd` target, `go install` target, daemon, supervisor, TUI); output = verdict (healthy / restart-needed / path-divergence) + remediation text. No I/O.
- [ ] 3.2 Wire the `doctor` subcommand in `cmd/argus/main.go`: gather identities (best-effort, unknown-on-failure), connect to the daemon for `BootInfo`, resolve symlinks/PATH/`go env GOPATH`, print the table + verdict + fix command. Read-only; runs without launching the TUI.

## 4. Startup detection + supervisor-aware modal

**Depends on:** Stage 2

- [ ] 4.1 Split `isDaemonStale` into `daemonStaleDecision` + `supervisorStaleDecision` over the enriched `BootInfoResp`; in `main.go` compute supervisor staleness whenever a supervisor is present (regardless of `preExisting`), keep daemon staleness gated on `preExisting`.
- [ ] 4.2 Extend the modal (`internal/tui/app.go` + `internal/tui/modal/`) to render rich identity for the stale process and offer the relevant restart action(s).
- [ ] 4.3 Add the supervisor-restart action with a double-confirm (`modal.ConfirmModal`) reading "Are you sure? This will restart N agent processes" (N = supervisor `ListSessions` count); wire the actual restart (re-exec `session-supervisor start` per the design's open question) only on the second yes.

## 5. Docs + gotchas

**Depends on:** Stage 3, Stage 4

- [ ] 5.1 Add `argus doctor` to the README Reference (commands table); note the path-divergence footgun.
- [ ] 5.2 Add a gotcha entry (`context/knowledge/gotchas/daemon-rpc.md`): the `go install` skew failure mode, that supervisor staleness is checked on the auto-start path while daemon staleness is not, the additive `ProtocolVersion` 2→3 + old-supervisor-unknown rule, and that doctor's verdict distinguishes restart-needed from path-divergence.
- [ ] 5.3 Run `make pre-pr` until green.
