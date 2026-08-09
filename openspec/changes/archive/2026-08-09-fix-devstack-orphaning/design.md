## Context

Argus creates a git worktree per task and lets the agent session run whatever it wants inside it — including, for projects that use devbox, `devbox services up` to bring up mysqld/redis/postgres/caddy for local dev. That launch is entirely outside argus's knowledge: no argus code invokes `devbox` or `process-compose` anywhere in this tree (confirmed by search). When the worktree is later removed, nothing stops that dev stack — it keeps running, reparented to launchd the moment its launching shell exited (process-compose's own designed persistence behavior, not a bug), now pointed at a directory that no longer exists.

Six call sites in this codebase remove a worktree (TUI delete, REST delete, hera cascade-nuke, both prune paths, orphan sweep, and `CreateAndStart`'s failure-unwind), and all six already funnel through one function: `agent.RemoveWorktree` (`internal/agent/cleanup.go`). All six already run off the UI/request thread.

## Goals / Non-Goals

**Goals:**

- Guarantee that stopping a worktree's dev stack happens automatically as part of worktree removal, for every removal path, with no new call site to remember.
- Make the stop mechanism reliable without depending on process-compose's own supervisor-to-children cascade (empirically not trustworthy — see Decisions).
- Surface any already-orphaned dev-stack process (predating this fix, or created by a path outside argus) via a read-only `argus doctor` check.

**Non-Goals:**

- Fixing "why process-compose reparents to launchd on shell exit" upstream — that's process-compose's intended persistence model (a dev stack is supposed to survive your shell closing), not a bug to remove.
- General worktree hygiene (parked/abandoned worktrees piling up because the user hasn't archived them) — out of scope; this only concerns worktrees that DO get cleaned up.
- Tracking dev-stack lifetime via a new pidfile/socket that argus writes. Investigated and rejected — see Decisions.
- Auto-killing orphans found by the doctor check — doctor is strictly read-only by its own doc comment (`cmd/argus/doctor.go`); this stays advisory only.

## Decisions

**Detect and stop via the OS process table directly, not via the `devbox` CLI.**
`devbox services stop -c <dir>` looked attractive — devbox derives a per-project control port purely from the project path (confirmed via `devbox services pcport -c <dir>` against a live instance, no state file needed), so in principle it could gracefully stop a specific worktree's stack with no tracking of our own. But an empirical test running it against a worktree whose devbox environment wasn't fully materialized hung for 15+ seconds on "Ensuring packages are installed..." before being killed. Since this step must run unconditionally on every worktree removal (including ones with no dev stack at all), an operation that can silently stall for an unbounded time on some fraction of worktrees is not acceptable. `pgrep`-based process discovery is a local, near-instant, no-network operation with no such failure mode, so it is used instead — for both detection and termination.

**SIGTERM every matched process directly; do not rely on the process-compose supervisor to cascade the signal to its children.**
The original investigation killed a process-compose supervisor and observed a mysqld child die but a sibling redis-server and caddy under the same supervisor survive, needing direct signals. Rather than special-case "signal the supervisor, hope, then clean up stragglers," every matched process (the supervisor and any service binaries) is SIGTERM'd in the same pass. This makes the fallback SIGKILL sweep the *only* thing doing extra work, not the primary mechanism — simpler and empirically grounded in what was actually observed to work.

**No pidfile / socket tracking for process-compose's lifetime.**
The handoff's remaining-work list suggested having whatever launches `devbox services up` also write a pidfile so cleanup has something reliable to target. Rejected: the worktree path is already embedded in every observed process's command line (`-f <worktree>/.devbox/virtenv/<service>/process-compose.yaml` for process-compose; `--datadir=<worktree>/.devbox/...` for mysqld; etc.) — a reliable, zero-new-state handle already exists. Adding a pidfile would mean also making argus responsible for writing it at dev-stack-launch time, but argus is never present at that moment (the agent's own shell runs `devbox services up`, not argus) — there's no natural hook to write it from. Confirmed against Aaron's approval to skip this.

**Match on a whole path segment, not a bare substring, when deciding whether a process belongs to the worktree being removed.**
A worktree named `Sherlock/3b` is a string-prefix of a sibling worktree `Sherlock/3b-more`. A bare `strings.Contains` match would kill the wrong worktree's dev stack when both exist. This mirrors the exact class of bug `gotchas/worktree.md` already documents for the orphan sweeper (`firstKnownDescendant`) — same failure shape, same fix shape (compare the cleaned, exact path).

**Doctor check follows the existing `StopHookStatus`/`ProfileLibraryStatus` tri-state shape (Found/None/Unknown), kept pure and I/O-free in `internal/doctor`.**
`internal/doctor`'s package doc explicitly requires it stay "pure, I/O-free" — all gathering (the pgrep scan, the `os.Stat` existence check per candidate) happens in `cmd/argus/doctor.go`, and `internal/doctor` only classifies already-gathered data. This matches `DiagnoseStopHook`/`DiagnoseProfileLibrary` exactly and keeps the new check unit-testable without spawning processes.

## Risks / Trade-offs

- **[Risk] The SIGTERM→wait→SIGKILL sweep adds a fixed grace-period delay (~5s) to every worktree removal that has a live dev stack.** → Mitigation: all six call sites already run this off the UI/request thread and already expect worktree cleanup to take "seconds" (existing code comments say so at every call site); a bulk prune already serializes per-repo, and this doesn't change that. No dev-stack-less worktree pays any extra latency beyond one fast `pgrep` call.
- **[Risk] `pgrep` is unavailable on Windows.** → Mitigation: `ScanDevStackProcesses` degrades to "nothing found" when the scan can't run, matching the existing best-effort convention throughout `cleanup.go` (failures are logged, never block removal). devbox/process-compose dev stacks aren't a realistic Windows scenario in the first place.
- **[Risk] A process matching one of the five known names but belonging to something unrelated to devbox could theoretically share a worktree path substring by coincidence.** → Mitigation: extremely unlikely in practice (the path pattern requires `/.argus/worktrees/<project>/<task>` or `/.claude/worktrees/<project>/<task>` literally in the command line), and the blast radius of a false positive is a SIGTERM/SIGKILL to a process that was, by construction, already about to have its cwd/data directory deleted out from under it.

## Migration Plan

No data migration. Purely additive behavior change plus one new advisory diagnostic. Ships as a normal PR; no flag needed (breaking-changes-are-fine, single-user policy).

## Open Questions

None outstanding — design approved by Aaron via the coordinator (include doctor check, skip pidfile tracking).
