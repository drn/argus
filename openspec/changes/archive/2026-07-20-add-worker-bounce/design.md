## Context

Hera roles come in three kinds: `coordinator`, `worker`, `freelance` (`internal/db/hera.go:113-115`). Coordinators already have a full context-budget management system (`openspec/specs/coordinator-context-management`): the `argus coord-hook` Stop hook tracks `context_size`, nudges a coordinator to reach a safe seam and self-recycle via `hera_status(handoff_note=..., request_recycle=true)`, and force-kills at 1.5x budget as a hard-stop escalation. The underlying `recycle_coord` primitive (`internal/hera/recycle.go`) kills and restarts a role's session in place — same task, worktree, branch, and hera binding — reseeded from the role's mission, plan-DAG sibling state, and any `handoff_note`.

Workers and freelance roles have none of this. A worker on a long-running implementation task can accumulate heavy context too, and today there's no way to reset it without abandoning the task (delete + respawn), losing the worktree/branch continuity `recycle_coord` already solves for coordinators.

Three existing choke points currently restrict all of this to coordinators:

- `internal/tui/hera/page.go:1076` and `internal/tui/heraactions.go:704` — the rail `B` key no-ops unless `sel.IsCoordinator()`.
- `internal/mcp/hera.go`'s `toolHeraStatus` — rejects `handoff_note`/`request_recycle` for worker/freelance callers with an error naming the parameter.
- `internal/hera/recycle_watcher.go:132` (`RecycleWatcher.tickTask`) — filters live bindings to `role.Kind == db.HeraKindCoordinator`, commented `// pending_recycle is coordinator-only (hera_status rejects it otherwise)`.

This change widens all three, purely to support a **manual**, rail-invoked "bounce" of an idle worker or freelance role — not to extend the coordinator's automated budget system to them.

## Goals / Non-Goals

**Goals:**

- Let a human, from the rail, manually bounce an idle worker or freelance role: kill and restart its session in place (same task/worktree/branch/binding), reseeded with a handoff summary the role writes about its own state before the restart.
- Reuse the existing `recycle_coord` / `RecycleWatcher` / `hera_status` machinery as-is by widening its gates, rather than building a parallel mechanism.
- Zero automation: nothing nudges, recommends, or times a worker/freelance bounce. It only ever happens because a human pressed `B`.

**Non-Goals:**

- No context-budget tracking, Stop-hook nudging, or hard-stop escalation for worker/freelance roles — `argus coord-hook` stays coordinator-only, unchanged.
- No fallback/timeout if the bounced role never calls `hera_status` after being prompted (explicit call — see D6).
- No change to coordinator `B`-key behavior (immediate `RecycleHumanForced`, unchanged).
- No gate requiring "a human pressed B" before a worker/freelance role's own `hera_status(request_recycle=true)` call takes effect — see Risks.

## Decisions

**D1 — No new `RecycleTrigger`.** Worker/freelance `B` reuses `RecycleSelfService` end-to-end via the role's own `hera_status` call; `B` itself never calls `RecycleCoord` directly for a worker/freelance selection.

- Alternative considered: `B` directly force-kills a worker/freelance role immediately (mirroring the coordinator's `RecycleHumanForced`), with the daemon actively harvesting a handoff response before killing (e.g. scanning PTY/transcript output for a sentinel marker). Rejected: this reinvents turn-completion detection, an area this codebase already has a long, painful history with (BUG-032/035/060/061/063 in `context/knowledge/gotchas/events.md`). Delegating the tool call to the role itself — which already knows when its own turn is done — avoids the problem entirely.

**D2 — Widen `hera_status`'s `handoff_note`/`request_recycle` acceptance from coordinator-only to any hera-bound role kind.**

- Alternative: add a separate parameter pair for worker/freelance (e.g. `bounce_note`), leaving `hera_status`'s existing contract untouched. Rejected: would duplicate validation/storage logic that writes the exact same `task_meta` keys, for no behavioral difference.

**D3 — Widen `RecycleWatcher.tickTask` from `role.Kind == coordinator` to accept any live-bound role kind.** This is the single choke point gating self-service recycle to coordinators; removing the filter is the whole mechanism. The existing "pick the oldest coordinator-kind binding when a task holds 2+ live bindings" disambiguation stays for tasks that do have a coordinator binding — it just no longer skips tasks that don't.

**D4 — Generalize `BuildRecycleSeedPrompt`'s opening line and doc comments away from "prior coordinator session."** Keep the plan-DAG-sibling-state section as-is for a worker/freelance role — harmless, still informative (shows what siblings under the same orchestrator are doing).

- Alternative: build a worker-specific seed section from `git status`/`diff` of the worktree instead of a handoff note. Rejected earlier in conversation: a self-authored handoff from the very session that has the context is more useful than machine-reconstructed diff state, and avoids the fresh session re-deriving "what happened" and burning its own budget doing it.

**D5 — `B` on a worker/freelance selection: confirm modal, then `WriteInputSystem` an instruction telling the (idle) role to call `hera_status(handoff_note=<summary>, request_recycle=true)`** describing what's done, current state, and what's next. No further daemon-side action — everything downstream (the role's tool call → `pending_recycle=true` → `RecycleWatcher`'s next tick finding the session idle → `RecycleCoord`/`RecycleSelfService`) is the existing pipeline, now just reachable by a non-coordinator role.

- Confirm-modal copy differs from the coordinator's ("Force recycle... wedged and unresponsive"): reads along the lines of "Bounce `<name>`? Asks it to hand off its current state, then restarts fresh once it does." "Bounce" is the user-facing verb for this path; the underlying code keeps `recycle_coord`/`RecycleCoord`/`RecycleSelfService` naming for the shared mechanism (renaming that would touch coordinator-only code for no behavioral reason).
- The rail help-modal label stays `B` (force recycle) unchanged — the physical key and mechanism are the same action (kill+restart in place); only the human-facing confirm copy differs by role kind. Not worth a help-text delta for a label that's still directionally accurate.

**D6 — No fallback/timeout (explicit user decision).** If the role ignores the instruction, errors out, or never goes idle again, nothing happens automatically. The human can press `B` again. Revisit only if this proves to be a real problem in practice.

**D7 — In scope: worker AND freelance role kinds, both widened identically** (same `role.Kind != coordinator` branch covers both — there is no behavioral difference between them for this feature). Coordinator behavior is unchanged throughout.

## Risks / Trade-offs

- **[Risk]** A worker/freelance role could call `hera_status(request_recycle=true)` unprompted — e.g. if it reasons "I'm heavy, let me self-recycle" — since nothing structurally enforces "only after a human-injected instruction." → **Mitigation:** none needed structurally. This exactly mirrors the freedom coordinators already have today (nothing stops a coordinator calling `request_recycle=true` outside of a `coord-hook` nudge either). Nothing advertises or nudges a worker/freelance role toward this, so it's a low-probability path, and its effect is fully observable after the fact (same task/worktree/branch survive, seed prompt records the handoff). Revisit only if it proves to be a real problem in practice.
- **[Risk]** Injecting the handoff-request instruction via `WriteInputSystem` when the role is *not* actually idle (pressed by mistake, or new work started between selection and confirm) just queues it as ordinary input — the role processes it whenever it gets there, possibly after doing unrelated work first. → **Mitigation:** none required; this matches the stated use case (press only when actually idle), and the confirm modal is the same human speed bump the coordinator path already relies on.
- **[Risk]** Widening `hera_status`'s acceptance touches its tool `Description` string, which currently says "Coordinator-only." → **Mitigation:** update the description and any other coordinator-only wording in the same PR so the tool's self-description stays accurate.

## Migration Plan

None needed — additive/widening change, no schema or data migration, no backwards-compatibility concern.

## Open Questions

None — all decisions above are settled for this change.

## Acceptance criteria

- It should allow a worker-kind caller to set `handoff_note` via `hera_status`.
- It should allow a worker-kind caller to set `request_recycle=true` via `hera_status`.
- It should allow a freelance-kind caller to set `handoff_note`/`request_recycle` via `hera_status` identically to a worker.
- It should continue rejecting nothing else about `hera_status` for worker/freelance callers — status validation and the existing worker-done roll-to-in_review behavior are unaffected.
- It should let `RecycleWatcher` pick up a `pending_recycle=true` row for a worker-kind role and drive it through `RecycleCoord`/`RecycleSelfService` once its session is idle.
- It should let `RecycleWatcher` do the same for a freelance-kind role.
- It should still resolve the coordinator-kind binding first when a task holds a coordinator binding among 2+ live bindings (existing disambiguation preserved).
- It should not word `BuildRecycleSeedPrompt`'s opening line as coordinator-specific when seeding a worker or freelance role's fresh session.
- It should, on `B` over a worker-role rail selection, open a confirm modal and, on confirm, send a system-input instruction to the role's live session asking it to call `hera_status(handoff_note=..., request_recycle=true)`.
- It should do the same for a freelance-role rail selection.
- It should leave `B` on a coordinator-role selection completely unchanged (still immediate `RecycleHumanForced`, unchanged confirm copy).
- It should still no-op `B` on an empty/non-selectable rail selection.
