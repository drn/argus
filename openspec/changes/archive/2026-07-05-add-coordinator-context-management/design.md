## Context

Hera coordinators are long-lived: unlike a worker (spawned for one slice of work, disposable when done), a coordinator persists for an entire multi-stage orchestration and personally accumulates every token it reads, delegates, or relays along the way. In practice this means coordinators routinely grow into the hundreds of thousands of tokens of carried context before anyone notices — the single largest driver of orchestration spend today. Bounce-recovery (killing a coordinator's session cleanly and restarting it without losing its place) was deliberately not ported from the old external hera plugin into native argus hera, so there is currently no primitive for this at all — a bloated coordinator just rolls on until it hits Claude Code's own auto-compaction wall.

Two existing signals were evaluated as candidates for reuse and found to be the wrong tool for this job:

- **`role_status`** (`idle`/`working`/`blocked`/`done`/`failed`) is a DB-CHECK-constrained enum wired into real task-rollup behavior (`RollHeraWorkerToReview` on `done`). It is deliberately left unchanged by this design — extending it would require a schema migration and touches rail-icon and gater code for a routing benefit that mostly doesn't exist (see Decision 2).
- **The `(?)` needs-input rail icon** (`internal/agent/needsinput.go`) is a PTY-output-scrape heuristic built for a human glancing at the rail. It has zero wiring into hera messaging or `role_status`, and — importantly — a coordinator has no way to see it either (a coordinator is itself just another agent pane; it only ever learns things through `hera_inbox`/`hera_tree_updates`). Building a bridge from this signal into hera coordination was considered and rejected (Decision 3): the coordinator's realistic response doesn't actually depend on *why* a worker went quiet, and a bridge with no matching "resolved" signal would go stale the moment a human answers the prompt directly (which they already can, via the rail).

Explicitly out of scope for this change, surfaced during design but belonging elsewhere:

- **Model/effort selection for the coordinator role** — owned by the existing diligence-profile/archetype machinery (a sibling chunk in this same model-tiering effort). This design assumes whatever model a coordinator runs on, and does not add a context-window knob.
- **Plan-DAG fan-in branch reconciliation** — a real, separately-scoped gap found while discussing this design (the gater picks one branch on a multi-blocker node rather than merging; no coordinator ping exists today when it does). Handed off as an independent investigation, not part of this change.

## Goals

- Give both the coordinator and a human a live, cheap, always-current signal of how much context a coordinator has accumulated.
- Nudge a coordinator, repeatedly and without ever going stale, to wrap up at a safe seam once it crosses a configurable budget — until it actually recycles.
- Bake a small set of context-preserving habits into every coordinator's spawn prompt, and into the shared `hera` skill so every future coordinator benefits, not just this change's.
- Build a `recycle_coord` primitive: kill a coordinator's session and restart it fresh on the *same* task/worktree/branch/binding, seeded with enough distilled context to continue coherently without any tool call needed to reconstruct it.
- Make recycling reachable two ways — a graceful self-service path (coordinator asks, once safe) and a forced human path (for a coordinator that's actually wedged and can't ask for anything) — since these are different failure modes requiring different intervention.

## Non-Goals

- No change to `role_status`'s five DB-backed values, no `hera_messages` schema migration, no new `escalated`/`review`/`shipped` top-level statuses.
- No new coordinator-facing signal for `(?)`/needs-input — rejected outright (see Decision 3).
- No model/context-window selection logic for coordinators — owned elsewhere.
- No plan-DAG fan-in branch-merge automation.
- No new telemetry/history table — `context_size` is a single overwriting scalar, not a time series.

## Decisions

### D1 — Context budget via a self-gated global Stop hook

A new `argus coord-hook` subcommand (`cmd/argus/`), registered as a `Stop` hook in the user's **global** `~/.claude/settings.json` (not any project's `.claude/settings.json`).

**Why global, not project-scoped:** every agent process argus spawns inherits the daemon's full environment via `cmd.Env = append(os.Environ(), ...)` (`internal/agent/agent.go`) — `HOME` always resolves to the real user's home directory regardless of which repo a worktree lives in. A hera coordinator can be spawned into any project, most of which argus doesn't own and can't pre-configure with hook wiring. One global registration covers every hera-spawned session in every project, forever.

**Why it must self-gate hard:** being global, it fires on every Claude Code session on the machine, including ordinary interactive work with no relation to argus. First action, every invocation: check `ARGUS_TASK_ID` (only set at spawn — `agent.go`) and the resolved role kind; no-op immediately unless both are present and the role is a `coordinator`. This mirrors the existing double-guard convention already used for the hera/iris/plannotator sections of this project's own CLAUDE.md.

**What it does on every Stop event, for a coordinator session:**

1. Tail the session's transcript JSONL (path arrives via the hook's stdin as `transcript_path` — Claude Code does not hand a Stop hook inline usage data) for the latest assistant message's `usage.cache_read_input_tokens`.
2. Resolve the daemon's live REST port + `~/.argus/api-token` (no `ARGUS_API_PORT`/`ARGUS_API_TOKEN` env var exists today — confirmed absent from the spawn env — so the subcommand self-discovers both, same as any other REST client would).
3. Always overwrite `task_meta` (`namespace="hera"`, `key="context_size"`) with the computed value — cheap, no telemetry table, always fresh. This is visible to a human (future rail surface) and to the recycle mechanism's own bookkeeping, with zero dependency on the coordinator's cooperation.
4. Compare against the configured `coordinator_context_budget` (new `HeraConfig.CoordinatorContextBudget int` field, `toml:"coordinator_context_budget"`, default `200000`). If at or over budget, **block** the Stop event (Claude Code's hook contract: a blocking decision injects its `reason` text as the next turn's context) with a message instructing the coordinator to reach a safe seam and call for a reboot.

Because this recomputes fresh on every single turn rather than firing once, there is no staleness risk of the kind found fatal for the (rejected) needs-input bridge in D3 below: the nudge simply keeps recurring for as long as the condition holds, and stops the moment it doesn't (post-recycle, `context_size` resets near zero).

**Alternative considered and rejected:** bumping the coordinator to a larger-context model as a buffer. Rejected — doesn't address the root cause (a coordinator that hoards context just hoards more before hitting the same wall, at higher per-turn cost), cuts against Decision 4's delegation-bias goal, and model selection is already the diligence-profile system's job, not this change's.

#### Acceptance criteria

- It should stamp `task_meta.context_size` on every Stop event for a coordinator-role session, unconditionally.
- It should no-op with no side effects when `ARGUS_TASK_ID` is unset or the bound role is not a coordinator.
- It should inject a repeating "reach a seam, call for a reboot" instruction on every Stop event while `context_size >= coordinator_context_budget`, and stop injecting it the turn that condition becomes false.

### D2 — No new `role_status` values; `escalated` survives only as a message convention

Ran the originally proposed enum (`in_progress`/`blocked`/`escalated`/`review`/`shipped`) against the actual code and against the test "does the coordinator route differently on the label alone":

- `in_progress` — a rename of `working`; no behavior change, not adopted.
- `review` — already fully covered: a worker reporting `done` already rolls its task to `in_review` and stamps `ready_to_close` while leaving the session alive (`RollHeraWorkerToReview`), and the rail already has a dedicated icon (`theme.IconReview`) for exactly this state. A separate `review` status would be pure duplication.
- `shipped` — covered by `done` plus a descriptive body (exactly the "push the branch, report the summary" pattern already in use). No routing difference.
- `escalated` — the only genuinely new concept. `blocked` today is inert (purely advisory, no rollup behavior), whereas a worker flagging a decision-fork or impasse *does* call for a different coordinator response (decide-and-reply vs. investigate-and-intervene).

`hera_messages` has no structured `kind` column (`body`/`tldr` only) — adding one is a schema change for a soft signal. Adopted instead: a `tldr`/body convention (e.g. a short `[decision_fork]`/`[impasse]` tag) taught to coordinators via the discipline spec (Decision 4), zero schema change. `role_status`'s existing DB `CHECK (status IN ('idle','working','blocked','done','failed'))` constraint is untouched.

#### Acceptance criteria

- It should require no `hera_messages` or `hera_role_status` schema change.
- It should teach coordinators (via the spawn orientation and the shared skill) to recognize a `[decision_fork]`/`[impasse]` tldr tag and respond accordingly.

### D3 — Rejected: an `AskUserQuestion`-triggered coordinator signal

Considered a `PreToolUse` hook on `AskUserQuestion` that would ping the coordinator (`hera_send status=blocked`) the moment a worker hit that widget, on the theory that the existing `(?)` rail icon is invisible to the coordinator. Rejected on two grounds surfaced during design review:

1. **Not actionable.** The coordinator's response doesn't actually branch on *why* a worker went quiet (widget vs. crash vs. long tool call vs. finishing a turn) — in every case the realistic response is the same ("can't talk to it directly"). Distinguishing the cause adds no decision the coordinator can make differently.
2. **No resolution/clear signal.** The moment a human resolves the prompt directly in the pane (which they already can, since they see `(?)` in the rail), the coordinator's belief that the worker is stuck goes stale with nothing to correct it — worse than no signal, since a stale "stuck" belief could drive an unnecessary intervention.

A secondary finding during this investigation — `ReliableNotify` (the delivery mechanism behind `hera_send`) gates purely on `session.IsIdle()` (output quiescence) and `FocusTracker.IsFocused`, with **no** `BlockedOnPrompt`/`NeedsInput` guard — was raised and explicitly deferred by the user (no observed incidents, not worth chasing without evidence). Not part of this change; noted here only so it isn't rediscovered as new information later.

### D4 — Coordinator-discipline spec, injected at spawn and in the shared skill

Extends `HeraCoordinatorOrientation` (`internal/agent/hera_spawn.go`) with five habits, plus a companion edit to `.claude/skills/hera/SKILL.md` §4 (the shared "coordination decision" rule, benefiting every coordinator, not just this change's):

1. **Small window (habit, not a model setting).** Distinct from model/context-window choice (owned elsewhere) — this is behaving as if context is precious regardless of the actual window size, because a coordinator's accumulation is permanent for the life of the orchestration in a way a worker's isn't.
2. **Low default reasoning effort, escalate for real judgment calls.** Routine coordination (relay a report, decide what to spawn next) is cheap cognitive work; default low, but escalate deliberately for genuine forks (an architectural decision, reconciling conflicting worker reports, a plan-DAG fan-in reconciliation moment).
3. **Delegation bias, sharpened to a concrete rule.** Investigation-class work (read a file, understand a function, check why a test failed) → Claude's native Agent/Task tool, model `sonnet` unless the question is genuinely hard — never `hera_spawn_worker` for this (a worktree/branch/task/binding is real overhead for something that just needs an answer back). `hera_spawn_worker` stays reserved for work that needs its own git worktree/branch/PR. The shared skill gets the tightened language: *"delegate with prejudice... but don't be dumb about it"* — a single small file or a one-shot grep with a few hits is cheaper read inline than round-tripped through a sub-agent; delegate when the exploration volume clearly dwarfs the answer needed back, not reflexively for every read.
4. **Pointers, not payloads.** Reference `path:line`, branch names, task IDs in messages and reports — never paste full file contents or long logs into a `hera_send` body (which duplicates the content into both sender's and receiver's context at once, and works against the 64 KiB body cap).
5. **Distillate-harvest-before-retire.** Before recycling (self-service or anticipating a forced one), (a) one pass ensuring `design.md`'s Open Questions / discovery-findings sections are current — this is not a new artifact, it's the existing OpenSpec discipline this project already mandates, just enforced as a checkpoint before winding down — and (b) write a short `handoff_note` via the extended `hera_status` call (Decision 5) capturing anything *not* already durably captured in the plan-DAG or `design.md` (why a non-obvious call was made, what to watch for).

#### Acceptance criteria

- It should include all five habits in every coordinator's spawn orientation text (verified by a spawn-prompt snapshot/golden test).
- It should tighten `.claude/skills/hera/SKILL.md` §4 with the delegate-with-prejudice language, without changing its existing decision triad.

### D5 — `recycle_coord`: same task, same worktree, kill-and-restart

**Mechanism:** reuse the exact same argus task row (same worktree, same branch, same git history) across every recycle. Hera bindings key on `(role_id, orchestrator_id, argus_task_id)` — not session ID — so nothing about the binding needs to change. `BuildCmd(task, cfg, resume=false)` (`internal/agent/agent.go`) already produces a brand-new, empty-context Claude session for an existing task as long as no stale `SessionID` is pinned; that is the entire restart mechanism.

**Why not a new task/worktree ("end-and-rebind"), the alternative considered:** investigated whether a coordinator's worktree accumulates state from its plan-DAG's workers that a worktree swap would lose (the user's working assumption going in). Traced the actual branch resolution in `internal/heragater/heragater.go`: the coordinator's own branch is only ever read once, as the DAG root's base (`resolveRootBaseBranch`) — nothing is ever merged back into it automatically. The coordinator's worktree holds only what the coordinator itself commits there (principally the OpenSpec change docs) — there is no hidden "gathered" state at risk. Reusing the same task therefore loses nothing and is strictly simpler than minting a new task, ending the old binding, and re-binding a new one.

**Two trigger paths, deliberately different idle semantics:**

- **Self-service (graceful):** the coordinator, once it reaches a safe seam (per the D1 nudge), calls the extended `hera_status(cwd, status, [orchestrator], [handoff_note], [request_recycle=true])` — one call does the harvest-note write and signals recycle intent together, rather than two separate tools. The daemon stamps a pending-recycle flag and a background watcher (same 5s-tick cadence as the existing `ReliableNotify` reconciler) waits for genuine `session.IsIdle()` before killing and restarting — never forces a recycle mid-turn.
- **Human-forced (for a wedged coordinator):** a rail keybinding on the coordinator's row, behind a confirm modal (consistent with existing destructive-action patterns like `J`-detach), that kills and restarts **immediately, regardless of idle state**. This is the must-have path per the known incident (a session can get stuck in ways argus's own tracking doesn't see) — a genuinely wedged coordinator will never become idle on its own, so waiting for it would hang forever for exactly the case this path exists to solve.

**Kill-step hardening:** informed by the sibling chunk's documented incident (`task_stop` not actually killing a session's background sub-agent job — a stray `run_in_background` job survives under the same Claude session UUID, tracked separately by Claude Code's own `claude agents` registry, causing worktree-write EPERM and a resume collision), the kill step checks for and cleans up any stray job tied to the session (`claude agents --json` → match → `claude stop <id>`) before restarting, rather than assuming a single kill signal is sufficient.

**Seed prompt — assembled daemon-side, injected directly, no MCP round-trip:** the recycle mechanism (plain Go, direct DB access) composes the new session's literal opening prompt from three sources, all pre-resolved before the session ever starts:

1. The role's original mission (`hera_roles.prompt`, already stored).
2. The current plan-DAG state for the orchestrator (node statuses, queried directly).
3. The `handoff_note` from `task_meta`, if present.

The new coordinator's first message already contains its situation — it never needs to call `hera_join`/`hera_plan`/anything just to reconstruct where it left off. This generalizes the same principle to the whole seed, not just the note.

#### Acceptance criteria

- It should preserve the task ID, worktree path, branch, and hera binding across a recycle — no rebind, no new worktree.
- It should wait for genuine session idleness before acting on a self-service recycle request, and never wait for idleness on a human-forced recycle.
- It should compose the new session's opening prompt from the role's stored mission, live plan-DAG state, and any `handoff_note`, with zero tool calls required from the new session to obtain any of the three.
- It should check for and stop any stray background job tied to the old session before restarting, not merely signal the primary PTY.

## Risks / Trade-offs

- **Global Stop hook cost.** Every Claude Code session on the machine pays a small hook-invocation cost, even non-argus ones. Mitigated by the `ARGUS_TASK_ID` early-exit being the very first check — near-zero cost for the common (non-hera) case.
- **Self-service recycle could be requested and then never go idle** (coordinator gets stuck mid-turn after requesting). Mitigated by the human-forced path existing independently — it doesn't depend on the self-service flag or on idleness at all.
- **A discipline spec is only as good as whether the coordinator follows it.** Nothing in this design *enforces* delegation bias or pointers-not-payloads; it's prompt guidance, not a hard constraint. Accepted trade-off — consistent with how the rest of hera's coordination conventions (mission-only prompts, self-rebase on fan-in) already work in this codebase.
- **`coordinator_context_budget` is a blunt token-count proxy**, not a true measure of "useful" vs. "wasted" context. Accepted for v1; revisit if it under/over-fires in practice.

## Migration Plan

Additive throughout — no existing behavior changes for non-coordinator roles or pre-existing coordinators:

1. Add `HeraConfig.CoordinatorContextBudget` (default `200000`) — absent key behaves as today (no budget enforced) until a project opts in, but the field ships with a non-zero default so it's active by default once this change lands.
2. Add the `argus coord-hook` subcommand; document the required global `~/.claude/settings.json` Stop-hook registration (a one-time manual step for the user, not something argus can write to their global settings on their behalf).
3. Extend `hera_status` with optional `handoff_note` / `request_recycle` params — fully backward compatible, existing callers unaffected.
4. Add the recycle primitive (daemon-side) and the rail keybinding + confirm modal.
5. Update `HeraCoordinatorOrientation` and `.claude/skills/hera/SKILL.md` — takes effect for newly-spawned coordinators only; running coordinators keep their original orientation text until their next recycle.

No rollback complexity: every piece is additive and independently disable-able (unset the budget, don't register the hook, never call the recycle tool/key).

## Open Questions

- Should `coordinator_context_budget` eventually vary by model (different effective context windows per backend), or is a single global default sufficient for v1? Leaning toward single default until real usage says otherwise.
- Should the rail surface `task_meta.context_size` visibly (e.g. a number or bar on the coordinator's row) as part of this change, or is that a follow-on TUI change? Leaning toward follow-on — this change's job is the plumbing and the recycle primitive, not the rail visualization.
