## MODIFIED Requirements

### Requirement: Enter refuses to restart a dead-session worker awaiting close-out

Pressing `Enter` on a DEAD session (no live process at all) SHALL start it via the ordinary dead-session restart path (`startSession`) for a coordinator role, UNCHANGED. For a worker or freelance role, the system SHALL first check the SAME `HeraWorkerAwaitingCloseout` predicate the `Worker revive restores in_progress` requirement's guard uses (`meta:hera.ready_to_close`, or a terminal `done`/`failed` role-status) and, if the task is awaiting close-out, SHALL refuse to restart the session on the first two Enter presses: the task's status is left completely unchanged and no session is started. This check and its refusal behavior SHALL be identical whether the task is reached via the Hera tab's rail row or the plain Tasks tab's agent view — the same underlying task refuses the same way regardless of which surface it is viewed from.

Refusal SHALL toggle a persistent, in-pane banner on the agent pane bound to the task (whichever pane that is for the surface in use), rather than relying solely on a footer notice: the FIRST `Enter` press against a closed-out task SHALL arm the banner (a status-bar message is also shown, for continuity with the pre-existing behavior) — the banner SHALL replace whatever the pane would otherwise render (its dead-session replay content, or the "Session not running" placeholder) for as long as it stays armed. A SECOND, immediately-following `Enter` press SHALL dismiss the banner, at which point the pane SHALL render exactly what an ordinary dead-session pane renders — its last recorded session output if any was logged, else the same placeholder — reusing the pane's existing dead-session rendering with no new PTY, process, or emulator spawned for this view. A THIRD, immediately-following `Enter` press SHALL actually revive the task: it SHALL clear both underlying close-out signals (`meta:hera.ready_to_close` and any terminal `done`/`failed` role-status on a live binding) and then start the session via the ordinary dead-session restart path, exactly as if no close-out marker had ever been present — this is a deliberate operator override of the guard, not a call to the stricter `Worker revive restores in_progress` guard (which continues to refuse this case in its own, separate contexts). The banner state (including how many of the three steps have been taken) is scoped to the pane's current task binding and SHALL reset — restarting the sequence at the first step — whenever the pane is rebound to a different task and back, including a rebind to the SAME task after navigating away.

Whichever surface the task is viewed from, the auto-start path that would otherwise restart a dead session merely on navigating to it (e.g. the plain Tasks tab's task-selection flow) SHALL also respect this guard: it SHALL skip starting the session for a closed-out task without arming the banner (mere navigation is not an `Enter` press), leaving the pane to show its ordinary dead-session view until the operator presses `Enter` explicitly.

This closes a gap discovered by live-testing `hera_accept`: the dead-session branch previously called `startSession` unconditionally for every role kind, which unconditionally flips the task to in_progress with zero Hera awareness. Because the underlying session had nothing left to resume, it exited almost immediately, and the ordinary post-exit rule then rolled the task to in_review – silently undoing an explicit `hera_accept` (or a self-reported-done worker's `ready_to_close` stamp) even though `Enter` is not itself an explicit revive. The refusal makes the "a premature accept can only be undone via an explicit revive" guarantee (`hera_accept`'s own tool description) hold for every UI trigger, not only the two that happened to call `ReviveHeraWorkerToInProgress` already (the live-session kick and `hera_revive`). The in-pane banner (`add-hera-closeout-banner`) closes a SEPARATE, UX-only gap: the original fix's only feedback was a 15-second-TTL status-bar notice, easy to miss, that left the pane's own content exactly as it already was — never actually showing anything or letting the operator proceed. `add-enter-closeout-guard-parity` closed a THIRD gap: the guard and banner originally protected only the Hera tab, leaving the plain Tasks tab's own Enter-to-restart and auto-start paths completely unguarded for the SAME underlying task. `add-force-revive-third-enter` then reversed the original "no separate third state" decision after dogfood testing found it unhelpful: three deliberate Enter presses in a row is unambiguous operator intent to reopen the task, not an accidental repeat.

Derived from: `internal/tui/heraactions.go` (`heraReattach`, `heraTaskClosedOut`, `heraReattachClosedOut`, `heraKickRestartClosedOut`), `internal/tui/app.go` (`reattachClosedOut`, `forceReviveClosedOut`, `onTaskSelect`, `handleAgentKey`), `internal/db/hera.go` (`HeraWorkerAwaitingCloseout`, `ClearHeraCloseout`), `internal/tui/terminal/terminalpane.go` (`ShowClosedOutBanner`/`DismissClosedOutBanner`/`ClosedOutBannerShown`/`ClosedOutReadyToRevive`/`ClearClosedOutState`).

#### Scenario: Enter on a dead-session accepted (complete) worker is refused and arms the banner

- **WHEN** a worker's task is `complete` (accepted via `hera_accept`) and has no live session, and the operator presses `Enter` on it for the first time this visit, from either the Hera tab or the plain Tasks tab
- **THEN** no session is started, the task's status stays `complete`, the status bar shows a closed-out message, and the bound agent pane shows the persistent closed-out banner instead of its replay content or placeholder

#### Scenario: Enter on a dead-session self-reported-done worker is refused and arms the banner

- **WHEN** a worker's task carries `meta:hera.ready_to_close` (or a terminal `done`/`failed` role-status) and has no live session, and the operator presses `Enter` on it for the first time this visit
- **THEN** no session is started, the task's status is left unchanged, and the bound agent pane shows the closed-out banner

#### Scenario: A second, immediately-following Enter dismisses the banner and shows read-only output

- **WHEN** the closed-out banner is currently armed on the agent pane and the operator presses `Enter` again
- **THEN** no session is started, the banner is dismissed, and the pane renders its last recorded session output read-only (or the "Session not running" placeholder if none was ever recorded) — reusing the pane's existing dead-session rendering, spawning no new PTY, process, or emulator

#### Scenario: A third Enter actually revives the task

- **WHEN** the closed-out banner has been dismissed on the agent pane and the operator presses `Enter` again
- **THEN** the task's `meta:hera.ready_to_close` mark is cleared, any terminal `done`/`failed` role-status on its live binding is reset to `working`, and the session is started via the ordinary dead-session restart path — the banner does NOT re-arm

#### Scenario: Leaving and returning to the row resets the sequence

- **WHEN** the operator navigates away from a closed-out task (rebinding its agent pane to a different task or unbinding it) and then back to the SAME task
- **THEN** the banner is not armed on return and the next `Enter` press is treated as the FIRST step again (arming the banner), not as a continuation of a prior visit's progress toward a revive

#### Scenario: Enter still restarts a dead session with no close-out marker

- **WHEN** a worker or freelance task has no live session and carries no close-out marker
- **THEN** `Enter` restarts it exactly as before this change, and no closed-out banner is ever shown

#### Scenario: Coordinators are unaffected

- **WHEN** a coordinator role's dead session is reattached via `Enter`, from either tab
- **THEN** it is restarted unconditionally, exactly as before this change – coordinators have no close-out concept and never show the closed-out banner

#### Scenario: Mere navigation on the plain Tasks tab does not auto-restart a closed-out task

- **WHEN** the operator selects a closed-out worker/freelance task from the plain Tasks tab (no live session), without yet pressing `Enter`
- **THEN** the auto-start path SHALL skip starting the session, the banner SHALL NOT be armed, and the pane SHALL show its ordinary dead-session view until the operator explicitly presses `Enter`
