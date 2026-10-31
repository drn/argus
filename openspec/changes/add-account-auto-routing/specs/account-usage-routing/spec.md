## ADDED Requirements

### Requirement: Daemon keeps a per-account usage ledger

The daemon SHALL keep an in-memory ledger of Claude plan usage per account, fed by every bridge `session.measure` that carries `rate_limits`. Each measurement SHALL be recorded under the measuring task's stored account (empty normalized to `default`), separately for the `five_hour` and `seven_day` windows, keeping the newest measurement per (account, window). A recorded reading SHALL remain valid until its own `resets_at`, regardless of whether any session on that account is still live, and SHALL be discarded once `resets_at` has passed. Readings with a percentage outside 0..100, a non-finite percentage, or an unparsable or past `resets_at` SHALL be ignored. The ledger SHALL NOT be persisted; after a daemon restart every account is unknown until its next measurement. The local TUI SHALL read the ledger over the TUI socket; a failed read SHALL be treated as every account unknown and logged under `[account]`.

#### Scenario: Cap remembered after sessions end
- **WHEN** a session on `default` reports `five_hour` at 100% resetting at 17:40 and then every `default` session exits
- **THEN** the ledger still reports `default` at 100% for the 5-hour window until 17:40

#### Scenario: Reading expires at reset
- **WHEN** the current time passes a recorded reading's `resets_at`
- **THEN** that window is unknown for that account

#### Scenario: Old daemon
- **WHEN** the TUI asks a daemon that does not implement the ledger read
- **THEN** every account is treated as unknown and the failure is logged

### Requirement: Auto account selection picks the most headroom

When a new task's account resolves to `auto`, the system SHALL choose among the accounts that support the task's backend (`default` first, then configured names in sorted order). For each candidate and each window, headroom SHALL be the window's threshold minus its used percentage, an unknown window counting as 0% used; the candidate's score SHALL be the smaller of its two headrooms. The candidate with the highest score SHALL be chosen, ties going to candidate order. When no candidate has a positive score, the highest score SHALL still be chosen and the outcome logged under `[account]`. Thresholds SHALL come from `[account_routing]` `five_hour_threshold_pct` and `weekly_threshold_pct` (default 90 each). The chosen account SHALL be stored as the task's account with an auto-routed flag set, and the choice and the scores SHALL be logged.

#### Scenario: Capped account skipped
- **WHEN** `default` is at 100% of its 5-hour window, `personal` is at 0% on both windows, and a task resolves to `auto`
- **THEN** the task is created on `personal`

#### Scenario: Weekly window counts too
- **WHEN** `default` is at 10% (5-hour) and 88% (weekly), `personal` is at 40% (5-hour) and 20% (weekly), both thresholds 90
- **THEN** `personal` is chosen (score 50 vs 2)

#### Scenario: Unknown counts as available
- **WHEN** `default` is at 60% (5-hour) and `personal` has no reading
- **THEN** `personal` is chosen

#### Scenario: All unknown
- **WHEN** no account has a reading
- **THEN** `default` is chosen

#### Scenario: Every account over threshold
- **WHEN** `default` is at 99% and `personal` at 95% of their 5-hour windows
- **THEN** `personal` is chosen and the over-threshold outcome is logged

### Requirement: A Claude task can be moved to another account

The system SHALL provide an account-move operation for a task on a Claude backend. The target SHALL be a configured account, `default`, or `auto`; `auto` SHALL apply the auto selection rule over the accounts that support the backend, excluding the task's current account. The move SHALL be rejected without side effects when the task does not exist, its backend is not Claude, the target is unknown, equals the current account, or does not support the backend, `auto` has no other candidate, or the task's session is live and not idle. Otherwise the system SHALL stop a live session and wait for it to exit, ensure the target's Claude config dir exists, copy the task's newest transcript (`<session-id>.jsonl` plus its `<session-id>/` sidecar directory when present) from the source account's `projects/<encoded worktree>/` into the target's, overwriting an older copy, and leave the source copy in place. It SHALL then store the target as the task's account with the auto-routed flag cleared, and resume the task's session under the target account on the same task, worktree, branch, and hera bindings. When no transcript exists, the move SHALL still complete, the session SHALL start fresh, and the result SHALL report that the conversation was not resumed. Every move SHALL be logged under `[account]` with the task, source, and target.

#### Scenario: Stuck agent moved and resumed
- **WHEN** an idle task on `default`, whose session hit its 5-hour cap, is moved to `personal`
- **THEN** its transcript is copied into `personal`'s projects dir, the task's account becomes `personal`, and the session resumes the same conversation under `personal`

#### Scenario: Move to auto
- **WHEN** a task on `default` is moved with target `auto` and `personal` is the only other candidate
- **THEN** the task moves to `personal`

#### Scenario: Busy session refused
- **WHEN** a task's session is actively producing output
- **THEN** the move is rejected and nothing changes

#### Scenario: Non-Claude task refused
- **WHEN** a codex task is moved
- **THEN** the move is rejected naming the backend

#### Scenario: Moving back keeps history
- **WHEN** a task is moved from `default` to `personal` and later back to `default`
- **THEN** it resumes the newest conversation, including turns taken on `personal`
