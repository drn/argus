## ADDED Requirements

### Requirement: Bridge cumulative cost accrues once across resets

The daemon SHALL accrue session.measure cumulative USD cost on the binding using a persistent baseline. Duplicate measurements and daemon restart SHALL not double-count. A lower counter or session identity change SHALL start a new accrual epoch; previous accrued cost SHALL remain immutable.

A lower counter from the same known process generation and session SHALL be ignored as a late sample. A lower counter after a process restart SHALL start a new accrual epoch even when the transcript id is reused.

#### Scenario: Late old-boot measurement after daemon restart

- **WHEN** a lower cumulative sample arrives for the same process generation and session after a higher one was persisted
- **THEN** it adds no cost and does not lower the persistent baseline

On a source handoff, the first reading SHALL establish the new source's offset without adding cost. Under-counting a transition turn is accepted; double-counting is forbidden.

#### Scenario: Bridge becomes stale and later recovers

- **WHEN** accounting switches from bridge to transcript or back
- **THEN** the first new-source reading saves its baseline without adding cost and subsequent readings add only their incremental usage

#### Scenario: Increasing cost and duplicate delivery

- **WHEN** cost reports 1 USD then 1.5 USD then repeats 1.5 USD
- **THEN** the total added cost is 1.5 USD

#### Scenario: Clear or restart resets the counter

- **WHEN** a new session counter reports 0.2 USD after an old session accrued 1.5 USD
- **THEN** the binding total becomes 1.7 USD

