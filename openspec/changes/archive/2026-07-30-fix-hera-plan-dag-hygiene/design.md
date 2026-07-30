## Context

Three hera plan-DAG / rail data-hygiene bugs were independently verified against the live `~/.argus/data.sql`. They are unrelated defects with different root causes — this design deliberately does NOT try to unify them under one fix.

- **Bug A** (`ListHeraPlannedNodes`, `internal/db/hera_plan.go:359-371`): the query filters only the node's own `archived_at`/`cancelled_at`, never the parent orchestrator's `archived_at`/`nuked_at`. Roles 343 ("4a-flex", orch 47 "plan-view-dogfood") and 358 ("3a-integrate", orch 49 "dag-states-test") have retried every ~60s heragater tick for over a month behind orchestrators archived+nuked on 2026-06-20.
- **Bug B** (`materializeNode`, `internal/heragater/heragater.go:439-480`): role 184 ("2a-team", live orchestrator 8 "sherlock-mvp", zero remaining blockers) has a blank `argus_project`. `agent.CreateAndStart` (`internal/agent/create.go:119`) errors `project %q not found in config`, and the gater retries forever with no escalation. Unlike Bug A, this is genuine pending work — the fix must not guess a project or cancel it.
- **Bug C** (`internal/tui/hera/model.go:1043`): freelance roles 813/814 render in the flat top-level Freelance section (which a `kind=freelance` role only escapes by being archived, unlike `kind=worker` which always nests). Their data chain — role, orchestrator, binding, task — is fully intact; they were simply never archived once their work finished (task 1782110807411961000 in_review with a merged PR, task 1781194310470051000 complete). ~150 other historical roles show the identical "task finished, binding still open" shape and render correctly because they're `kind=worker`.

## Goals / Non-Goals

**Goals:**

- Bug A: a planned node under an archived/nuked orchestrator must stop being polled/retried, both prospectively (new archives/nukes) and for rows that already exist (the defensive list-query filter covers both without a data migration).
- Bug B: a node that can never materialize must not retry in total silence forever — it should escalate to the coordinator after a bounded number of consecutive failures, with zero guessing at the actual fix.
- Bug C: understand and document why this shape occurred, and clean up the two known-broken rows, without changing rendering logic that is behaving as designed.
- Retroactively repair the specific broken rows already in the live DB (Part 2), through the daemon's own `*db.DB`, not hand-edited SQL.

**Non-Goals:**

- Guessing or auto-assigning `argus_project` for role 184, or auto-cancelling it. It has zero remaining blockers and is genuine pending work; only a human can say what it should target.
- Auto-archiving freelance roles when their binding ends. This would be a real behavior change (see Decision: Bug C below) and is out of scope for a hygiene fix targeting three specific verified bugs.
- Any change to `hera_bindings` uniqueness semantics, the plan-DAG's cycle detection, or fan-in branch resolution — all unrelated to these three bugs.

## Decisions

### Bug A: defensive filter in the read path, not just a write-time cascade

Two independent fixes are needed, not one:

1. `ArchiveHeraOrchestrator`/`NukeHeraOrchestrator` cascade-cancel their still-planned (never-materialized) child roles at the moment of archive/nuke. This prevents the bug from recurring for any FUTURE archive/nuke.
2. `ListHeraPlannedNodes` additionally joins `hera_orchestrators` and requires `archived_at IS NULL AND nuked_at IS NULL` on the parent.

(1) alone does not fix rows 343/358, which predate the fix — a one-time data migration would be needed to make (1) retroactive. (2) alone works for existing AND future rows without any migration, but leaves a defensive gap if a future code path other than Archive/Nuke ever flips those columns without going through the cascade. Both together mean the invariant holds from either direction: the write path prevents the state from occurring, and the read path tolerates it if it ever does anyway (matching this codebase's existing "list queries are the belt-and-braces layer" pattern already used for nuked orchestrators — see `internal/tui/hera/model.go`'s defense-in-depth comment on `o.NukedAt`).

Alternative considered: only fix the read-path filter and skip the cascade-cancel. Rejected — `ListHeraPlannedNodes` would then silently and permanently exclude cancellable dead nodes from ever appearing anywhere (including future plan-view tooling that might want to show "N nodes cancelled because their orchestrator ended"), leaving them un-cancelled forever in a state that looks alive everywhere except the one query that matters today. Stamping `cancelled_at` is the more honest representation and costs one extra `UPDATE`.

### Bug B: bounded consecutive-failure escalation, mirroring `agent.EscalateParkedSelection`

`heragater.Watcher` already has a proven shape for "this transient-looking failure has actually been going on for a suspiciously long time, tell someone" — `holdAndPing`'s per-(node,blocker) dedup map, and (in a different package) `agent.EscalateParkedSelection`'s bounded consecutive-tick counter (`NeedsInputEscalationTicks=8`, see `context/knowledge/gotchas/events.md` BUG-029/BUG-060). This change adds the same shape for materialize failures: an in-memory `map[int64]int` counting consecutive failures per node id, and a `map[int64]bool` recording that the one-time escalation ping has already fired for that node (so it does not spam every tick after crossing the threshold — matching `holdAndPing`'s ping-once contract). Both maps are swept each tick for any node id no longer in the planned set (materialized, cancelled, or removed), mirroring `rearmHeldPings`'s cleanup of `heldPings` — an unbounded-forever-growing map would be exactly the kind of hygiene defect this change exists to fix.

Threshold: 5 consecutive ticks (`materializeFailureEscalationTicks`), i.e. ~5 minutes at the gater's 1-minute tick interval. This is deliberately smaller than `NeedsInputEscalationTicks` (8) — that counter guards against isolated torn reads/blink-off frames on a fast, sub-second polling loop, a noise source that does not apply here. A materialize failure is a fully synchronous, deterministic result (config resolves or it doesn't) with no torn-read risk, so the threshold only needs to rule out "the coordinator is mid-recovering something and the very next tick will succeed" — 5 minutes is generous for that and still surfaces a permanently-broken node well before "over a month," the actual duration Bug A's rows sat silent.

On success, both maps are cleared for that node id (a node that fails a few times then succeeds — e.g. because a project config gap was fixed concurrently — should not carry a stale escalated-flag forward if it is later re-planned).

Alternative considered: auto-cancel the node after N failures, treating repeated failure as equivalent to "this was a mistake." Rejected per the explicit brief: node 184 has zero remaining blockers and is verified pending work; auto-cancelling any node exceeding the threshold could silently discard real DAG state that a human just hasn't gotten to yet. Escalate-and-wait is the correct action; auto-remediation is not.

Alternative considered: make the escalation notice re-fire periodically (like a nagging reminder) rather than once. Rejected for consistency with the existing `holdAndPing`/`pingFanIn` one-shot-or-dedup conventions already established in this file — a coordinator that ignores the first escalation notice pressing it again every tick adds noise, not information; the node stays visible in the plan-DAG view regardless.

### Bug C: no code change — this is a data-hygiene finding, not a display defect

The flat-Freelance-section rule (`role.Kind == HeraKindFreelance && role.ArchivedAt == nil && o.ArchivedAt == nil`) is intentional, documented behavior (`context/knowledge/gotchas/hera-view.md`: "Freelance section"). The bug is that nobody archived roles 813/814 once their work finished — an operational gap, not a logic error. No `Clean`/`Prune`/`Sweep`-named function touching hera data exists anywhere in this codebase; whatever produced this specific pair's un-archived state was very likely a manual/ad hoc action outside argus, not an argus feature bug.

Considered and rejected: auto-archiving a freelance role once its binding ends (mirroring how a worker role's nesting is unconditional regardless of archived state, an auto-archive would make freelance behave the same way from the rail's perspective). Rejected here because it is a genuine behavior change with its own design surface (when exactly does "binding ended" count — immediately, after a grace period, only on terminal task status?) that the three-bug brief explicitly scoped OUT: Bug C's fix, per the task brief, is Part 2 (data cleanup) only. If this shape recurs, it is a separate, affirmatively-scoped follow-up change, not smuggled into this one.

## Risks / Trade-offs

- [Bug A's JOIN adds a small query-plan cost to `ListHeraPlannedNodes`, called every gater tick] → negligible: `hera_orchestrators` is tiny (one row per orchestrator ever created) and the query is already `O(planned nodes)`; adding an indexed-PK join does not change its asymptotic cost.
- [Bug B's escalation maps live only in the in-process `Watcher` struct — a daemon restart forgets any in-flight failure streak] → acceptable: the streak is a "how long has this been broken lately" heuristic, not durable state; a restart simply restarts the count, and a permanently-broken node re-crosses the threshold within another 5 ticks.
- [Cascade-cancel could theoretically race a concurrent `hera_plan_node` create on the same orchestrator between the archive UPDATE and the cascade UPDATE] → both statements scope by `orchestrator_id` and the cascade query re-checks `archived_at IS NULL AND cancelled_at IS NULL AND NOT EXISTS (binding)` at execution time; a create that lands in that narrow window would either land before the cascade (and get cancelled, correct) or after (and become an orphaned planned node under an archived orchestrator — but Bug A's own read-path filter in `ListHeraPlannedNodes` already covers that case defensively, so the DAG-hygiene invariant holds either way).

## Migration Plan

**Code (this PR):** no schema change. `cancelled_at`, `archived_at`, `nuked_at` are pre-existing nullable columns (see `context/knowledge/gotchas/orchestration.md` "Hera schema / store (M1)" and "Hera living plan-DAG").

**Data (Part 2, run once against the live `~/.argus/data.sql`, through the daemon's own `*db.DB`, not raw `sqlite3 UPDATE`):**

1. Fresh backup: `cp ~/.argus/data.sql ~/.argus/data.sql.bak-<timestamp>` immediately before the mutation (a pre-investigation backup already exists at `.bak-20260729-213549`, but a fresh one is taken right before the actual write).
2. `CancelHeraPlannedNode(343)`, `CancelHeraPlannedNode(358)` — the exact pre-existing store method Bug A's own fix relies on; this migration is effectively "run the same cascade the code fix would have run, had it existed a month ago."
3. `ArchiveHeraRole(813)`, `ArchiveHeraRole(814)` — pre-existing store method.
4. `EndHeraBinding(803, endReason)`, `EndHeraBinding(804, endReason)` (or the equivalent store call — see `internal/db/hera.go` for the exact signature) with an end reason describing "task finished, binding never closed."
5. Node 184 is explicitly excluded — no mutation touches it.
6. Verify via `sqlite3 -readonly ~/.argus/data.sql`: 343/358 no longer satisfy `ListHeraPlannedNodes`'s WHERE clause; 813/814 have non-null `archived_at`; bindings 803/804 have non-null `ended_at`; role 184 is unchanged.

**Rollback:** restore from the fresh pre-mutation backup; all four mutations are simple column stamps with no cascading side effects (cancel/archive do not touch bindings; ending a binding does not touch the role), so no compensating script is needed beyond a straight file restore.

## Open Questions

- What `argus_project` should role 184 ("2a-team" under "sherlock-mvp") target? Left to Aaron — reported back via `hera_send` as an open question, not resolved in this change.
