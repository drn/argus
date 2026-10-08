# Design

## D1. One daemon sweeper instead of per-call-site stops

Tasks become complete or archived through many entry points: MCP, REST, the TUI, hera_accept, the gater, the exit hook, prune. Hooking each one would drift, and none of them could safely stop the caller's own session mid-tool-call. A daemon loop (`runFinishedSessionReaper`, 10s tick) reads the runner's running/idle sets and the task rows instead.

## D2. Idle on two consecutive ticks

A finished task's session is "armed" on the first tick it is idle (`RunningAndIdle`: 3s without output, no kick pending). It is stopped on the next tick if it is still idle and still finished. Any non-idle tick disarms it.

This gives at least ~10s of quiet before a stop, so an agent that just called `task_complete` on itself finishes its reply first. The same applies to a worker answering `hera_accept`'s check-in.

## D3. Transition semantics with an exemption for deliberate restarts

The sweeper keeps per-task in-memory state for running sessions only. A session first seen while its task is already finished is exempt for its lifetime, except on the daemon's first tick, which is the backlog sweep. A task that becomes finished while its session is known is eligible. A task that goes back to unfinished clears its exemption.

Restart and revive paths set `in_progress`, so they aren't affected. The exemption only matters for starting an archived task in place. After a daemon restart, such a session is swept once idle (accepted edge).

## D4. Process-group kill in Session.Stop

The pty library starts the agent with setsid, so pgid == pid. MCP servers spawned over stdio inherit that group.

- `Stop` sends SIGTERM to `-pgid`, then a goroutine sends SIGKILL to the group after 5s.
- ESRCH (group already gone) is fine.
- Guards: pgid must be > 1, and if it equals argus's own pgid only the PID is signalled.

This applies to every Stop caller: kick, recycle, hide, nuke, delete, prune and the reaper. None of them want the agent's MCP children to outlive it.

## D5. Supervisor surface

`session.go` is a stream-surface file, so `SupervisorStreamSurface` is bumped to 7. A stale supervisor still stops only the PID. The reaper itself is daemon-side, so it works against a stale supervisor too, stopping via the existing Stop RPC.
