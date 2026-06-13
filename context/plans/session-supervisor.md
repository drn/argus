# Session-Supervisor — design doc + phased plan (M8.5 → pre-M9)

**Status:** DESIGN ONLY (Phase 1). No implementation in this PR. Awaiting coordinator/Darren review before any code lands.
**Author:** m8punkt5-daemon-bounce worker (Opus), 2026-06-13.
**Decision lineage:** Aaron flagged the recovery gap ("subs detach on Argus restart"); Darren chose in-process Hera + *"a single daemon with a strict, unchanging interface that all it does is keep the agent processes up."* M8.5 investigation proved the gap is real (see below). Coordinator decision: **build the real supervisor (Option B); reject fd-handoff (A); skip the interim auto-respawn (C).**

---

## 0. Why — the verified problem

The merge plan (lines 53, 162) claims *"daemon-owned PTYs already survive restart."* **This is false.** Empirically verified in the M8.5 investigation (throwaway `creack/pty` probe, since deleted; full findings in KB `memory/project/m8punkt5-daemon-bounce-findings.md`):

- Agents are session leaders in their **own** session (`agent.StartSession` → `creack/pty.StartWithSize` forces `Setsid`+`Setctty`), so signalling the daemon pid does **not** reach them via process group.
- But agents die on every daemon bounce via **two** mechanisms:
  1. **Explicit kill** — `daemon.cleanup()` → `runner.StopAll()` SIGTERMs every agent (`daemon.go:896`).
  2. **PTY-master-close SIGHUP** — when the daemon process exits for *any* reason (graceful, crash, SIGKILL where `cleanup` never runs), the kernel closes every PTY master fd; closing the master delivers **SIGHUP** to the slave's session → agent dies. Proven: a child started exactly like `StartSession` dies with `signal: hangup` the instant the master closes, no signal sent.
- Today's bounce is therefore a **re-spawn, not a re-attach**: `bounce.go` persists only task IDs, `ReconcileStaleSessions` flips orphans → InReview, `replayBounceSignals` posts `ARGUS_BOUNCED` to inboxes — but **nothing relaunches the agent**, and the signal lands in the inbox of an already-killed agent. A human presses Enter to `--resume`. The in-flight turn is lost.

**Core constraint:** keeping an agent running *usefully* across a daemon death requires the PTY **master fd** (and the process) to outlive the daemon — else SIGHUP, and even if SIGHUP were ignored the agent's tty I/O fails `EIO`. The master fd lives only in the owning process's address space.

**Why not fd-handoff (Option A, rejected):** passing the master fd old→new daemon via `SCM_RIGHTS` fights the flock-singleton/no-coexistence model, is fragile on SIGKILL, and **regresses #707**: a re-adopted process is *not a child* of the new daemon, so `Cmd.Wait()` can't reap it or read its exit code — `ExitInfo.CleanExit()` breaks.

**The fix (Option B):** make the **owning process never bounce.** A separate long-lived **session-supervisor** forks and owns every agent PTY + process; the argus **daemon becomes a client** of it. Bouncing the daemon (to iterate on hera/coordination) leaves the supervisor — and every agent — untouched. The supervisor is the always-parent, so it observes real exit codes → #707 preserved structurally.

---

## 1. Architecture

### 1.1 Process layering

```
┌─────────────┐     unix sock (R/S protocol)      ┌──────────────────────────┐
│   TUI       │  ───────────────────────────────▶ │  argus daemon            │
│ (tcell)     │     ~/.argus/daemon.sock           │  (BOUNCE-ABLE)           │
└─────────────┘                                    │  • hera / coordination   │
                                                   │  • API / MCP / scheduler │
                                                   │  • depswatcher / heraAdopt│
                                                   │  • d.runner = SUPERVISOR  │
                                                   │    CLIENT (not in-proc)   │
                                                   └───────────┬──────────────┘
                                                               │ unix sock (R/S protocol)
                                                               │ ~/.argus/supervisor.sock
                                                               ▼
                                                   ┌──────────────────────────┐
                                                   │  session-supervisor      │
                                                   │  (NEVER bounced)         │
                                                   │  • agent.Runner          │
                                                   │  • owns Cmd + ptmx       │
                                                   │  • readLoop/waitLoop      │
                                                   │  • ring buffers + logs    │
                                                   │  • Cmd.Wait → ExitInfo    │
                                                   └──────────────────────────┘
```

### 1.2 The key insight — reuse the existing client/stream machinery verbatim

The daemon already exposes a first-byte **`R` (JSON-RPC) / `S` (raw stream)** protocol (`daemon.go:821 handleConn`), and the TUI already consumes it via `internal/daemon/client` (`Client` implements `agent.SessionProvider`; `RemoteSession` implements `agent.SessionHandle` with a **local ring** fed by a stream reader that reconnects with `StreamHeader{TaskID, Since}` for gap-free replay). The TUI *already* swaps between an in-process `*agent.Runner` and this daemon-client (`cmd/argus/main.go` runTUI).

**We apply the exact same swap one layer down.** The in-process `*agent.Runner` physically **moves into the supervisor**. The supervisor serves the **same R/S protocol** the daemon serves today. The daemon's `d.runner` field changes from `*agent.Runner` to a **supervisor-client** that implements `agent.SessionProvider` — structurally identical to today's daemon-client.

Consequences:
- **No new wire protocol.** Reuse `daemon/types.go`, `daemon/rpc.go`, `daemon/stream.go`, `daemon/client/*` (generalized for two consumers).
- **No SCM_RIGHTS / fd-passing.** The supervisor keeps the master fd + `readLoop`; it *streams bytes* to the daemon exactly as the daemon streams to the TUI today. The daemon's local ring (in its supervisor-client `RemoteSession`) is the fan-out source for TUI stream conns — the daemon's existing `handleStream` tees from that local ring to all TUI clients.
- **Bytes path:** supervisor `readLoop` → daemon's supervisor-client local ring → daemon `handleStream` → TUI local ring → x/vt. A double-proxy hop (see Risks); acceptable, all reusing built code. Optional later optimization: TUI streams directly from the supervisor (coordination RPCs stay on the daemon).

### 1.3 What lives where (post-move)

| Concern | Supervisor (never bounced) | Daemon (bounce-able) |
| --- | --- | --- |
| `exec.Cmd`, `ptmx` master fd | **owns** | — |
| `readLoop`/`waitLoop`, `Cmd.Wait()` | **owns** | — |
| ring buffer + `~/.argus/sessions/<id>.log` | **owns** (source of truth) | local mirror ring (per supervisor-client `RemoteSession`) for TUI fan-out |
| `ExitInfo` cache (`Err`/`Stopped`/`StreamLost`/`PendingRestart`) | **computes** from `Cmd.Wait()` | relays via `GetExitInfo` RPC; forces `StreamLost` on RPC failure |
| `pendingRestart`/`KickRerender` | **owns** | observes via `HasPendingRestart` RPC (unchanged surface) |
| `BuildCmd` / sandbox-exec wrap / env (`TERM`,`COLORTERM`,`ARGUS_TASK_ID`) | **runs** (it spawns `sh -c`) | — |
| `CaptureSessionID` / `NeedsSessionRecapture` (Claude `/clear`, Codex `state_5.sqlite`, Pi) | **runs post-exit** (it owns the worktree-scoped read) OR daemon runs it on the exit relay — see §2.3 | hook on exit relay |
| DB status flips (#707 `transitionTaskOnExit`, `RollHeraWorkerToReview`) | — | **owns** (DB is daemon's) |
| hera / API / MCP / scheduler / depswatcher / heraAdopt / push / clipboard | — | **owns** (all coordination iteration happens here, bounce-freely) |

---

## 2. #707 preservation (load-bearing)

The single authoritative predicate stays `daemon.ExitInfo.CleanExit() = !Stopped && Err=="" && !StreamLost` (`daemon.go:82`). It is preserved **structurally**:

1. **Exit code observed by the always-parent.** The supervisor forked the agent, so its `waitLoop` runs `s.Cmd.Wait()` (`session.go:214`) and gets the real `*os.ProcessState` / exit error — distinguishing clean exit (nil), non-zero/crash (`exec.ExitError`), and signal. This never crosses a non-parent boundary (the bug Option A had).
2. **Relayed, not re-derived.** The supervisor's `onFinish` builds the `ExitInfo` (`Err`, `Stopped`, `LastOutput`) exactly as the daemon's runner does today (`runner.go:154` `exitErr := sess.Err()`). It caches it and signals the daemon's supervisor-client via stream-close, identical to how the daemon signals the TUI today.
3. **The daemon's supervisor-client fetches it via `GetExitInfo` RPC** and runs the *unchanged* `transitionTaskOnExit` + `RollHeraWorkerToReview` against its DB. The existing defense — *"if `GetExitInfo` RPC fails, force `StreamLost=true`"* (`daemon-rpc.md`:10) — now guards the supervisor→daemon boundary so a failed relay can never read as a clean exit and wrongly Complete a task.
4. **Stream-loss vs exit** stays distinct: a daemon→supervisor stream blip (supervisor alive) is `StreamLost`, not exit; only a confirmed `Cmd.Wait()` return is an exit. Same rule the daemon-client uses against the daemon today.
5. **Hera worker finish policy (BUG-050)** is unchanged: it lives in `transitionTaskOnExit` / `RollHeraWorkerToReview`, both DB-side in the daemon. The supervisor never touches status.

**Net:** the exit-status crossing the supervisor→daemon boundary is byte-for-byte the same `ExitInfo` mechanism that already crosses the daemon→TUI boundary, with the same RPC-failure backstop. #707 cannot regress unless that mechanism regresses for the TUI too.

---

## 3. Re-attach, not re-spawn (the payoff)

On a **daemon** bounce (the common case — iterating on hera/coordination):

1. New daemon starts, `killExistingDaemon` + daemon flock takeover (supervisor untouched — see §4).
2. Daemon constructs its supervisor-client and **connects to the still-running `~/.argus/supervisor.sock`** (auto-starts the supervisor only if absent — see §4.3).
3. The daemon's `ListSessions` against the supervisor returns the **live** sessions. For each, the supervisor-client opens a stream with `StreamHeader{Since:0}` → supervisor replays its ring → daemon's local ring rebuilds → TUI re-attaches with its own `Since` cursor. **No agent process was killed. The in-flight turn continues.**
4. **`bounce.go` becomes re-attach:** the startup reconcile must NOT blanket-flip InProgress→InReview. Revised order (supervisor mode):
   - `killExistingDaemon` → daemon flock.
   - **Query supervisor `ListSessions`** → set of live task IDs.
   - `ReconcileStaleSessions` flips InProgress→InReview **only for tasks the supervisor does NOT report alive** (true orphans, e.g. supervisor also restarted). Tasks the supervisor still runs stay InProgress.
   - `heraadopt.ReconcileBindings` **unchanged** — keyed on task-row existence, not session liveness (`heraadopt` ReconcileBindings). A re-attached task's row exists → its binding survives. M4 composes for free. ✅ (verified)
   - `replayBounceSignals` only for tasks that actually lost their session (or drop it entirely in supervisor mode — re-attached agents were never interrupted, so there's nothing to signal).
5. **TUI needs no change** — its tick reconciles against `RunningAndIdle()`; re-attached sessions show up as alive/InProgress. The existing `restartDaemon` flow (close client → AutoStart → re-wire `OnSessionExit` → reset `runningIDs`) already re-fetches from the new daemon.

When the **supervisor itself** must restart (rare — see §4.4), agents *are* interrupted (same SIGHUP mechanism). That is the irreducible residual; the supervisor's "strict, unchanging interface" minimizes how often that happens.

---

## 4. Lifecycle, singleton, and split-brain

### 4.1 Two processes, two flocks
- Daemon keeps `~/.argus/daemon.lock` + `daemon.sock` + `daemon.pid` (unchanged, `singleton`/`lock.go`).
- Supervisor gets its **own** `~/.argus/supervisor.lock` + `supervisor.sock` + `supervisor.pid`, using the **same** `acquireSingletonLock` (flock `LOCK_EX|LOCK_NB`, 2s timeout) + `killExistingDaemon`-style pid takeover, refactored to be path-parameterized.
- The two singletons are independent. A daemon takeover never touches the supervisor flock/pid; a supervisor takeover never touches the daemon's.

### 4.2 killExistingDaemon scope
`killExistingDaemon` **only ever targets the daemon pid file.** It must never signal the supervisor. The supervisor has its own `argus session-supervisor stop`. This is the whole point: daemon serial-takeover (SIGTERM old daemon → flock handoff → new daemon binds) proceeds exactly as today, and the new daemon simply re-connects to the live supervisor. No split-brain: daemon flock forbids two daemons; supervisor flock forbids two supervisors; the daemon↔supervisor link is a plain client connection.

### 4.3 Who starts the supervisor
Mirror `autoStartDaemon` (`client/autostart_fork.go`): `Setsid` fork of `os.Executable() session-supervisor start`, `cmd.Process.Release()`, poll `supervisor.sock` (50ms / 3s). The daemon auto-starts the supervisor on its own startup **iff** no live supervisor responds to `Ping`. Because the supervisor is detached (`Setsid`) and not a child of the daemon, a daemon exit does not SIGHUP it (it has no controlling tty tied to the daemon). The supervisor also gets the daemon's logging guards: `slog.SetDefault`→file, `log.SetOutput`→file (it writes to `~/.argus/supervisor.log`), and — because it forks PTY children — the same `os.Stderr`/fd2 discipline (it never writes to fd 1/2 after startup; subprocess stderr defaults to `/dev/null`, never inherited).

### 4.4 When the supervisor must restart (the residual)
The only event that still interrupts agents is a supervisor restart. To keep that rare and safe:
- **Strict, unchanging, versioned protocol** (Darren's mandate). The supervisor↔daemon R/S protocol is **additive-only** and carries a `ProtocolVersion` in a `Hello`/`BootInfo` handshake. A newer daemon must talk to an **older, already-running** supervisor (because `go install` + daemon restart does NOT restart the supervisor). So:
  - The daemon **never auto-restarts a live supervisor on version skew** — it logs a warning and operates within the older protocol's capabilities, or surfaces a "supervisor restart needed (will interrupt agents)" prompt for explicit user action. It must NOT silently kill agents to pick up a new supervisor binary.
  - New RPCs/fields are optional; the daemon feature-detects via the handshake version.
- **Binary staleness UX:** today the TUI prompts to restart a *stale daemon*. With the split, restarting the daemon for a rebuild is **cheap** (agents survive) — so that prompt becomes low-stakes. A separate, explicit, rarely-needed "restart supervisor" action is the only agent-interrupting path, and it is never automatic.
- **Supervisor crash recovery (degraded mode):** if the supervisor dies unexpectedly, its agents die (master close → SIGHUP, same as today). The daemon detects `Ping` failure, restarts the supervisor, and falls back to the existing reconcile→InReview + `ARGUS_BOUNCED` path. This is exactly where the rejected interim **Option C (auto-respawn-on-startup)** could be revived later as a *degraded-mode* recovery — out of scope for the core delivery, noted for completeness.

---

## 5. What must be preserved across the move (checklist)

All of these are exercised by existing tests; each phase must keep them green:
- **Ring-buffer replay** (`AddWriter`/`AddWriterFrom`/`AddWriterFromTolerant`, `Since` offset, `TotalWritten` atomic) — moves into the supervisor unchanged; the daemon's supervisor-client reuses the daemon-client replay logic.
- **Writer fan-out** (multi-writer tee, errored-writer auto-removal) — supervisor-side.
- **Attach/Detach** full-screen (`attach.go`, `detachReader`, Ctrl+Q) — the supervisor owns the PTY, so `argus session-supervisor attach <task>` (or the daemon proxies attach). Decide in P2; attach is rarely used in daemon mode (TUI uses the pane, not full attach).
- **`NeedsSessionRecapture` / Claude `/clear` recapture / Codex post-exit `state_5.sqlite` / Pi session-file scan** — these read worktree-scoped state. Cleanest: the **supervisor** runs `CaptureSessionID` post-exit (it owns the process+worktree timing) and returns the captured `SessionID` in the exit relay; the daemon persists it. Alternative: daemon runs it on the exit relay (it has DB + worktree path). Either preserves the "both sites gate on `NeedsSessionRecapture`" invariant — pick one, document it, don't double-write.
- **`os.Stderr`/fd2 + slog/log redirects** — replicated in the supervisor's startup (it forks PTY children, so the discipline is mandatory).
- **Sandbox-exec wrapping** — supervisor-side (it builds `sh -c`).
- **Hera binding reconciliation (M4)** — daemon-side, unchanged; composes with re-attach (verified, keyed on task-row existence).
- **`pendingRestart`/`KickRerender` kick-rerender** — supervisor-side; daemon observes `HasPendingRestart` over RPC (already an RPC today).
- **`ARGUS_TASK_ID` env export** for MCP sub-tasks — supervisor-side `BuildCmd`.
- **PTY size alignment** (`computePTYSize`, `ForceResyncPTY`, the agent-view overhead constants) — sizes cross as `Rows`/`Cols` in `StartReq`/`ResizeReq` exactly as daemon RPCs carry them today; no math changes, just one more hop.

---

## 6. Phased PR breakdown

Each phase is independently shippable, keeps `make pre-pr` green, and keeps the TUI working. Phases P1–P3 are dark/flagged so master is never destabilized.

- **P0 — refactor seam (no behavior change).** Parameterize `acquireSingletonLock`/`killExistingDaemon`/pid+sock paths so they work for an arbitrary `(lock, sock, pid)` triple (today hard-wired to daemon paths). Extract the RPC-service + stream-handler + Runner wiring into a reusable "session server core" that both the daemon (today) and the supervisor (P1) can mount. Pure refactor; existing daemon behavior identical. *Risk: low.*
- **P1 — supervisor binary, dark.** Add `argus session-supervisor {start,stop,status}` (route in `main.go` mirroring `daemon`). It owns an `agent.Runner` and serves the **existing** R/S protocol on `supervisor.sock` with its own flock, `Setsid` auto-startability, logging guards, graceful shutdown, and a `Hello`/version handshake. **Nothing connects to it yet.** Tested in isolation with `exec.Command("sleep")`/`echo` fakes under `t.TempDir()`, `t.Setenv("HOME",...)`; never touches the real daemon/socket. *Risk: low (additive, unused).* 
- **P2 — daemon connects (flagged, default OFF).** Add `cfg.Supervisor.Enabled`. When ON: `d.runner` becomes a supervisor-client (generalize `internal/daemon/client` to target either socket); the daemon auto-starts the supervisor if absent; `onFinish`/`OnSessionExit`/`captureSessionIDPostExit`/stream-proxy/`ListSessions` wire through it; `GetExitInfo`-failure→`StreamLost` backstop applies to the supervisor boundary. When OFF: today's in-process Runner path, byte-identical. *Risk: high — the heart; flag-gated so master stays safe. Heavy test coverage: exit-relay #707 matrix (clean/crash/stop/stream-lost), fan-out, recapture.* 
- **P3 — re-attach on daemon bounce (flagged).** In supervisor mode, daemon startup queries supervisor `ListSessions` and re-attaches live sessions instead of flipping them to InReview; `bounce.go` becomes re-attach; confirm hera bindings survive and #707 holds across the bounce. Add an integration test: start supervisor + fake session, bounce the *daemon* process, assert the session is still alive and re-attached and the task stayed InProgress. *Risk: medium.*
- **P4 — flip default ON + retire in-daemon PTYs.** After burn-in, default `cfg.Supervisor.Enabled=true`; keep the in-process path one release as rollback. Update README (Reference appendix only) + `gotchas/daemon-rpc.md` + `pty-terminal.md`. **This unblocks M9** (M9 retires the external hera daemon and depends on real bounce-resilience). *Risk: medium (the cutover); mitigated by the flag + retained fallback.*

---

## 7. Risks & migration

- **Double-proxy latency** (supervisor→daemon→TUI for output bytes). All reused code; measure in P2. If material, add an optional direct TUI→supervisor stream (coordination RPCs stay on the daemon) as a follow-up — not required for correctness.
- **Protocol skew / "strict unchanging interface."** The hardest correctness item. The daemon may be newer than a long-lived supervisor. Mitigation: additive-only, versioned protocol + handshake; daemon never auto-kills a live supervisor to adopt a new binary (§4.4). Treat the supervisor↔daemon protocol as a frozen public contract — review changes like an API break.
- **Supervisor as the new single point of agent-interruption.** Irreducible: restarting the supervisor kills agents. Minimize its code surface (it does *only* PTY supervision) so it almost never needs to change. Layer Option-C auto-respawn later as degraded-mode recovery for crashes.
- **No flag-day.** P2/P3 ship behind `cfg.Supervisor.Enabled=false`; P4 flips the default with the in-process path retained as rollback. Rollback = set the flag false + restart the daemon (agents re-spawn via the existing `--resume` path; the supervisor can be `session-supervisor stop`-ped).
- **Test safety (every phase):** never signal/dial the real daemon or supervisor; `agent.NewRunner(nil)` + `exec.Command("sleep")`/`echo`; `t.TempDir()` + `t.Setenv("HOME",...)`; keep socket-path test names short (macOS 104-byte `sun_path`). A real detach/SIGHUP test uses a throwaway `sleep` child only.

---

## 8. Open questions for review (Darren)
1. **Recapture ownership** (§5): supervisor-runs-`CaptureSessionID`-and-relays vs daemon-runs-on-exit-relay. Recommend **supervisor-runs** (owns process+worktree timing), relayed in `ExitInfo`. Confirm.
2. **Attach** in supervisor mode (§5): do we still need full-screen `attach` (`argus attach`)? If yes, supervisor must expose it (proxy or its own `attach` subcommand). Recommend deferring/proxying — the TUI pane covers 99% of use.
3. **Direct TUI→supervisor stream** optimization: build now or defer until latency is measured? Recommend **defer** (correctness first).
4. **Protocol-skew policy** (§4.4): confirm "daemon never auto-restarts a live supervisor on version skew; warns + explicit user action only."
5. Should degraded-mode **Option C (auto-respawn on supervisor crash)** be in P3, or a separate later milestone? Recommend **later**.
