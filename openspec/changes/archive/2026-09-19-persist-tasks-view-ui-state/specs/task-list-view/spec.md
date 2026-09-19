## MODIFIED Requirements

### Requirement: Hide hera-managed tasks toggle

The task list SHALL provide a single hera-visibility toggle bound to the `H` key. The toggle SHALL default to OFF (hera-managed tasks **visible** inline), so the Tasks tab shows hera-spawned workers and live coordinators alongside plain tasks by default, each marked with a per-row hera-role indicator. While the toggle is ON, the list SHALL hide every **hera-managed** task and SHALL show freelancer and plain non-hera tasks; pressing `H` toggles between the two states.

A task SHALL be classified as **hera-managed** when EITHER of the following holds:

- It is a hera-spawned worker — its `task_meta` `hera.role` is `worker` (the sidecar stamped at spawn/join, which is permanent and is never cleared when a binding ends); OR
- It holds at least one live hera binding (a binding whose `ended_at` is unset) to a role of kind `coordinator` or `worker`, as reported by the hera bindings/roles store.

A task SHALL be classified as a **freelancer** (and therefore SHALL remain visible regardless of the toggle) when it is neither a hera-spawned worker nor holds a live coordinator/worker binding — i.e. it has no live binding, or holds only `freelance`-kind live bindings. A plain non-hera task (no hera role at all) SHALL likewise always remain visible.

The toggle SHALL compose with the substring filter (`/`) — each is an independent exclusion applied in the same row-build pass. In remote (`--remote`) mode, where no binding-query REST endpoint exists, the live-binding signal MAY fall back to a best-effort union of the `task_meta` `hera.role` worker and coordinator entries; this MAY report a finished worker or coordinator as still managed until the next tick refresh, and is a known degradation documented in the design.

The toggle's ON/OFF state SHALL persist locally across an argus restart, so relaunching argus restores the same visibility the user last set instead of always defaulting to OFF. In `--remote` mode, where there is no local persistence seam, the toggle SHALL NOT persist and SHALL always start from the OFF default.

#### Scenario: Hera worker visible by default, hidden by H

- **WHEN** a task is a hera-spawned worker and the toggle is OFF (the default)
- **THEN** the task is shown in the Tasks tab with a worker indicator; pressing `H` hides it and pressing `H` again shows it

#### Scenario: Live coordinator visible by default, hidden by H

- **WHEN** a task holds a live coordinator-kind binding and the toggle is OFF (the default)
- **THEN** the task is shown in the Tasks tab with a coordinator indicator; pressing `H` hides it and pressing `H` again shows it

#### Scenario: Freelancer always visible

- **WHEN** a task has no live hera binding (or only `freelance`-kind live bindings) and is not a hera-spawned worker
- **THEN** the task is visible regardless of the toggle state

#### Scenario: Plain non-hera task always visible

- **WHEN** a task holds no hera role at all
- **THEN** the task is visible regardless of the toggle state

#### Scenario: Composes with the substring filter

- **WHEN** the toggle is ON and a substring filter is active
- **THEN** a task is visible only if it is not hera-managed AND matches every substring term

#### Scenario: Toggle state persists across a restart

- **WHEN** the user presses `H` to turn the toggle ON and then restarts argus
- **THEN** the Tasks tab shows the toggle ON again (hera-managed tasks hidden) without the user pressing `H`

#### Scenario: No persisted value defaults to OFF

- **WHEN** no toggle state has ever been persisted (first run) or argus is running in `--remote` mode
- **THEN** the toggle defaults to OFF and hera-managed tasks are visible
