## MODIFIED Requirements

### Requirement: Uncertain permission input is corroborated per field

A live bridge SHALL remain the source throughout pending-or-running permission intervals. Display consumers SHALL preserve bridge busy state and resolve needs-input as bridge input AND the raw scraper needs-input for uncertain permissions. Uncertain means confidence `pending-or-running` or missing. Exact permission, `pending` permission, question and plan input SHALL use bridge state directly. Source logs SHALL fire only for real liveness or authority transitions, never for an uncertain field alone. REST status, daemon-connected TUI, web, macOS and push SHALL apply the same display rule. The separate shadow permission diagnostic bucket SHALL compare raw bridge needs-input against scraper input. Delivery routing and claim-settlement evidence SHALL retain raw bridge needs-input as a conservative safety gate.

#### Scenario: Hook-approved long tool execution

- **WHEN** a busy live bridge reports a pending-or-running permission and the scraper reports no input request
- **THEN** it remains busy without displayed needs-input and without a source transition

#### Scenario: Real permission dialog

- **WHEN** both a live uncertain permission and the scraper report needs-input
- **THEN** displayed needs-input is true and the session is quiescent while bridge busy remains unchanged

#### Scenario: Exact permission

- **WHEN** a live bridge reports an exact permission request and the scraper disagrees
- **THEN** displayed needs-input is true immediately

#### Scenario: Pending permission not seen running

- **WHEN** a busy live bridge reports a permission with confidence `pending` and the scraper reports no input request
- **THEN** displayed needs-input is true immediately and the session is quiescent while bridge busy remains unchanged

#### Scenario: Prior exact input must not corroborate a later uncertain permission

- **WHEN** an exact dialog was published and a later uncertain permission arrives without raw scraper input
- **THEN** the previous published verdict does not corroborate the uncertain permission

#### Scenario: Telemetry hook unavailable

- **WHEN** the engine refuses first-party telemetry observation for user mods
- **THEN** the mod retains its grace-period permission derivation and its running-evidence refinement

#### Scenario: Raw permission disagreement remains observable

- **WHEN** raw bridge uncertain permission input is true and scraper input is false beyond the shadow settling period
- **THEN** disagreement counts under needs_input_permission while the headline needs_input counters remain unaffected

#### Scenario: Delivery blocks despite display corroboration clearing input

- **WHEN** the bridge is idle with raw uncertain permission input true and scraper input false
- **THEN** delivery is blocked and a fresh heartbeat does not establish settled absence for a claim

### Requirement: Bridge mod derives needs-input from pending tool calls
The mod SHALL report `needs_input` when a user dialog is pending and `cleared` when none remain. `AskUserQuestion` and `ExitPlanMode` calls SHALL be pending from `tool.call` pre to post. For any other tool, a `tool.check` verdict of `ask` whose `tool.call` is still pending after a grace period of about 1.5 seconds SHALL count as pending until `tool.call` post or until the call is seen running, whichever comes first, with confidence `pending`. A call is seen running when the engine renders a `ToolProgress` row carrying its `tool_use_id`; a call seen running SHALL NOT count as pending. The mod's `ToolProgress` hook SHALL return the engine's rendering unchanged and SHALL NOT wait on event delivery. The mod SHALL NOT rely on `classic.PermissionRequest` or `classic.Notification`.

#### Scenario: Question dialog
- **WHEN** `AskUserQuestion` begins and has not completed
- **THEN** `needs_input` with kind `question` is posted, and `cleared` is posted when the call completes

#### Scenario: Auto-approved permission ask
- **WHEN** `tool.check` returns `ask` and the `tool.call` completes within the grace period
- **THEN** neither `needs_input` nor `cleared` is posted

#### Scenario: Unanswered permission dialog
- **WHEN** `tool.check` returns `ask` and the `tool.call` is still pending after the grace period, with no `ToolProgress` row for it
- **THEN** `needs_input` with kind `permission` and confidence `pending` is posted, and `cleared` is posted when the call completes

#### Scenario: Approved command seen running
- **WHEN** a permission needs-input is active for a call and a `ToolProgress` row carrying that call's `tool_use_id` is rendered
- **THEN** `cleared` is posted while the call keeps running, provided no other dialog is pending

#### Scenario: Running before the grace elapses
- **WHEN** a `ToolProgress` row for an `ask` call is rendered before the grace period elapses
- **THEN** the call never qualifies and no `needs_input` is posted for it

#### Scenario: Exact dialogs
- **WHEN** `needs_input` is posted for `AskUserQuestion` or `ExitPlanMode`
- **THEN** its kind is `question` or `plan` and its confidence is `exact`
