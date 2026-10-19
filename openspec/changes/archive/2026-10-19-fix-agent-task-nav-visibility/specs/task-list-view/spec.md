## ADDED Requirements

### Requirement: Agent-view previous/next task follows the visible task list

In the Tasks-tab agent view, the previous-task and next-task actions (default `Cmd+Up` / `Cmd+Down`) SHALL move to the adjacent task in the task list's display order — Pinned, then Active projects alphabetically, then Archive — considering only tasks the list would show with the current substring filter and hide-hera-managed toggle applied. Tasks in folded projects SHALL remain reachable. When the current task is not visible in that order, or no adjacent visible task exists in the requested direction, the action SHALL do nothing.

#### Scenario: Hidden hera-managed tasks are skipped

- **WHEN** the hide-hera-managed toggle is ON and the next task in the raw list is a hera-spawned worker or live coordinator
- **THEN** next-task skips it and opens the next visible task

#### Scenario: Hera-managed tasks are reachable when shown

- **WHEN** the hide-hera-managed toggle is OFF
- **THEN** next-task and previous-task can land on hera-managed tasks

#### Scenario: Filtered-out tasks are skipped

- **WHEN** a substring filter is active
- **THEN** next-task and previous-task move only between tasks that match the filter

#### Scenario: Pinned tasks come first

- **WHEN** a pinned task is open and an unpinned active task exists
- **THEN** next-task opens the first active task, and previous-task from that task returns to the pinned one
