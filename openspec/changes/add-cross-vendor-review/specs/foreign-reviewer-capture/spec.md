# foreign-reviewer-capture (delta)

## ADDED Requirements

### Requirement: Reviewer-mode prompt sentinel wrapping

When a session is spawned in reviewer-mode, the system SHALL wrap the review prompt with an instruction directing the agent to emit its review delimited by a fixed opening and closing sentinel (`<<<ARGUS_REVIEW>>>` and `<<<END_ARGUS_REVIEW>>>`), so a non-hera-aware foreign agent (which cannot call any MCP tool) still produces machine-locatable output.

#### Scenario: Reviewer-mode wraps the prompt

- **WHEN** a session is spawned in reviewer-mode with a review prompt
- **THEN** the delivered prompt instructs the agent to bracket its review between the opening and closing sentinels

#### Scenario: Non-reviewer spawns are unaffected

- **WHEN** a session is spawned without reviewer-mode
- **THEN** no sentinel wrapping is applied and the prompt is delivered unchanged

### Requirement: Sentinel extraction from the persisted session log

The system SHALL extract the reviewer's output as the text between the opening and closing sentinels in the session's persisted output log (`~/.argus/sessions/<taskID>.log`), which is written outside the worktree and survives worktree teardown. Extraction SHALL tolerate the surrounding terminal/UI noise that precedes and follows the sentinel block.

#### Scenario: Review extracted from the log

- **WHEN** the session log contains a sentinel-delimited review block
- **THEN** the system returns the text between the sentinels as the captured review

#### Scenario: Capture survives worktree teardown

- **WHEN** the reviewer's worktree has been removed but the session log remains
- **THEN** the captured review is still extractable from the log

#### Scenario: Missing sentinels reported, not fatal

- **WHEN** the session log contains no closing sentinel
- **THEN** the system returns a structured "no review captured" outcome rather than an error or partial garbage

### Requirement: Structured, addressable capture result

The captured review SHALL be exposed as a structured result addressable by the reviewer's task, so an orchestrator can read one clean field rather than scraping raw scrollback. The capture path SHALL NOT depend on the foreign agent calling any MCP tool.

#### Scenario: Orchestrator reads the captured result

- **WHEN** an orchestrator requests the capture result for a completed reviewer task
- **THEN** it receives the extracted review text (or the "no review captured" outcome) as a structured value keyed to that task

#### Scenario: No dependency on agent-side MCP

- **WHEN** the foreign reviewer never calls any MCP tool
- **THEN** the review is still captured (via log extraction), confirming capture does not rely on agent cooperation beyond emitting the sentinels
