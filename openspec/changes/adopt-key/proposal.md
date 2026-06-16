## Why

The native Hera rail dropped the plugin's `J` adopt/reparent key. Today an operator who wants to fold a freelance agent into a coordinator, or re-nest a standalone coordinator under another, has no rail affordance — the only path is having the agent re-run `hera_join` itself. This is the `J` adopt picker regression tracked in `docs/NATIVE-VS-PLUGIN-PARITY-MATRIX.md` §8 and `docs/PARITY-OUTCOME.md` item #3, and it is the one parity gap where a coordination operation was *functionally lost*, not merely cosmetically degraded.

The plugin also carried a load-bearing teardown invariant (BUG-026): re-parenting a coordinator must end ALL prior parent-links by role id — live AND ended — so de-collided leftover link roles never pile up. The parity matrix explicitly flags that if `J`/adopt is restored, the BUG-026 teardown rule must come with it.

## What Changes

- Bind `J` on the focused Hera rail to an adopt/reparent action (rail-focus-only; in a pane the `J` rune forwards to the PTY).
- **Freelancer selection** → open a themed, type-to-filter picker of the active (non-archived) orchestrators; `Enter` adopts the freelancer's argus task as a `worker` under the chosen orchestrator by creating a worker role + live binding through the same DAO path `hera_join`'s attach-mode uses. The default role name is the freelancer's name, de-collided with a numeric suffix on collision.
- **Coordinator selection** (a coordinator role row, or an orchestrator header whose orchestrator has a coordinator role) → open a picker of the OTHER active orchestrators; `Enter` re-parents the coordinator under the chosen parent as a sub-coordinator (the multi-binding the orchestration tree already nests). Self-adoption and descendant cycles are rejected with visible feedback.
- **BUG-026 teardown:** re-parenting ends EVERY prior parent-link of the coordinator's task by ROLE id (live bindings ended with reason `reparented`, then the link role deleted so its bindings cascade), leaving exactly one clean link.
- **Already-bound guard / dedupe:** reject creating a second live binding for the same task under the chosen orchestrator (the per-`(task, orchestrator)` unique index); de-collide default role names.
- The picker and the role/binding writes run off the tview event loop; a second adopt while one is in flight no-ops with feedback. Every not-applicable selection gets visible feedback, never a silent no-op.

## Capabilities

### Modified Capabilities

- `hera-view`: The "Rail keybindings (area 4)" requirement gains the `J` binding (and the omitted-key NOTE/scenarios drop `J`); the "Freelance roles hoisted into a top-level section (area 8)" requirement gains the adopt affordance (replacing its "no adopt affordance" scenario). A new requirement captures the `J` adopt/reparent behavior in full.

## Impact

- **New code:** `internal/tui/hera/adopt.go` (`AdoptOps` over an `AdoptStore` interface: `AdoptTaskIntoOrchestrator`, `ReparentCoordinator`, `ListActiveOrchestrators`); `internal/tui/herapicker.go` (`OrchPickerModal`, a type-to-filter orchestrator picker mirroring `SessionPickerModal`).
- **New DAO:** `db.ListHeraBindingsByTask(taskID)` — all bindings (live + ended) for a task, required by the BUG-026 teardown (native previously had only `ListHeraLiveBindingsByTask`).
- **Modified code:** `internal/tui/hera/page.go` (`OnAdopt` callback + `J` case in `handleRailMutation`); `internal/tui/app.go` (`modeHeraOrchPicker`, picker fields, key routing, `OnAdopt` wiring, `heraAdoptOps`); `internal/tui/heraactions.go` (`heraOpenAdopt` dispatch + picker plumbing); `internal/tui/modal/help.go` (+ `help_test.go`); README keybinding table.
- **Native divergence from the plugin (documented in the delta NOTE):** the plugin's freelancers are UNMANAGED tasks with zero bindings, so its guard rejects *any* live binding. Native's Freelance section holds explicit `freelance`-kind roles that already carry their own live binding, so native's guard rejects only a duplicate binding under the SAME target orchestrator (consistent with the multi-binding model). A freelance row with no live binding (no task id) is rejected with feedback, mirroring the plugin's "no argus task id" scenario.
- **Dependencies:** none added.
- **Data:** no schema change; reuses `hera_roles` / `hera_bindings` and the `meta:hera.role=worker` best-effort stamp.
