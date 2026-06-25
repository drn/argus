## Why

In the native Hera view, `J` on a coordinator adopts/re-parents it UNDER
another coordinator (`ReparentCoordinator`). There is NO inverse: once a
coordinator is nested as a sub-coordinator, the operator has no way to detach
it back to top-level (un-nest it to a root orchestrator with no parent). A user
who accidentally nests the wrong coordinator under another has no escape hatch.

The teardown for this already exists. `ReparentCoordinator` nests child `C`
under parent `P` by creating a worker "link role" in `P` bound to `C`'s
coordinator argus TASK (a bridge / multi-binding). Before recreating the link
it already tears down EVERY prior parent linkage of `C`'s coord task (ends live
parent-link bindings with reason `reparented`, then deletes each distinct
parent link role so its bindings cascade) — everything EXCEPT `C`'s own
coordinator role. Detach-to-top-level is exactly that teardown WITHOUT
recreating a new link.

## What Changes

- **Add `DetachCoordinator(childOrchestratorID)` to `AdoptOps`** (op layer,
  `internal/tui/hera/adopt.go`). It resolves `C`'s coordinator role + latest
  binding's task id (same as re-parent), runs ONLY the teardown (end live
  parent-link bindings + delete parent link roles, excluding `C`'s own coord
  role), recreates NO link, and is IDEMPOTENT (already-top-level → clean no-op,
  not an error).
- **Extract the teardown into a shared helper** (`teardownParentLinks`) so
  `ReparentCoordinator` and `DetachCoordinator` use the SAME single-source
  teardown — no duplication.
- **UI entry point: reuse `J`.** When `J` targets a coordinator, the
  orchestrator picker gains a sentinel row at the TOP labeled
  `— Detach (make top-level) —` that calls `DetachCoordinator` instead of
  opening a parent target. This reuses the existing `J` key and adds NO new
  keybinding (no help-modal / README keybinding churn). The coordinator picker
  therefore always opens (the detach row is always offered, idempotently), so
  the "no eligible target" feedback now applies only to freelancers.

## Capabilities

- `hera-view` — modifies the `J` adopt/re-parent requirement to add the
  detach-to-top-level affordance and its idempotent teardown-without-recreate
  semantics.

## Impact

- `internal/tui/hera/adopt.go` — new `DetachCoordinator` + `DetachResult` +
  `teardownParentLinks` shared helper + `EndReasonDetached`; `ReparentCoordinator`
  refactored onto the shared helper.
- `internal/tui/heraactions.go` — coordinator picker prepends the detach
  sentinel and dispatches to `DetachCoordinator`; the `len(targets)==0` bail is
  removed for coordinators.
- `internal/tui/hera/adopt_test.go` — detach + idempotency + shared-teardown
  regression tests.
- `context/knowledge/gotchas/hera-view.md` — the bridge-binding nesting model /
  detach-as-teardown-without-recreate invariant.
- No new keybinding; help modal / README unchanged.
