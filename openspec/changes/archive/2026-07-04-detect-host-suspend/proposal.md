## Why

**A hera coordinator cannot distinguish "the laptop was asleep" from "my worker is stuck", so it misjudges concurrent worker silence as staleness and spawns duplicate retries.**

When the host sleeps, EVERY process on it freezes identically — the argus daemon, the coordinator's own PTY session, and every worker's PTY session — then all resume together on wake. Nothing about that gap is observable to a coordinator agent: from its point of view a worker simply "hasn't reported in N hours", which is indistinguishable from a genuinely stuck agent.

Live repro (reported by the user): a coordinator running a plan-DAG for a fork-naming feature came back from a laptop-sleep gap, concluded several of its own workers were dead, and spawned duplicate "retry" plan nodes — the rail showed `2a-data-model`, `2a-data-model-retry`, `2a-data-model-retry2`, `4b-fork-naming`, `4b-fork-naming-retry`, `4b-fork-naming-retry2`, etc. The workers were never stuck; they were asleep along with everything else and would have reported back on their own. The user's read is exactly right: "when my laptop opened up the coord looked at the clock and decided the agent was dead and so started anew, despite the agent also waking up at that time."

There is NO existing code-level timeout / staleness / auto-retry mechanism anywhere in the codebase — the heragater's `Tick()` is purely status-driven (a planned node materializes only when every blocker reaches hera role-status `done`; there is no elapsed-wall-clock check). So the duplicate `-retry` nodes are the COORDINATOR AGENT's own judgment call. The fix is not to add machine auto-correction; it is to give the coordinator a trustworthy SIGNAL that the host was asleep — right when it wakes and might be tempted to misjudge — instead of leaving it to guess from elapsed silence alone.

## What Changes

- **A new daemon-owned background watchdog detects host suspend.** It ticks on a fixed cadence and, each tick, compares the current WALL-CLOCK time against the previous tick's. A gap far larger than the tick interval means the host was suspended and resumed (laptop sleep / hibernate / VM pause), since the daemon's own tick loop was frozen along with everything else.
- **On detection, the daemon broadcasts a system note to every currently-running task**, reusing the exact `SystemTaskID` / `KindNote` / `InsertSystemMessage` primitive that `sendBounceSignals` (ARGUS_BOUNCED) already uses — a durable inbox message the agent sees on its next inbox poll, NOT a new MCP surface it must remember to query. The note carries the approximate gap duration and explicit guidance: do not treat worker silence spanning that gap as staleness; give workers a chance to report back before creating a duplicate retry/plan node.
- **Notify, don't auto-correct.** The daemon changes no task or role state. It only informs; the coordinator decides.
- **The watchdog runs unconditionally** (independent of `cfg.Hera.Enabled`): a bare-worker coordinator with no plan-DAG misjudges silence in exactly the same way, and the note is cheap and harmless for a non-coordinator to receive.

## Capabilities

- `daemon-lifecycle` — the daemon detects host suspend from a wall-clock gap between watchdog ticks and posts an advisory system note to every running task; the notice fires at most once per suspend, skips the first post-start comparison (no baseline), and never mutates task or role state.

## Out of scope

- No new MCP tool or `hera_*` surface — the whole point is the coordinator does not have to ask; the note lands in its inbox via the same mechanism as ARGUS_BOUNCED.
- No auto-retry, auto-cancel, timeout, or staleness machinery of any kind. Detection is advisory only.
- The separate, unrelated `1a-tests` node that failed immediately with a concrete "UNIQUE constraint failed" DB error is a different bug (a real failure, not a silence/staleness misjudgment) and is not addressed here.
- No change to reliable-pane-delivery / the notifier: the note is inbox-durable exactly like ARGUS_BOUNCED, not force-injected into the PTY.
