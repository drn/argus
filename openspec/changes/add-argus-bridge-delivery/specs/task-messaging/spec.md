## ADDED Requirements

### Requirement: Bridge task notices retain explicit acknowledgment

Task notices SHALL use the shared bridge delivery route when eligible with unchanged nudge payload trust boundaries. task_message_ack SHALL cancel queued or claimed notification delivery; bridge transport acknowledgment SHALL NOT mark the durable message read. Deadline abandonment and ambiguous delivery SHALL preserve the unread row.

#### Scenario: Task acknowledgment cancels pending bridge notice

- **WHEN** a recipient explicitly acknowledges its message before mod authorization
- **THEN** no later mod or PTY submission occurs for that notice
