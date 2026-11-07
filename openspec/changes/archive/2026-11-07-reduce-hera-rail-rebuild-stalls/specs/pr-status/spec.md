## ADDED Requirements

### Requirement: An unchanged poll result is not rewritten

The poller SHALL persist a task's polled PR `state` and `url` only when at least one of them differs from the value already stored under namespace `pr`. The comparison and the write SHALL occur inside a single transaction, so there is no window in which a stale read decides the outcome. When both values already match, the poller SHALL commit nothing, so the database's change counter (`PRAGMA data_version`) does not move and the stored `updated_at` of that row is not advanced. Merge notifications SHALL be unaffected: whether a poll resolves a PR as merged is decided from the fetched result, not from whether a write happened. (A terminal stored state is already excluded from polling, so a merge transition always arrives with a changed value and is written.) Because `updated_at` of an unchanged `pr` row no longer advances, `GET /api/tasks/{id}/meta` reports the time the value last changed, not the time it was last polled.

Derived from: `internal/daemon/daemon.go` (`pollPRStatesOnce`), `internal/db/task_meta.go` (`SetMetaBatchIfChanged`).

#### Scenario: An identical poll result commits nothing

- **WHEN** a poll resolves a task's PR as state `none`, url `''` and the stored `pr` row already holds exactly those values
- **THEN** no transaction commits, `data_version` does not change and `updated_at` is unchanged

#### Scenario: A changed state is written

- **WHEN** a poll resolves `awaiting-review` with a URL for a task whose stored state is `none`
- **THEN** both keys are written and `updated_at` advances

#### Scenario: A changed url alone is written

- **WHEN** only the URL differs from the stored value
- **THEN** both keys are written

#### Scenario: A merge transition still notifies

- **WHEN** a poll resolves a task's PR as merged while the stored state is still open (`awaiting-review`), so the result differs and is written
- **THEN** both merge notifications fire under the existing rules, exactly as before this change
