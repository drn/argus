# TUI Shell

## MODIFIED Requirements

### Requirement: Spinner animation only while work is active

The shell SHALL drive spinner repaints only while a spinner glyph actually rendered on screen right now would animate, and SHALL suppress periodic spinner repaints otherwise. On the base Tasks-tab view, this SHALL be scoped to the currently visible (filtered, on-screen) task row set — a running-but-idle or running-but-filtered-out task SHALL NOT keep the periodic redraw alive. Every other mode (the Hera tab, the fullscreen agent view, any modal/picker) SHALL fall back to the fleet-wide check (any running task anywhere is not idle), unchanged from prior behavior. The gate MAY lag a tab switch by up to one tick; this staleness SHALL be bounded and self-healing.

#### Scenario: No spinner repaints when all tasks are idle

- **WHEN** there are no running tasks, or every running task is idle
- **THEN** the shell does not enqueue spinner-driven redraws

#### Scenario: Spinner repaints while a visible task is actively running (Tasks tab)

- **WHEN** the Tasks tab is active and at least one task in its currently visible row set is running and not idle
- **THEN** the shell enqueues periodic redraws to animate the spinner

#### Scenario: A running-but-hidden task does not keep the Tasks-tab spinner alive

- **WHEN** the Tasks tab is active and the only running-and-not-idle task is hidden from the visible row set (filtered out, or a hera-managed task with hera-managed tasks hidden)
- **THEN** the shell does not enqueue spinner-driven redraws for that task

#### Scenario: Non-Tasks-tab modes keep the original fleet-wide check

- **WHEN** the Hera tab, the fullscreen agent view, or any modal/picker is active
- **THEN** the shell's redraw gate considers every running-and-not-idle task in the whole fleet, regardless of what is or isn't rendered on screen, unchanged from prior behavior
