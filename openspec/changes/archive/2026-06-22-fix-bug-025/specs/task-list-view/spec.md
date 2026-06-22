# task-list-view

## ADDED Requirements

### Requirement: Hide hera-managed tasks toggle

The task list SHALL provide a single hera-visibility toggle bound to the `H` key. While the toggle is ON (the default), the list SHALL hide every **hera-managed** task and SHALL show freelancer and plain non-hera tasks; pressing `H` reveals the hidden tasks inline, and pressing `H` again hides them again. The toggle SHALL default to ON (hera-managed tasks hidden, because they have their own home in the Hera tab).

A task SHALL be classified as **hera-managed** when EITHER of the following holds:

- It is a hera-spawned worker — its `task_meta` `hera.role` is `worker` (the sidecar stamped at spawn/join, which is permanent and is never cleared when a binding ends); OR
- It holds at least one live hera binding (a binding whose `ended_at` is unset) to a role of kind `coordinator` or `worker`, as reported by the hera bindings/roles store.

A task SHALL be classified as a **freelancer** (and therefore SHALL remain visible regardless of the toggle) when it is neither a hera-spawned worker nor holds a live coordinator/worker binding — i.e. it has no live binding, or holds only `freelance`-kind live bindings. A plain non-hera task (no hera role at all) SHALL likewise always remain visible.

The toggle SHALL compose with the substring filter (`/`) — each is an independent exclusion applied in the same row-build pass. In remote (`--remote`) mode, where no binding-query REST endpoint exists, the live-binding signal MAY fall back to a best-effort union of the `task_meta` `hera.role` worker and coordinator entries; this MAY report a finished worker or coordinator as still managed until the next tick refresh, and is a known degradation documented in the design.

#### Scenario: Hera worker hidden by default, revealed by H

- **WHEN** a task is a hera-spawned worker and the toggle is ON (the default)
- **THEN** the task is hidden from the Tasks tab; pressing `H` reveals it and pressing `H` again hides it

#### Scenario: Live coordinator hidden by default, revealed by H

- **WHEN** a task holds a live coordinator-kind binding and the toggle is ON (the default)
- **THEN** the task is hidden from the Tasks tab; pressing `H` reveals it and pressing `H` again hides it

#### Scenario: Freelancer always visible

- **WHEN** a task has no live hera binding (or only `freelance`-kind live bindings) and is not a hera-spawned worker
- **THEN** the task is visible whether the toggle is ON or OFF

#### Scenario: Plain non-hera task always visible

- **WHEN** a task holds no hera role at all
- **THEN** the task is visible whether the toggle is ON or OFF

#### Scenario: Composes with the substring filter

- **WHEN** both the toggle is ON and a substring filter is active
- **THEN** a task is visible only if it is not hera-managed AND matches every substring term

## REMOVED Requirements

### Requirement: Freelancers-only filter toggle

**Reason**: Collapsed into the single `H` hide-hera-managed toggle (BUG-025). The dedicated `f` "freelancers-only" key, its `freelancersOnly` state, and its title indicator are removed; the live coordinator/worker binding signal it consumed is now folded into the `H` predicate so `H` hides both spawned workers and live coordinators.

**Migration**: Operators who pressed `f` to see only freelancers now leave `H` in its default ON state, which hides every hera-managed task (workers + coordinators) and shows freelancers plus plain non-hera tasks. No data migration; the `f` key becomes unbound on the Tasks tab.
