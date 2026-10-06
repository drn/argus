# Mobile PWA

## ADDED Requirements

### Requirement: Swipe gestures on task rows change task status

The web task list SHALL let a touch user swipe a task row horizontally to change its status. Swiping a row left SHALL mark the task `complete`. Swiping a `complete` row right SHALL mark it `in_review` (active again). While dragging, the row SHALL translate with the finger and reveal a labelled, coloured action behind it; releasing beyond a commit threshold SHALL apply the status change via `POST /api/tasks/{id}/status`, and releasing short of it SHALL snap the row back with no request. Predominantly vertical gestures SHALL be left to list scrolling. A swipe in a direction that does not apply to the row's status SHALL NOT send a request. A completed swipe SHALL NOT also open the task detail.

#### Scenario: Swipe left completes a task

- **WHEN** the user swipes a non-complete task row left past the commit threshold
- **THEN** the client posts `{"status":"complete"}` to `/api/tasks/{id}/status` and the list re-renders with the task shown as complete

#### Scenario: Swipe right reactivates a complete task

- **WHEN** the user swipes a `complete` task row right past the commit threshold
- **THEN** the client posts `{"status":"in_review"}` to `/api/tasks/{id}/status` and the list re-renders with the task no longer complete

#### Scenario: Short swipe snaps back

- **WHEN** the user releases a horizontal drag before the commit threshold
- **THEN** the row animates back to rest and no request is sent

#### Scenario: Vertical scroll is not hijacked

- **WHEN** a touch gesture on a row moves mostly vertically
- **THEN** the list scrolls normally and the row does not translate

#### Scenario: Inapplicable direction does nothing

- **WHEN** the user swipes a `complete` row left, or a non-complete row right
- **THEN** no status request is sent and the row snaps back

#### Scenario: Request failure restores the row

- **WHEN** the status request fails
- **THEN** the row snaps back to its prior state and an error toast is shown

#### Scenario: Swipe does not trigger a tap

- **WHEN** a swipe gesture ends over the row
- **THEN** the task detail view is not opened
