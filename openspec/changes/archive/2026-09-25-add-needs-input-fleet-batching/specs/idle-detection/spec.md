# Idle Detection

## ADDED Requirements

### Requirement: The per-tick needs-input/content-idle fleet scan is batched for large fleets

The TUI's per-tick needs-input and content-idle detection SHALL bound the worst-case per-tick cost of the fleet scan (tail read + terminal re-emulation) at a fixed batch size, regardless of how many sessions are concurrently running. A fleet at or below the batch size SHALL be scanned in full every tick, identical to the unbatched behavior. A fleet above the batch size SHALL have its running sessions partitioned into fixed-size rotation batches, with exactly one batch due for a fresh read each tick; sessions outside the due batch SHALL reuse ("replay") their last-computed raw signal for that tick instead of re-reading and re-emulating. Every running session's tick-scoped counters (escalation, resumed-activity, sustained-active, settlement) SHALL still advance every tick regardless of batch membership, using the replayed signal when not due. A session with no prior raw signal (its first tick since appearing in the running set) SHALL always be treated as due, so a freshly-spawned session's first reading is never delayed by rotation.

Derived from: `internal/tui/app.go` (`needsInputScanBatchSize`, `needsInputScanBatch`, `detectNeedsInputSticky`'s `reuseCached`).

#### Scenario: A fleet at or below the batch size is unaffected

- **WHEN** the number of running sessions is at or below the batch size
- **THEN** every running session is read and re-emulated fresh every tick, with no staleness introduced

#### Scenario: A fleet above the batch size caps the per-tick scan

- **WHEN** the number of running sessions exceeds the batch size
- **THEN** only one rotation batch of sessions is freshly read and re-emulated each tick; the remaining sessions reuse their last-computed signal for that tick

#### Scenario: Tick-counters advance every tick regardless of batch membership

- **WHEN** a session is outside the due batch for several consecutive ticks with unchanging content
- **THEN** its escalation/resumed-activity/sustained-active/settlement counters advance on every tick exactly as they would if freshly read each time

#### Scenario: A change in an out-of-batch session is caught once its batch comes due

- **WHEN** a session's on-disk log changes while it is outside the due batch
- **THEN** the change is detected and reflected within one full rotation of the batches (never permanently missed), once that session's batch next comes due

#### Scenario: A freshly-spawned session's first reading is never delayed

- **WHEN** a session appears in the running set for the first time, regardless of current batch rotation
- **THEN** it is read and re-emulated fresh on that same tick
