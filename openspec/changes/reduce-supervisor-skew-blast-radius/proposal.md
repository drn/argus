# Make supervisor/daemon/TUI binary skew mostly harmless instead of mostly alarming

> **Status: PROPOSAL ONLY — not approved, not implemented.** No `tasks.md` and no
> implementation branch exist yet, per this repo's spec-first process. Approve or
> redirect before any code is written.

## Why

Supervisor binary skew has caused a live incident three-plus times in ~2.5 weeks. Each
time the remedy on offer was a manual `argus doctor` followed by a supervisor restart that
SIGHUPs **every** running agent. The operator's summary: *"I think TUI and supervisor shift
apart often. Restarting super is painful as it kills all running agents."*

The premise everyone has been working from — "they drifted, so restart the supervisor" — is
wrong most of the time, and the most recent occurrence proves it. On 2026-09-01 the TUI and
daemon were on `c7326dcf` and the supervisor still on `90306aea`, and the visible garbling
had nothing whatsoever to do with the skew:

```
$ git diff --stat 90306aea c7326dcf -- internal/agent internal/daemon internal/api
 internal/agent/agent.go      |  81 ++++++++++++++++++
 internal/agent/agent_test.go | 190 +++++++++++++++++++++++++++++++++++++
```

`internal/agent/session.go`, `ringbuffer.go` and `internal/daemon/sessioncore.go` were
byte-identical across the two builds; the only supervisor-side delta was a spawn-time
cache-dir env export. The real cause was a width-drift bug in the TUI
(`fix-deferred-anchor-clobber`). Two of the three earlier occurrences *were* genuine
supervisor skew, so the reflex is understandable — but it is now a coin flip dressed up as
a diagnosis, and the cost of guessing wrong is 25 dead agents.

### The signal is ~90% noise, and that is measurable

`daemon.BinaryHashFile` is a SHA-256 over the **entire `argus` binary**. The supervisor
executes a small, slow-moving slice of that binary. Over the last three months of `master`:

| | distinct commits |
| --- | ---: |
| All commits | 295 |
| Touch the supervisor's PTY/stream core (`session.go`, `ringbuffer.go`, `runner.go`, `sessioncore.go`, `supervisor.go`) | 13 |
| Touch anything supervisor-resident at all (the above **plus** the `BuildCmd` spawn stack: `agent.go`, `sandbox.go`, `skills.go`) | 28 |

So **roughly 9 in 10 builds change nothing the supervisor runs, yet every single one of
them makes the whole-binary hash differ** and gets reported as supervisor staleness. (Worse:
`make install-signed` re-signs the binary *after* `go install`, so the hash is not even a
pure function of the source.) A signal that cries wolf nine times out of ten trains the
operator to either ignore it — and miss the real one — or obey it — and kill 25 agents for
nothing. Both have now happened.

### And the detection is one-shot

`evaluateSkew` runs exactly once, in `cmd/argus/main.go`'s startup path. A TUI left running
for days across several `go install`s never re-evaluates. Skew is discovered whenever the
operator happens to relaunch, which is precisely why it keeps surfacing mid-incident rather
than at a moment of the operator's choosing.

### The tradeoff being protected is real and should stay protected

The supervisor is long-lived on purpose (`gotchas/daemon-rpc.md`, P1–P4). It is the
always-parent PTY owner with its own independent socket/pid/lock trio, so a daemon bounce
re-attaches to live sessions and the in-flight turn survives; only a *supervisor* restart
interrupts agents. The R/S protocol is versioned and **additive-only** specifically because
`go install` + daemon restart does not restart a live supervisor, and design §4.4 states
outright: never auto-restart a live supervisor on version skew, because that would SIGHUP
its agents. `connectSupervisor` honors this today — on protocol mismatch it logs and
proceeds within the running supervisor's capabilities.

Nothing here proposes weakening that. The proposal is to stop paying its cost nine times
out of ten for no reason, and to make the tenth time cheap.

## What Changes

Three layers, independently shippable, in increasing order of cost. The recommendation is
to ship Layer 1 alone first and re-measure before committing to Layer 2 or 3.

### Layer 1 — Make the skew signal mean something (recommended first)

- Introduce `daemon.SupervisorSurfaceVersion`, a hand-bumped integer alongside the existing
  `ProtocolVersion`, naming the observable behavior of the supervisor-resident code. It is
  reported through `Hello`/`BootInfo` next to `BinaryHash`.
- Skew is judged on **that** first. Equal surface versions ⇒ **coherent**, whatever the
  binary hashes say. The whole-binary hash is retained and still displayed, demoted to a
  tie-breaker for the "unknown surface version" (old supervisor) case, exactly as an empty
  `SupervisorHash` is treated today.
- A CI test fails a PR that touches a declared supervisor-resident path without bumping the
  constant — the same single-source-plus-drift-test pattern `internal/tui/store`'s
  `assert_test.go` and the keymap's `help_test.go` already use.
- Split the surface version into two independently-reported components so the verdict can be
  **tiered by consequence** rather than reduced to stale/not-stale:
  - **spawn surface** (`BuildCmd`, sandbox, skills/routing, secrets, cache dirs) — affects
    only sessions started *from now on*. Existing agents are untouched, so the honest
    verdict is "new agents will spawn with the previous build's spawn config; restart when
    convenient," never a mid-incident emergency.
  - **stream surface** (PTY read loop, ring buffer, session log writer, R/S handlers,
    exit-info caching) — affects live sessions. This is the only tier that justifies
    interrupting agents.
- Re-evaluate skew continuously rather than only at TUI launch: on the existing tick and on
  daemon reconnect, surfaced through the status-bar notice (15s TTL, already built), with
  the blocking modal reserved for a stream-surface mismatch at startup as today.

### Layer 2 — Shrink what lives in the supervisor at all

Move the spawn stack out of the supervisor: the daemon resolves the full command spec
(argv, env, dir, sandbox wrapper) and ships it in an expanded `StartReq`; the supervisor
forks what it is told. P1's own notes already flagged this as a deferred protocol decision
("P2 may instead carry config in an expanded `StartReq`").

By the table above this converts 15 of the 28 supervisor-resident commits into
daemon-resident ones — code that refreshes on every cheap, non-destructive daemon bounce.
It also removes the standing hazard that a stale supervisor spawns *new* agents with an old
sandbox profile, old skills injection, or old model tiering, which is a silent correctness
problem no rendering fix addresses.

### Layer 3 — Make the residual restart non-destructive

Generation-scoped supervisors with drain. A skewed supervisor is not restarted; a **new**
one is started on `supervisor-<gen>.sock` and every *new* session goes to it, while existing
sessions stay with their own supervisor until they exit naturally. The old supervisor exits
when its last session ends. No agent is ever killed for a skew.

`singletonPathsForSock` already derives the pid and lock paths from the socket path, so a
second supervisor gets its own independent trio for free; P0 already proved two locks
coexist. The work is (a) parameterizing the hard-coded `DefaultSupervisorSocketPath()` in
`runSupervisor`/`buildSupervisorStartCmd`, and (b) turning the daemon's single
`SupervisorClient` into a per-task router with a startup reattach that enumerates every live
generation.

## Impact

- Affected specs: `binary-coherence`, `daemon-lifecycle` (Layers 2 and 3 also touch
  `agent-execution`).
- Affected code, Layer 1: `internal/daemon/{binaryhash,supervisor,sessioncore}.go`,
  `cmd/argus/{main,doctor}.go`, `internal/tui` (status-bar notice + periodic re-evaluation).
- Affected code, Layer 2: `internal/agent/{agent,runner}.go`, `internal/daemon` R/S types,
  `ProtocolVersion` bump (additive).
- Affected code, Layer 3: `internal/daemon/{supervisor,client}`, `cmd/argus/main.go`.
- **No weakening of design §4.4.** Nothing here auto-restarts a live supervisor. Layer 1
  reduces how often a restart is *suggested*; Layer 3 removes the need for one entirely.
- **`argus doctor`'s exit code** stays governed by binary coherence, but the coherence
  verdict itself becomes surface-based. That is a behavior change to a scripted surface and
  needs an explicit call — see design D5.

## Non-Goals

- **Auto-restarting the supervisor when all agents are idle.** Evaluated and rejected — see
  design D1. On a machine that routinely runs 25 concurrent tasks the condition essentially
  never holds, so it would buy almost nothing while adding a way for the system to kill
  agents on its own initiative.
- **Live fd hand-off (SCM_RIGHTS) to a replacement supervisor.** Evaluated and rejected for
  now — see design D2. It would preserve the PTYs but forfeits `Cmd.Wait()`, and with it
  `ExitInfo.CleanExit()`, the predicate PR #707 and the evidence-based-completion rule
  depend on.
- **Build-time computation of the surface fingerprint.** Structurally impossible on the real
  deploy path — see design D3.
- Changing the rendering read path. `fix-log-ring-coordinate-space` (#964) and
  `fix-deferred-anchor-clobber` already cover it; this change is about process coherence.
