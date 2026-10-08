# Stop finished tasks' agent sessions

## Why

Aaron's `ps` showed about 55 live `npm exec @playwright/mcp` → `node playwright-mcp` pairs, 10 minutes to 18 days old. None of them were orphaned: every one was the MCP child of a still-running `claude`. Claude Code starts every globally configured MCP server for each session, so each lingering `claude` holds its own Playwright MCP (and memory).

The `claude` processes linger because nothing in argus ends a finished task's session:

- `task_complete`, `task_archive` (MCP and REST), the TUI archive, `hera_accept` and the gater's auto-accept only change the task row. `gotchas/hera-view.md` records "completion and detachment stay orthogonal".
- Agents are told to run argus-complete / argus-archive when done, so every finished task's agent idles indefinitely.
- `Session.Stop` signals only the agent's PID. Its MCP children rely on noticing their parent died.

## What changes

- **Process-group stop:** `Session.Stop` signals the session's whole process group: SIGTERM, then SIGKILL to the group after a short grace period. The agent is a session/group leader via the PTY's setsid, and its MCP servers share its group.
- **Finished-session reaper:** a new daemon sweeper stops the agent session of any task that is complete or archived, once that session has been idle on two consecutive ticks.
  - Waiting for idle defers an agent that completes itself (`task_complete` / argus-complete) until its turn has finished, rather than killing it mid-response.
  - It covers every path that marks a task finished, now and in future, without per-call-site hooks.
  - Its first pass after daemon startup clears the existing backlog.
- **Deliberate restarts are respected:** a session started for a task that was already finished (e.g. starting an archived task) is exempt for that session's lifetime. Every normal restart/revive path already sets the task back to `in_progress`, so it isn't finished.
- **Scope:** only sessions in the argus runner/supervisor are ever stopped. `claude` processes argus didn't spawn are never touched.

## Non-goals

- The TUI's in-process fallback runner (used only when no daemon is reachable) isn't swept. Named follow-up.
- No idle reaper for unfinished tasks (dropped by Aaron).
- PR #1044's env-tag descendant reaper is closed and superseded.
