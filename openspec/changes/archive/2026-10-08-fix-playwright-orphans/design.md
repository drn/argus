# Design: session-tag descendant reaper

## D1. Environment tag over process group / ppid walk

Rejected alternatives:

- `kill(-pgid)`: Playwright browsers call `setsid` (`detached: true`), so they are in their own group.
- Walking the parent-PID tree at stop time: processes orphaned mid-session (Bash-tool timeout) are already reparented to launchd (ppid 1).

Environment variables survive `setsid`, reparenting, and `exec`. Same-user processes' environments can be read without privilege (darwin `KERN_PROCARGS2`, linux `/proc/<pid>/environ`). This makes the tag a lightweight, cgroup-like membership marker.

## D2. Per-spawn tag, not `ARGUS_TASK_ID`

A kick/rerender restart reuses the task ID and starts within milliseconds of the old session exiting. Reaping by task ID would kill the replacement agent itself. `StartSession` mints a fresh random tag per spawn and stores it on the `Session`. The reaper matches only that exact `KEY=value` entry.

## D3. Hook point: the runner's exit goroutine

`Runner.Start`'s `<-sess.Done()` goroutine is the one place every exit path passes through: explicit Stop, StopAll, natural exit, kick, and recycle. It runs in whichever process owns the PTY (supervisor or in-process runner). The reaper is launched with `go` so it never delays `onFinish` or a kick-restart.

## D4. Grace then TERM then KILL

The reaper waits `sessionReapGrace` (2s) before scanning, so a Playwright MCP server that saw stdin EOF can close Chrome cleanly first. It then sends SIGTERM to every match, waits `sessionReapKillAfter` (3s), and re-scans. Anything still carrying the tag gets SIGKILL. Re-scanning instead of signalling remembered PIDs avoids hitting a recycled PID.

## D5. Safety exclusions

- Never signal `os.Getpid()`.
- The daemon and supervisor auto-start forks (`internal/daemon/client` autostart, `cmd/argus` supervisor auto-start) strip `ARGUS_SESSION_TAG` from the child environment. An agent that runs `argus` and triggers auto-start therefore can't tag the long-lived daemon/supervisor for reaping.
- Empty tag means no scan.

## D6. Test seams

The enumerator, signaller, liveness check and sleep live in a `reapHooks` struct behind an `atomic.Pointer`. Session-exit goroutines from unrelated tests read it asynchronously, and the hooks are disabled under `go test` unless a test installs fakes. The reaper body is pure logic over them. The real darwin/linux enumerators get a live test: spawn `sleep` with a unique tag and assert it is found, then reaped. This is guarded by `testing.Short()`.

## D7. Supervisor surface

The edited files (runner.go, session.go, supervisor.go) are on the stream surface, so bump `SupervisorStreamSurface` (v7) and add the new files to its path list. A stale supervisor simply doesn't tag or reap, which is the old behavior and fails open.

## D8. Startup sweep keyed on owner PID

The tag is `<ownerPID>-<random>`. A sweep at supervisor and daemon startup reaps tags whose owner PID is dead, which is exactly the leak left by a runner that died with live sessions. Tags owned by a live process are skipped: this process, a live supervisor seen from the daemon, the test server, or a TUI fallback runner. That makes the sweep safe to run from any startup, with no knowledge of which sessions are live. PID reuse can only cause a miss (a recycled owner looks alive), never a wrong kill.

## D9. Ancestor tag inheritance

macOS hides KERN_PROCARGS2 env for Apple platform binaries (`/bin/sh`, `/bin/sleep`), even to the same user. Node and Playwright's chrome-headless-shell expose it (verified live). The darwin enumerator therefore also records PPIDs, and gives an unreadable process the tag of its nearest tagged ancestor.
