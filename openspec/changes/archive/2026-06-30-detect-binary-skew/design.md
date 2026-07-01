## Context

Argus runs as three cooperating processes that each load a binary from disk:

- the **TUI** (`argus`, the binary on the user's `PATH`),
- the **daemon** (launched from the `~/.argus/argusd` symlink), and
- the **session-supervisor** (a separate, long-lived process that owns the agent PTYs).

`go install ./cmd/argus/` writes to `$(go env GOPATH)/bin/argus`. On a fresh install the `argus` on `PATH` and the `~/.argus/argusd` symlink target can resolve to a **different file** than the `go install` target. The result is a *binary skew*: the TUI runs one build, the daemon and/or supervisor run another. Because the TUI and daemon communicate over the R/S socket, the keys that need that round-trip silently break (Enter to attach a session, Ctrl+Q to detach a stream) while purely-local TUI keys (1/2/3 tab switch) keep working — a confusing, hard-to-self-diagnose state. This was hit in the field: a user ran `go install`, saw the native 3-pane Hera appear (newer TUI) but lost Enter/Ctrl+Q (older daemon), restarted the daemon, and it did not help — because the daemon relaunched the *same old binary from the divergent symlink path*.

**Current state of detection.** `cmd/argus/main.go:isDaemonStale` already compares the daemon's boot-time binary hash (`daemon.BootInfoResp.BinaryHash`, a SHA-256 content hash) against the TUI's own resolved binary, and `App.SetDaemonStale` drives a blocking `RestartDaemonModal` (Restart / Skip). It has three gaps:

1. It is **skipped entirely on the auto-start path** (`if err == nil && preExisting`) — the assumption being that a daemon the TUI just forked can't be stale. True *for the daemon*, but it leaves the supervisor unchecked on that path.
2. It **never checks the supervisor**, which is a separate long-lived process. `HelloResp` already carries the supervisor's `BinaryPath`/`BinaryMtime` and the type's doc explicitly defers the hash: *"When supervisor-staleness checking is actually implemented, add `BinaryHash` here and bump `ProtocolVersion` then."*
3. It surfaces only an opaque "stale" boolean — it never names the **paths**, so it can't reveal the actual root cause (path divergence) and can't tell the user the fix.

**Constraints.**
- The R/S protocol between daemon and supervisor is **additive-only** and version-gated (`daemon.ProtocolVersion`, currently 2). Adding a field to `HelloResp` requires bumping to 3.
- Restarting the supervisor **interrupts every running agent** (`cleanup` → `runner.StopAll()`). This is the one event the daemon/supervisor split exists to avoid; it must never happen by accident.
- The staleness *decision* must stay hash-based — VCS info (see Decisions) can be blank when a binary is built outside a git tree, so it is for **display only**, never the gating signal.
- Specs are LOCAL DOCS only; no spec tooling is wired into CI. The quality gate is `make pre-pr`.

## Goals / Non-Goals

**Goals:**

- Make TUI/daemon/**supervisor** binary skew impossible to miss at launch, and trivially diagnosable on demand.
- Add an `argus doctor` command that enumerates every argus binary on disk, what each process is running, and prints a green/red verdict with the **exact fix command** — distinguishing "just needs a restart" from "your binaries diverge by path" (the real footgun).
- Extend the existing startup modal to cover the supervisor and to display **rich identity** (commit SHA + dirty flag + path), and offer a **guarded, double-confirmed** supervisor restart that names the number of agents it will interrupt.

**Non-Goals:**

- **No auto-mutation.** We do not silently re-point symlinks, rewrite `PATH`, or auto-rebuild. `doctor` is read-only; the only mutation is a user-initiated, double-confirmed supervisor restart.
- **No persistent banner.** Surface is the blocking modal only (extends today's pattern). A skipped warning does not leave a lingering header indicator.
- **No change to `go install` behavior** or to how the daemon/supervisor are spawned.
- **No install-time WIP/dirty-build guard** and **no commit-SHA `-ldflags` plumbing** (VCS info comes free from `debug.ReadBuildInfo`).
- Remote (`--remote`) mode is out of scope — skew is about local binaries; the remote TUI talks to a remote daemon over REST and has no local daemon/supervisor to compare.

## Decisions

### D1. Treat the supervisor as a first-class checked actor; relay its identity through the daemon's `BootInfo`.

The TUI connects only to the daemon, not the supervisor. So the daemon — which already holds a `SupervisorClient` and performs a `Hello` handshake — relays the supervisor's identity to the TUI. `BootInfoResp` gains supervisor fields (`SupervisorPresent`, `SupervisorPath`, `SupervisorHash`, `SupervisorVCS`); the daemon populates them by querying `Hello` when it serves `BootInfo` (re-queried, not cached at `New()`, so an independently-restarted supervisor reports current identity). `HelloResp` gains `BinaryHash` + VCS, and `ProtocolVersion` bumps 2→3 (additive; a v2 supervisor simply reports empty hash → supervisor staleness is reported "unknown", never a false positive).

- *Alternative considered:* a separate `Daemon.SupervisorInfo` RPC. Rejected — `BootInfo` is already the one staleness call site; one round-trip is simpler and there is no other consumer.
- *Alternative considered:* have the TUI dial `supervisor.sock` directly. Rejected — it would make the TUI a second supervisor client (the daemon is the sole driver by design) and duplicate the handshake.

### D2. Identity = commit SHA + dirty flag + resolved path, read from `debug.ReadBuildInfo`, with the content hash as the gating signal and fallback.

Each process reads its **own** `debug.ReadBuildInfo()` at boot and reports `vcs.revision` + `vcs.modified`. Go stamps these automatically when building from inside the module's git tree (`go install ./cmd/argus/` from the repo), so no `-ldflags` wiring is needed. The TUI reads its own locally. Display format: `daemon: a1b2c3 (dirty) @ ~/.local/bin/argus`.

- The **staleness decision stays the SHA-256 content-hash comparison** (`staleDecision`), unchanged and authoritative — VCS info is blank for binaries built outside a git tree, so it can never gate. VCS + path are display-only.
- *Alternative considered:* `-ldflags -X` commit injection. Rejected — bare `go install` (the documented deploy path) wouldn't set it, so it would usually be blank; `ReadBuildInfo` populates for free on the same path.

### D3. `argus doctor`: read-only diagnostic with a verdict tree that distinguishes restart-needed from path-divergence.

A new subcommand (works without launching the TUI). It gathers: all argus binaries resolvable on `PATH` (`exec.LookPath` + the rest of `PATH`), the `~/.argus/argusd` symlink target, the `$(go env GOPATH)/bin/argus` target, the running daemon's identity (`BootInfo`), the supervisor's identity (relayed), and the TUI/`os.Executable()` identity. It prints a table and a verdict:

- **Healthy** — all resolve to the same file (matching hash). Exit 0.
- **Restart needed** — same resolved path but different hash (a rebuild happened, daemon/supervisor still on old bytes). Print the restart command(s).
- **Path divergence** — the daemon symlink target and/or the `PATH` `argus` resolve to **different files** (the root footgun). Print the re-point/reinstall fix, because a plain restart would loop.

Doctor never mutates. The verdict logic is a pure function (input: the gathered identities; output: verdict + fix text) so it is unit-testable without processes.

### D4. Surface via the existing blocking modal only, extended for the supervisor with a double-confirmed restart.

`isDaemonStale` splits into `daemonStaleDecision` and `supervisorStaleDecision` (both read the enriched `BootInfoResp`). Daemon staleness stays gated on `preExisting` (a just-forked daemon can't be stale); **supervisor staleness is computed whenever a supervisor is present, regardless of `preExisting`** — closing gap #1. The modal now renders rich identity for whichever of {daemon, supervisor} is stale and offers the relevant action(s):

- **Restart daemon** — existing flow, unchanged (safe; agents live on the supervisor).
- **Restart supervisor** — NEW, and **double-confirmed**: selecting it opens a second confirm reading *"Are you sure? This will restart N agent processes"*, where N is the live session count from the supervisor's `ListSessions`. Only an explicit second yes triggers it.
- **Skip** — proceed (no persistent banner; consistent with the chosen surface).

- *Alternative considered:* a persistent header banner after Skip. Rejected by the user — modal-only keeps wiring minimal; the modal firing correctly (now including the supervisor and the auto-start path) is the load-bearing fix.

## Risks / Trade-offs

- **Skipped modal re-strands the user** → Accepted. No persistent banner by choice; `argus doctor` is the on-demand recovery path, and the modal now fires in the previously-silent cases (supervisor, auto-start).
- **Supervisor restart kills running agents** → Mitigated by the double-confirm naming the exact agent count, and by never auto-restarting the supervisor (matches the existing `SupervisorProtocolMatch` "never auto-restart live supervisor" rule).
- **VCS info blank for out-of-tree builds** → Mitigated: hash remains the gating signal and the display falls back to the short hash; doctor still reaches a correct verdict from paths + hashes alone.
- **`ProtocolVersion` 2→3 bump against a live v2 supervisor** → Safe by the additive-only contract: the daemon feature-detects (empty `BinaryHash` from a v2 supervisor → supervisor staleness "unknown", reported as such, never a false "stale"). No auto-restart is triggered by skew.
- **`doctor` shelling to `go env GOPATH`** → Best-effort; a failure degrades that one row to "unknown", never aborts the command.

## Migration Plan

Additive, no data migration. Ship behind no flag (detection is always-on, matching today's `isDaemonStale`). Rollback = revert; the `ProtocolVersion` bump is forward/backward compatible by the additive contract, so a reverted daemon talks to a bumped supervisor and vice-versa.

## Alternatives considered

Captured inline per decision (D1–D4). The headline rejected alternatives: a separate supervisor-info RPC (D1), `-ldflags` commit injection (D2), and a persistent post-skip banner (D4).

## Acceptance criteria

**Supervisor identity relay (D1):**

- It should report the supervisor's binary hash, resolved path, and VCS identity to the TUI when a supervisor is connected.
- It should report supervisor identity as "unknown" (never "stale") when the supervisor speaks an older protocol version that omits the hash.
- It should report "no supervisor" cleanly when the daemon runs the in-process runner (supervisor disabled).

**Identity model (D2):**

- It should display each binary's commit SHA and a dirty flag when VCS build info is present.
- It should fall back to the short content hash for display when VCS build info is absent.
- It should base the stale/not-stale decision on the content hash, never on VCS info.

**`argus doctor` (D3):**

- It should exit 0 with a healthy verdict when the TUI, daemon, and supervisor resolve to the same binary.
- It should report "restart needed" with the restart command when a process runs a different hash at the same resolved path.
- It should report "path divergence" with the re-point/reinstall fix when the daemon symlink target and the PATH `argus` resolve to different files.
- It should run read-only and mutate nothing.
- It should degrade a single unresolvable row to "unknown" rather than aborting.

**Startup modal (D4):**

- It should compute supervisor staleness even when the TUI auto-started the daemon.
- It should keep daemon-staleness gated on connecting to a pre-existing daemon.
- It should display the rich identity of whichever of daemon/supervisor is stale.
- It should require a second explicit confirmation, naming the count of agents to be interrupted, before restarting the supervisor.
- It should not restart the supervisor if the second confirmation is declined.

## Open Questions

- Exact supervisor-restart mechanism for the modal action: re-exec `session-supervisor start` (which `killExistingDaemon`s its predecessor by pid then rebinds) vs. SIGTERM `supervisor.pid` and let the daemon's auto-start re-spawn it. Both interrupt agents identically; the implementer picks the one that composes cleanly with the daemon's existing `AutoStartSupervisor`. (Leaning re-exec for symmetry with the auto-start path.)
