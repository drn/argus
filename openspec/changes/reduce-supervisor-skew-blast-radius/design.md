## Context

Three processes load the same `argus` binary from disk: the TUI (`argus` on `PATH`), the
daemon (`~/.argus/argusd`), and the session-supervisor. `go install` replaces the file; the
TUI is relaunched by the operator and the daemon is bounced freely, but the supervisor is
deliberately long-lived — it is the always-parent PTY owner, and killing it SIGHUPs every
agent and flips their `InProgress` rows to `InReview`.

Everything about that design is intentional and documented (`gotchas/daemon-rpc.md`, P1–P4;
design §4.3/§4.4): independent socket/pid/lock trio so a daemon bounce cannot touch it, an
additive-only versioned R/S protocol so a newer daemon can drive an older supervisor, and an
explicit prohibition on auto-restarting a live supervisor on version skew. `connectSupervisor`
implements that prohibition today — protocol mismatch logs a line and proceeds.

What is *not* intentional is the quality of the skew signal. `daemon.BinaryHashFile` hashes
the whole binary, so it reports staleness for every build regardless of whether the
supervisor's own behavior changed. Measured over three months of `master`: 295 commits, 13
touching the supervisor's PTY/stream core, 28 touching anything supervisor-resident. The
signal is therefore right about 1 time in 10 and the remedy it points at costs an entire
fleet of running agents.

The 2026-09-01 incident is the worked example. `argus doctor` reported the supervisor stale
(`90306aea` vs `c7326dcf`) while the TUI showed garbled Hera panes. The two are unrelated:
diffing the builds shows the only supervisor-side change was a spawn-time cache-dir env
export, and the garbling root-caused to a TUI width-drift bug. A supervisor restart would
have killed every agent and fixed nothing. Two earlier occurrences *were* genuine skew, so
this is not a case of the check being pointless — it is a case of it being unable to tell the
operator which situation they are in.

## Goals / Non-Goals

**Goals:**

- The skew verdict distinguishes "the supervisor is running different *behavior*" from "the
  binary file changed", and reports coherent in the ~90% case where only the latter is true.
- When the supervisor genuinely is behind, the verdict says what that costs — new spawns only,
  or live sessions — so the operator can decide without reverse-engineering a diff.
- Skew is discovered continuously, not only at TUI launch.
- The long-lived-supervisor tradeoff and the §4.4 no-auto-restart rule survive intact.

**Non-Goals:**

- Automatic restarts of a live supervisor under any condition (D1).
- Preserving PTYs across a supervisor replacement via fd hand-off (D2).
- Any change to the terminal read/render path.
- Making a stale supervisor *correct*. It stays behind until replaced; the aim is to make
  that state honestly reported and cheap to leave, not invisible.

## Decisions

**D1. Reject "auto-restart when all agents are idle."** This was the obvious reading of "make
it self-healing" and it does not survive contact with the machine it is for. `RunningAndIdle`
regularly reports ~25 live sessions; a moment where *every* one is simultaneously idle and the
operator would not mind losing their PTYs essentially does not occur. So the trigger would
almost never fire — and on the rare occasion it did, the system would be killing agents on its
own initiative, which is a strictly worse failure mode than the status quo (a human deciding).
The problem is not that the human is in the loop; it is that the human is being asked the
question nine times too often and given no basis to answer it. Layer 1 addresses that directly.
Rejected, not deferred.

**D2. Reject fd hand-off (SCM_RIGHTS) for a zero-downtime supervisor swap, for now.** Passing
the PTY master fds to a replacement over the existing Unix socket would keep the agent
processes alive across a supervisor upgrade — the nginx/tmux pattern, and genuinely the most
elegant answer to "restarting super kills all running agents." It fails on exit accounting:
the agents were forked by the *old* supervisor, so the new one cannot `Cmd.Wait()` them and
cannot observe the real exit status. `ExitInfo.CleanExit()` — the predicate that distinguishes
a clean self-exit from a crash, which PR #707 and the evidence-based-completion rule
(`reconcile → InReview, never Complete`) are built on — would silently degrade to "we know it
exited, not how." On macOS `kqueue`'s `NOTE_EXIT` reports the event but not the status, so
there is no cheap substitute. A husk process that stays alive purely to `Wait()` and relay
would work but reintroduces exactly the long-lived-stale-process problem this change exists to
reduce. Revisit only if Layer 3 proves insufficient.

**D3. The surface fingerprint must be a plain Go constant, not a build-time computation.** The
attractive version — hash the source of a declared package set at build time and inject it with
`-ldflags -X` — is structurally impossible here. `.iris.toml`'s build is `make install-signed`,
whose body is a bare `go install ./cmd/argus`; that is the exact file the daemon runs, and it
accepts no ldflags. A `go:generate` step would work but drifts silently whenever someone forgets
to run it. So: a hand-bumped `SupervisorSurfaceVersion` constant living next to `ProtocolVersion`
— which is already hand-bumped by the same judgment call — guarded by a CI test that fails a PR
touching a declared supervisor-resident path without bumping it. This repo already relies on that
single-source-plus-drift-test shape (`internal/tui/store/assert_test.go`, the keymap's
`help_test.go`), so it is a known-good pattern rather than a new one. The cost is honest: the
constant reflects human judgment about whether a change is observable, and a wrong call yields a
false *negative* — a genuinely-changed supervisor reported coherent. The CI guard makes silent
omission hard; a deliberate "this is a comment-only change" call is exactly the judgment
`ProtocolVersion` already asks for.

**D4. Two surface components, not one, because the consequences are different in kind.** A
spawn-surface change (`BuildCmd`, sandbox, skills, secrets, cache dirs) cannot affect a single
running agent — it is read only when a session starts. A stream-surface change (PTY read loop,
ring buffer, session-log writer, R/S handlers, exit-info caching) affects every live session.
Collapsing them loses the one piece of information that determines whether interrupting 25
agents is warranted. This also makes Layer 2 measurable: moving the spawn stack to the daemon
should drive the spawn-surface component's bump rate to zero.

**D5. The coherence verdict changes meaning, and `argus doctor`'s exit code follows it.** Today
the exit code is governed by whole-binary coherence. Under Layer 1 a supervisor with a differing
binary hash but an identical surface version is *coherent* and the exit code becomes zero where
it is currently non-zero. That is the entire point, but it is a change to a scripted surface and
must be called out rather than slipped in. The displayed rows keep showing both hashes and both
surface versions, so nothing becomes less inspectable — only the pass/fail line moves. The
existing advisory-only checks (Stop-hook registration, diligence-profile library) stay advisory
and continue not to affect the exit code.

**D6. Continuous re-evaluation goes to the status bar, not a modal.** The startup modal is
correct for its moment — the operator is at the keyboard, nothing is in flight, and a blocking
choice is cheap. Firing a modal mid-session because a `go install` happened in another terminal
would interrupt exactly the work this change is trying to protect. The status-bar notice already
has a 15s TTL and a lazy revert, so it costs nothing new. Reserve the modal for a stream-surface
mismatch detected at startup, which is where it already lives.

**D7. Layer 3's generational drain is preferred over both D1 and D2 as the eventual answer.**
It reaches the same "no agent dies for a skew" outcome as fd hand-off while keeping every
supervisor the true parent of its own children, so `Cmd.Wait()` and `CleanExit()` are untouched.
Mixed generations are safe because a session is wholly owned by one supervisor: its ring buffer,
its session log, and its exit accounting all live in the same process, and the TUI's read path
addresses the log by task ID, never by supervisor. The cost is that the daemon's single
`SupervisorClient` becomes a per-task router and startup reattach must enumerate generations —
real work, but contained, and `singletonPathsForSock` already gives each generation its own
pid/lock trio for free.

## Risks / Trade-offs

- **A wrong surface-version judgment yields a false negative** — the worst outcome this change
  can produce, and strictly worse than today's false positives, because it is silent. Mitigated
  by the CI path guard (omission is caught mechanically; only a deliberate "not observable" call
  can be wrong) and by keeping the whole-binary hash visible in `doctor` output so the raw fact
  is never hidden.
- **Layer 2 relocates secret resolution.** `add-secrets-resolver-registry` deliberately resolves
  `keychain://`/`op://` sources inside whichever process owns `BuildCmd`, precisely so a
  credential exported into the daemon's environment never has to reach the supervisor. Moving
  `BuildCmd` to the daemon inverts that and would put resolved secret *values* into a `StartReq`
  payload. The mitigation, and the shape Layer 2 should take, is to ship the env map with
  **descriptors unresolved** and leave the (small, stable) resolver in the supervisor — keeping
  the churn-heavy command construction in the daemon and the security-sensitive resolution where
  it already is. This needs to be settled before Layer 2 is scoped, not during it.
- **Layer 3 multiplies long-lived processes.** A machine that upgrades often while holding
  long-running agents could accumulate several draining supervisors. Bounded in practice by
  sessions ending, but it needs an explicit cap and a `doctor` row enumerating live generations,
  or it trades one invisible-stale-process problem for several.
- **Layer 1 alone does not fix a genuine stream-surface skew.** It correctly identifies it and
  correctly says a restart is warranted; the restart still kills agents until Layer 3 lands. This
  is an accepted intermediate state — it converts an unknowable coin flip into a known, justified
  cost, which is the actual complaint.
- **Doing nothing is also a position** and should be considered against Layer 1's cost. It is
  rejected because the failure mode is already established: three-plus incidents, at least one
  unnecessary fleet-killing restart avoided only by diffing two build SHAs by hand, and a signal
  the operator is actively learning to distrust.

## Open Questions for Approval

1. **Ship Layer 1 alone and re-measure, or commit to 1+2 up front?** Recommendation: Layer 1
   alone. It is small, reversible, and its own telemetry (how often the surface version actually
   bumps) is the evidence that decides whether Layer 2 is worth a `ProtocolVersion` bump.
2. **Is the `doctor` exit-code change (D5) acceptable?** It is the point of the change, but if
   anything scripts `argus doctor`, it wants a heads-up.
3. **Layer 2's secret-resolution split** — descriptors-over-the-wire with resolution left in the
   supervisor, as the risk section proposes, or accept resolved values in `StartReq`?
