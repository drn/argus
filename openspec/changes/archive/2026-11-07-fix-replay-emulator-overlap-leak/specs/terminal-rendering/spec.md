## ADDED Requirements

### Requirement: Replay rebuilds are single-flight and never publish stale content

A terminal pane SHALL have at most one asynchronous replay rebuild in flight. Resetting the pane, invalidating its replay cache, or changing its task SHALL NOT permit a second rebuild to start while one is running. A rebuild kicked for a binding that has since been reset SHALL NOT publish its emulator or its alt-screen state; it SHALL park the emulator for reuse and trigger a redraw so a fresh rebuild is kicked for the current binding.

#### Scenario: Rebind while a rebuild is in flight

- **WHEN** a pane's rebuild for task A is in flight and the pane is rebound to task B
- **THEN** no second rebuild starts until A's rebuild finishes, A's result is not published, and the rebuild that follows publishes B's content

#### Scenario: Slow stale rebuild finishes last

- **WHEN** a rebuild for the previous binding finishes after a rebuild for the current binding
- **THEN** the published emulator still holds the current binding's content

#### Scenario: Stale alt-screen state is not recorded

- **WHEN** a stale rebuild's content entered the alternate screen
- **THEN** the pane's recorded alt-screen state is unchanged

### Requirement: A pane owns at most two replay emulators

A terminal pane SHALL own at most two replay emulators at any time (published plus spare, or two spares), reusing them across rebuilds. An emulator SHALL NOT be discarded without being retained for reuse; if none can be retained, a drop counter and a log line SHALL record it, and the emulator SHALL NOT be closed.

#### Scenario: Repeated rebinds during rebuilds

- **WHEN** a pane is rebound 25 times, each while a rebuild is in flight
- **THEN** the pane has created at most two replay emulators, the drop counter is zero, and the process goroutine count has grown by no more than the two emulators' drain goroutines

#### Scenario: A rebuild panics before publishing

- **WHEN** a rebuild panics after acquiring its emulator
- **THEN** the in-flight flag is cleared and the emulator is retained for reuse
