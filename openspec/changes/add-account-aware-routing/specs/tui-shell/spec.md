## MODIFIED Requirements

### Requirement: Status bar surfaces cached usage-probe readings

The status bar SHALL permanently render the daemon's cached weekly-usage readings alongside the default task-count summary, regardless of whether anything currently needs operator attention. When the tier list contains Claude tiers on more than one distinct signed-in identity, the Claude readout SHALL render one segment per distinct identity, labelled by its account name (`default` rendered as `cla`), followed by the Codex segment (e.g. `cla 100% · personal 36% · cdx 25%`); identities collapsed into one candidate SHALL render as a single segment. Otherwise it SHALL render the single-account form (e.g. `cla 42% · cdx 76%`). A reading that is absent (never probed) or stale (older than that probe's own staleness window) SHALL render as a literal `—`, visually distinct from a percentage, rather than a number that could be hours old and indistinguishable from a fresh one. The TUI SHALL poll the daemon for these readings on a fixed interval (`usageRecheckInterval`) no more frequent than the daemon's own probe cadence requires, reusing the daemon-connection source its periodic binary-skew recheck already holds.

In `--remote` mode and the in-process-runner fallback (no daemon connection to poll), every segment SHALL permanently render as unknown (`—`) rather than erroring or blocking — there is no daemon-process-local cache to ask in either mode.

#### Scenario: Fresh readings on both sides render as percentages

- **WHEN** the daemon reports both the Claude and Codex usage readings as fresh, with a single Claude identity
- **THEN** the status bar renders both as percentages, e.g. `cla 42% · cdx 76%`

#### Scenario: Two distinct Claude identities render as two segments

- **WHEN** Claude tiers exist for `default` and `personal`, signed into different logins, with fresh readings
- **THEN** the status bar renders `cla <pct> · personal <pct> · cdx <pct>`

#### Scenario: Collapsed identities render as one segment

- **WHEN** `default` and `personal` are signed into the same login
- **THEN** the status bar renders a single Claude segment, `cla <pct>`, followed by the Codex segment

#### Scenario: A stale or never-probed reading renders as unknown, not a percentage

- **WHEN** the daemon reports a reading as absent or stale for any segment
- **THEN** that segment renders `—`, never a percentage, even if a previous percentage had been displayed

#### Scenario: The readout does not depend on daemon-side logging

- **WHEN** the daemon's own probe-related log lines are structurally unobservable (the daemon process never initializes its debug logger)
- **THEN** the status bar readout is unaffected, since it is relayed directly from the daemon's in-memory cache via RPC, not derived from logs

#### Scenario: No daemon connection leaves both sides unknown

- **WHEN** the TUI is running in `--remote` mode or the in-process-runner fallback, with no local daemon connection to poll
- **THEN** every segment of the readout permanently renders `—`
