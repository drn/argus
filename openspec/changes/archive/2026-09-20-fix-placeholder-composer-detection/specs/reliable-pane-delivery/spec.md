## MODIFIED Requirements

### Requirement: Reliable inject-and-submit

The system SHALL provide a `ReliableNotify(taskID, text, deliveryID string, opts) func()` entry point that injects `text` into the named task's PTY exactly once and submits it using standalone carriage-return attempts as soon as it is safe to do so. Safety SHALL be decided primarily from the rendered recipient composer: an empty composer, including one whose only visible draft text is dim-styled Claude Code placeholder content, or one containing only previously injected Argus/Hera notice text is safe immediately; non-notice content is unsafe while it changes between snapshots and becomes safe after remaining unchanged for the stability window. Session idle and pane focus SHALL be fallback gates only when a supported composer cannot be identified. A delivery SHALL be recorded as submitted only after a freshly rendered composer confirms the submitted draft was consumed or materially changed; PTY output activity alone SHALL NOT acknowledge an attempt. The caller receives a cancel func; invoking it abandons the delivery if it is still pending.

#### Scenario: Empty composer submits immediately

- **WHEN** the rendered recipient composer is identifiable and empty
- **THEN** the notifier attempts delivery in the current reconcile cycle regardless of raw session idleness or pane focus

#### Scenario: Dim placeholder composer submits immediately

- **WHEN** the identifiable composer contains only dim-styled Claude Code placeholder text
- **THEN** the notifier treats it as empty and does not preserve or annotate the placeholder

#### Scenario: Previously injected notice submits immediately

- **WHEN** the rendered composer contains only one or more previously injected Argus/Hera notices
- **THEN** the notifier treats the content as stale, clears it, and attempts the current delivery immediately

#### Scenario: Changing non-notice content defers

- **WHEN** the rendered composer contains non-notice text that differs from its prior snapshot
- **THEN** the delivery remains pending and no PTY write occurs in that reconcile cycle

#### Scenario: Stable non-notice content recovers

- **WHEN** non-notice composer text remains unchanged for the stability window
- **THEN** the notifier preserves it, appends a do-not-act annotation and the current notice, and attempts submission

#### Scenario: Unknown composer uses conservative fallback

- **WHEN** the terminal renderer cannot identify a supported composer
- **THEN** the notifier attempts delivery only when the session is idle or content-idle and no human is focused

#### Scenario: Cancel before submit abandons delivery

- **WHEN** the caller invokes the cancel func returned by `ReliableNotify` before the delivery has been submitted
- **THEN** no PTY write occurs for that delivery on any subsequent tick
