## MODIFIED Requirements

### Requirement: Enter refuses to restart a dead-session worker awaiting close-out

Pressing `Enter` on a hera rail row with a DEAD session (no live process at all) SHALL start it via the ordinary dead-session restart path (`startSession`) for a coordinator role, UNCHANGED. For a worker or freelance role, the system SHALL first check the SAME `HeraWorkerAwaitingCloseout` predicate the `Worker revive restores in_progress` requirement's guard uses (`meta:hera.ready_to_close`, or a terminal `done`/`failed` role-status) and, if the task is awaiting close-out, SHALL refuse to restart the session: the task's status is left completely unchanged and no session is started.

Refusal SHALL toggle a persistent, in-pane banner on the agent pane bound to the task, rather than relying solely on a footer notice: the FIRST `Enter` press against a closed-out row SHALL arm the banner (a status-bar message is also shown, for continuity with the pre-existing behavior) — the banner SHALL replace whatever the pane would otherwise render (its dead-session replay content, or the "Session not running" placeholder) for as long as it stays armed. A SECOND, immediately-following `Enter` press SHALL dismiss the banner, at which point the pane SHALL render exactly what an ordinary dead-session pane renders — its last recorded session output if any was logged, else the same placeholder — reusing the pane's existing dead-session rendering with no new PTY, process, or emulator spawned for this view. Further `Enter` presses SHALL keep toggling the banner on and off; there is no separate third state. The banner state is scoped to the pane's current task binding and SHALL reset — re-arming on the very next `Enter` — whenever the pane is rebound to a different task and back, including a rebind to the SAME task after navigating away.

This closes a gap discovered by live-testing `hera_accept`: the dead-session branch previously called `startSession` unconditionally for every role kind, which unconditionally flips the task to in_progress with zero Hera awareness. Because the underlying session had nothing left to resume, it exited almost immediately, and the ordinary post-exit rule then rolled the task to in_review – silently undoing an explicit `hera_accept` (or a self-reported-done worker's `ready_to_close` stamp) even though `Enter` is not itself an explicit revive. The refusal makes the "a premature accept can only be undone via an explicit revive" guarantee (`hera_accept`'s own tool description) hold for every UI trigger, not only the two that happened to call `ReviveHeraWorkerToInProgress` already (the live-session kick and `hera_revive`). The in-pane banner (`add-hera-closeout-banner`) closes a SEPARATE, UX-only gap: the original fix's only feedback was a 15-second-TTL status-bar notice, easy to miss, that left the pane's own content exactly as it already was — never actually showing anything or letting the operator proceed.

Derived from: `internal/tui/heraactions.go` (`heraReattach`, `heraTaskClosedOut`, `heraReattachClosedOut`), `internal/db/hera.go` (`HeraWorkerAwaitingCloseout`), `internal/tui/terminal/terminalpane.go` (`ShowClosedOutBanner`/`DismissClosedOutBanner`/`ClosedOutBannerShown`).

#### Scenario: Enter on a dead-session accepted (complete) worker is refused and arms the banner

- **WHEN** a worker's task is `complete` (accepted via `hera_accept`) and has no live session, and the operator presses `Enter` on its rail row for the first time this visit
- **THEN** no session is started, the task's status stays `complete`, the status bar shows a closed-out message, and the bound agent pane shows the persistent closed-out banner instead of its replay content or placeholder

#### Scenario: Enter on a dead-session self-reported-done worker is refused and arms the banner

- **WHEN** a worker's task carries `meta:hera.ready_to_close` (or a terminal `done`/`failed` role-status) and has no live session, and the operator presses `Enter` on its rail row for the first time this visit
- **THEN** no session is started, the task's status is left unchanged, and the bound agent pane shows the closed-out banner

#### Scenario: A second, immediately-following Enter dismisses the banner and shows read-only output

- **WHEN** the closed-out banner is currently armed on the agent pane and the operator presses `Enter` again
- **THEN** no session is started, the banner is dismissed, and the pane renders its last recorded session output read-only (or the "Session not running" placeholder if none was ever recorded) — reusing the pane's existing dead-session rendering, spawning no new PTY, process, or emulator

#### Scenario: A third Enter re-arms the banner

- **WHEN** the closed-out banner has been dismissed on the agent pane and the operator presses `Enter` again
- **THEN** the banner re-arms, replacing whatever the pane was rendering

#### Scenario: Leaving and returning to the row resets the banner

- **WHEN** the operator navigates the rail selection away from a closed-out row (rebinding the agent pane to a different task or unbinding it) and then back to the SAME row
- **THEN** the banner is not armed on return, so the next `Enter` press shows the banner again rather than immediately dismissing to read-only

#### Scenario: Enter still restarts a dead session with no close-out marker

- **WHEN** a worker or freelance task has no live session and carries no close-out marker
- **THEN** `Enter` restarts it exactly as before this change, and no closed-out banner is ever shown

#### Scenario: Coordinators are unaffected

- **WHEN** a coordinator role's dead session is reattached via `Enter`
- **THEN** it is restarted unconditionally, exactly as before this change – coordinators have no close-out concept and never show the closed-out banner
