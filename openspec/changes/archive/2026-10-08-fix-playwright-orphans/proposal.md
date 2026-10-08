# Fix orphaned Playwright browsers after agent sessions

## Why

Every agent session that runs Playwright (the `web-tests` e2e suite, or the Playwright MCP server's Chrome) can leave browser processes running after the session ends. They pile up on the host over time.

Argus ends a session by sending SIGTERM to the one PID it started (`Session.Stop`), and does nothing more when a session exits on its own. Browser processes are out of reach of both:

- Playwright starts every browser with `detached: true`. That calls `setsid()`, so the browser gets its own session and process group. Hangup and process-group signals from the agent's terminal never reach it.
- When Claude Code's Bash tool times out or is interrupted, it kills the shell but not `npx playwright test` and its workers. Those processes are reparented to launchd while the agent is still running. By the time the session ends, they are no longer its descendants.
- A session that dies abruptly (SIGTERM/SIGKILL, or a supervisor restart) never runs Playwright's exit handlers. Any MCP server or test runner still running when that happens keeps its browser alive.

Signalling the process group or walking the parent-PID tree at stop time can't catch these, because the browsers have already left both.

## What changes

- Every spawned session gets a unique per-spawn tag in its environment (`ARGUS_SESSION_TAG=<ownerPID>-<random>`). The tag is inherited by every descendant, including processes that call `setsid`, are reparented to launchd, or are Playwright browsers. Playwright passes `process.env` to the browsers it launches by default.
- When a session's process exits for any reason (stop, natural exit, kick/rerender, recycle), the runner starts a background reaper. After a short grace period that lets graceful shutdown finish, it finds every live process whose environment carries that exact tag, sends each SIGTERM, then sends SIGKILL to any still running.
- Because the tag is per spawn, a kick-restart's replacement session (same task ID, new tag) is never touched by the old session's reaper.
- The reaper never signals its own process, and the daemon/supervisor auto-start forks drop the tag. An agent that happens to auto-start the daemon or supervisor therefore can't get it reaped.
- Supervisor and daemon startup sweep: they reap every tagged process whose owner PID is dead. This covers the leak left when a supervisor restarts (every deploy) before its per-session reapers can run.
- A process whose own environment can't be read inherits the tag of its nearest tagged ancestor. macOS hides the environment of Apple platform binaries such as `/bin/sh`; Playwright's Chromium and node expose theirs (verified).
- Process enumeration is per OS: darwin uses `kern.proc.all` + `kern.procargs2`, linux uses `/proc/<pid>/environ`, and other platforms do nothing. Every failure is logged and swallowed.
- Argus's own harness: `web-tests` uses only the Playwright test runner's managed browsers, with no manual `launch()`. This was checked and needs no change. The reaper also covers a runner killed mid-run.

## Non-goals

- A process orphaned mid-session (e.g. by a Bash-tool timeout) stays alive until its session ends. That gap is accepted.
- Processes that scrub their own environment (e.g. `env -i`) are not tracked. Playwright does not do this.
- No change to Claude Code's background-session reaping (`claude stop`), which stays as is.
