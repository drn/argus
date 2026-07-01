# Idle / Needs-Input Detection

## Purpose

When an agent session stops generating and waits for the user, the orchestrator must recognize that the session is blocked so the UI can surface it and so destructive maintenance actions never silently dismiss a pending question. This capability defines how recent agent output is inspected to decide whether the agent is waiting on user input, and how that decision gates a width-driven session restart ("rerender kick") that would otherwise discard an in-flight prompt.
## Requirements
### Requirement: Needs-input detection from recent output

The system SHALL determine whether an agent is blocked waiting for the user by inspecting only the most recent window of its output. Detection MUST fire on either of two signals: the agent's numbered-selection prompt UI, or the agent's last visible transcript line ending with a question mark. Empty output SHALL never be treated as needs-input.

#### Scenario: Empty buffer is not blocked

- **WHEN** the output buffer is empty
- **THEN** the system reports the agent is not waiting for input

#### Scenario: Plain output is not blocked

- **WHEN** the recent output contains only ordinary work output with no selection prompt and no trailing question
- **THEN** the system reports the agent is not waiting for input

### Requirement: Selection-prompt UI is recognized regardless of wording or surrounding markup

The system SHALL treat the agent's numbered-selection widget as a needs-input signal whenever the visible text shows the selection cursor immediately followed (with zero or more spaces/tabs) by ANY numbered option (a number and a period), not only the first option — so a permission cursor a user has navigated down to option 2 or 3 is still caught. Detection MUST be based on this shared UI shape, not on any specific prompt wording, so permission prompts, edit confirmations, plan-mode confirms, and open-ended multiple-choice questions are all caught. Detection MUST survive interleaved color/escape sequences and cursor-positioning codes that produce a visible gap without any literal space byte.

The system SHALL ALSO treat the AskUserQuestion chooser footer as a needs-input signal. The chooser renders plain options whose selection cursor does NOT follow the numbered-option shape, so the footer is the robust matcher: it is present regardless of which option is highlighted. Footer detection MUST match an Enter-action affordance and an Esc-action affordance on the SAME line, tolerant of the action wording (e.g. "select", "confirm", "choose"), letter case, and the navigation hints/separators (`·`, `↑/↓`) rendered between them. The two affordances appearing on SEPARATE lines, or either affordance alone, MUST NOT fire.

#### Scenario: Permission prompt with a numbered selection

- **WHEN** the recent output ends with a selection cursor followed by a numbered list of options
- **THEN** the system reports the agent is waiting for input

#### Scenario: Permission prompt with the cursor navigated to a later option

- **WHEN** the selection cursor sits on option 2 or 3 (not the first option)
- **THEN** the system reports the agent is waiting for input

#### Scenario: Open-ended question with a numbered selection but no fixed phrasing

- **WHEN** the selection widget appears without any "Do you want to" phrasing
- **THEN** the system reports the agent is waiting for input

#### Scenario: AskUserQuestion chooser footer fires regardless of highlighted option

- **WHEN** the recent output shows the chooser footer with an Enter-action affordance and an Esc-action affordance on one line, separated by navigation hints
- **THEN** the system reports the agent is waiting for input

#### Scenario: Selection markup split by color escapes

- **WHEN** the cursor and the first option are separated only by color escape sequences and a literal space
- **THEN** the system reports the agent is waiting for input

#### Scenario: Selection markup with cursor-positioning instead of a space

- **WHEN** the cursor and the first option are separated by a cursor-positioning escape and no literal space byte exists between them
- **THEN** the system reports the agent is waiting for input

#### Scenario: A bare selection cursor without a numbered option does not fire

- **WHEN** the output contains the selection cursor glyph but it is not followed by a numbered option
- **THEN** the system reports the agent is not waiting for input

#### Scenario: A plain numbered list without the selection cursor does not fire

- **WHEN** the output contains a numbered list with no selection cursor preceding the first item
- **THEN** the system reports the agent is not waiting for input

#### Scenario: A lone footer affordance or split footer does not fire

- **WHEN** the output contains only one of the two chooser affordances, or both on separate lines
- **THEN** the system reports the agent is not waiting for input

### Requirement: Trailing-question detection is anchored to the input prompt box

The system SHALL treat the agent as waiting for input when the last non-blank line of the transcript above the rendered input prompt box ends with a question mark (ASCII `?` or full-width `？`). The search MUST be anchored to the prompt-box opener so that hint lines rendered below the box are excluded. Blank lines between the transcript and the prompt box MUST be skipped. When no prompt box is present in the recent output, the trailing-question signal SHALL NOT fire.

#### Scenario: Question above the prompt box fires

- **WHEN** the last content line above the input prompt box ends with a question mark
- **THEN** the system reports the agent is waiting for input

#### Scenario: Statement above the prompt box does not fire

- **WHEN** the last content line above the input prompt box does not end with a question mark
- **THEN** the system reports the agent is not waiting for input

#### Scenario: Full-width question mark fires

- **WHEN** the last content line above the prompt box ends with a full-width question mark
- **THEN** the system reports the agent is waiting for input

#### Scenario: Hint lines below the prompt box are ignored

- **WHEN** a statement sits above the prompt box and only a hint line containing a question mark sits below it
- **THEN** the system reports the agent is not waiting for input

#### Scenario: Trailing whitespace after the question mark still fires

- **WHEN** the last content line ends with a question mark followed by trailing whitespace
- **THEN** the system reports the agent is waiting for input

#### Scenario: Blank lines between transcript and prompt box are skipped

- **WHEN** multiple blank lines separate the trailing question from the prompt box
- **THEN** the system reports the agent is waiting for input

#### Scenario: No prompt box present skips the question heuristic

- **WHEN** the output contains a trailing question but no rendered input prompt box
- **THEN** the system reports the agent is not waiting for input

### Requirement: Detection scans only a bounded recent tail

The system SHALL only inspect a bounded window at the end of the output buffer. Signals appearing only in output older than this window SHALL NOT be detected, while a signal landing at the very end of the buffer MUST be detected even when far more older output precedes it.

#### Scenario: Signal at the end of a large buffer is detected

- **WHEN** the selection prompt appears at the very end of a buffer larger than the scan window
- **THEN** the system reports the agent is waiting for input

#### Scenario: Signal older than the scan window is not detected

- **WHEN** the selection prompt appears only at the start of a buffer and is followed by more output than the scan window
- **THEN** the system reports the agent is not waiting for input

### Requirement: Session-level blocked check over the live output ring

The system SHALL expose a session-level check that reports whether a given session is blocked on a user prompt by applying needs-input detection to that session's recent output tail. A nil session SHALL never be reported as blocked. This check is intended to be paired with an idle check by the caller, because a prompt the agent is still streaming past is not blocking.

#### Scenario: Nil session is not blocked

- **WHEN** the blocked check is given no session
- **THEN** the system reports the session is not blocked

#### Scenario: Session with plain output is not blocked

- **WHEN** a session's recent output contains only ordinary work output
- **THEN** the system reports the session is not blocked

#### Scenario: Session showing a selection overlay is blocked

- **WHEN** a session's recent output shows the numbered-selection prompt UI
- **THEN** the system reports the session is blocked

### Requirement: Rerender kick is gated on idle and needs-input state

The system SHALL decide whether to stop-and-resume a session to re-render its scrollback at a new terminal width based on, in order: whether a kick is possible at all, whether the width change is large enough to matter, whether the agent is idle, and whether the agent is blocked on a user prompt. A kick SHALL be skipped entirely when there is no session to resume or a kick is already in flight. A kick SHALL be skipped when the width change is below the configured margin. When the change is large enough but the agent is not idle, the decision SHALL be to defer because the agent is busy. When the change is large enough and the agent is idle but blocked on a user prompt, the decision SHALL be to defer because of the prompt, so that resuming the session does not silently dismiss the pending question. Only when all gates pass SHALL the decision be to kick.

#### Scenario: No session to resume skips the kick

- **WHEN** there is no session to resume (or a kick is already pending)
- **THEN** the decision is to skip

#### Scenario: Below-margin width change skips the kick

- **WHEN** the difference between the panel width and the session's initial width is smaller than the margin
- **THEN** the decision is to skip

#### Scenario: Busy agent defers the kick

- **WHEN** the width change exceeds the margin but the agent is not idle
- **THEN** the decision is to defer because the agent is busy

#### Scenario: Idle agent blocked on a prompt defers the kick

- **WHEN** the width change exceeds the margin and the agent is idle but is blocked on a user prompt
- **THEN** the decision is to defer because of the prompt

#### Scenario: All gates pass triggers the kick

- **WHEN** the width change exceeds the margin, the agent is idle, and it is not blocked on a prompt
- **THEN** the decision is to stop and resume the session

### Requirement: Width-margin threshold treats unknown initial width as sane

The system SHALL compute whether a width change is large enough to justify a kick from the absolute difference between the panel width and the session's recorded initial width, using a fixed minimum margin. An unknown initial width (reported as zero or less) SHALL be treated as already sane so that it never triggers a surprise restart.

#### Scenario: Unknown initial width never exceeds the threshold

- **WHEN** the session's initial width is unknown
- **THEN** the system reports the margin threshold is not exceeded

#### Scenario: Width difference at or above the margin exceeds the threshold

- **WHEN** the absolute difference between panel width and initial width is at least the margin
- **THEN** the system reports the margin threshold is exceeded

### Requirement: Needs-input is detected for a never-idle session via content stability

The system SHALL flag a running session as waiting for user input even when it
never enters the idle set, provided ONE of the following awaiting-input signals
is present in its recent (raw or emulated-screen) output AND its meaningful
content is unchanged across consecutive detection ticks:

1. An UNAMBIGUOUS selection-prompt signal — the numbered-selection cursor (on
   any option) or the chooser footer. This signal needs no further gating.
2. A FREE-TEXT trailing question (the last transcript line above the input
   prompt ends in a question mark) WHEN the agent's "working" affordance is
   ABSENT from the screen.

The agent's "working" affordance is the interrupt hint the agent renders WHILE
it is generating or executing (e.g. "esc to interrupt" / "ctrl+c to interrupt")
and REMOVES the moment it returns to the idle input prompt. Its ABSENCE is the
load-bearing discriminator for signal (2): a busy agent whose narration happens
to end in `?` and that briefly stalls on a spinner frame is content-stable AND
ends in `?`, so content stability ALONE is NOT a sufficient guard for the
free-text question — the working-affordance-absent gate is REQUIRED. When the
working affordance is present, the free-text question SHALL NOT be flagged by
this pass.

"Meaningful content" SHALL exclude animation/redraw chrome — spinner and timing
decoration lines, the rendered input/cursor prompt line, blank lines, and ANSI
escape sequences — and SHALL be robust to a session repainting the same frame a
varying number of times (e.g. an alt-screen prompt). This closes the gap where a
session parked at a prompt emits a steady trickle of redraw bytes that keep its
raw-output clock fresh, so it never goes idle and the idle-gated detector never
scans it — and the further gap (BUG-035) where a fullscreen agent parked at a
free-text question was caught by neither the idle pass (never idle) nor the
selection-only stability pass.

A session whose meaningful content CHANGES between ticks SHALL NOT be flagged by
this pass: a still-streaming agent that transiently shows a signal is not
blocked. The idle-gated detection and the sticky carry-forward pass remain
unchanged (they still honor the trailing-question heuristic behind the idle
gate); this content-stability pass is additive.

#### Scenario: Never-idle session parked at a selection prompt is flagged once content is stable

- **WHEN** a running session shows the selection-prompt signature and only its
  animation chrome (spinner, cursor blink, repaint) has changed since the
  previous tick
- **THEN** the system reports the agent is waiting for input

#### Scenario: Never-idle session parked at a free-text question with no working affordance is flagged

- **WHEN** a running session's last transcript line ends in a question mark, the
  agent's working affordance is absent from the screen, and only its animation
  chrome has changed since the previous tick
- **THEN** the system reports the agent is waiting for input

#### Scenario: Content-stable working agent ending in a question is not flagged

- **WHEN** a running session's meaningful content is stable across ticks and its
  last transcript line ends in a question mark, but the agent's "working"
  affordance (interrupt hint) is present on the screen
- **THEN** the content-stability pass does not flag it (the working agent is
  still generating, not awaiting input)

#### Scenario: Streaming session showing a signal transiently is not flagged

- **WHEN** a running session shows an awaiting-input signal but its meaningful
  transcript content has changed since the previous tick
- **THEN** the system reports the agent is not waiting for input

#### Scenario: First observation records but does not flag

- **WHEN** a never-idle session showing an awaiting-input signal is observed for
  the first time (no prior tick to compare against)
- **THEN** the system does not yet flag it, and records its content fingerprint
  so the next tick can compare

#### Scenario: Repaint count does not destabilize the decision

- **WHEN** a parked session's recent output contains the same static frame
  repainted a different number of times between ticks
- **THEN** the content is treated as stable and the agent is reported waiting for input

### Requirement: Content fingerprint excludes animation chrome and collapses repaint frames

The system SHALL expose a content fingerprint over a session's recent output that
is identical for two output tails differing only in animation/redraw chrome, and
different for tails differing in meaningful transcript content. The fingerprint
MUST strip ANSI sequences, drop spinner/timing decoration lines and the rendered
input/cursor prompt line, ignore blank lines, and de-duplicate repeated lines so
that repainted frames collapse rather than inflating or shifting the fingerprint.

#### Scenario: Animation-only difference fingerprints identically

- **WHEN** two snapshots of the same parked prompt differ only in the spinner
  glyph, the timing seconds, and cursor-positioning escapes
- **THEN** their content fingerprints are equal

#### Scenario: New transcript content fingerprints differently

- **WHEN** a later snapshot contains a new transcript line not present in the earlier one
- **THEN** their content fingerprints differ

### Requirement: Detection matches the emulated screen for cursor-addressed (alt-screen) prompts

The system SHALL recognize a selection-prompt / needs-input signal even when the
agent paints its prompt with cursor-addressed in-place redraws (a fullscreen /
alt-screen agent), where the prompt glyphs are not linearly adjacent in the raw
output stream. Detection SHALL therefore be able to match against the VISIBLE
SCREEN reconstructed by feeding the recent output tail through a terminal
emulator sized to the session's current dimensions, in addition to the raw
ANSI-stripped stream. Stripping ANSI escapes alone does NOT apply cursor
positioning, so a cursor-addressed prompt is invisible to a raw-text match; the
emulated screen places the glyphs where they actually render.

The raw-text match SHALL remain the fast path: a linear (main-screen) agent
whose prompt IS linearly present in the stream MUST be detected exactly as before
and MUST NOT depend on emulation. Emulation SHALL be used as a fallback when the
raw match misses (or unconditionally, provided linear behavior is preserved).
When the session's true dimensions are unknown, a sane default terminal size
(80×24) SHALL be used. This guarantee applies to BOTH the selection-prompt signal
and the never-idle content-stability pass, so an alt-screen prompt is flagged
without waiting for a view-triggered resize/repaint.

#### Scenario: Cursor-addressed alt-screen prompt is detected via the emulated screen

- **WHEN** a running session's recent output paints a numbered-selection prompt
  using cursor-positioning such that the selection cursor and the first option are
  not linearly adjacent in the raw byte stream (so a raw ANSI-stripped match
  misses)
- **THEN** the system reconstructs the visible screen, finds the selection
  signature there, and reports the agent is waiting for input

#### Scenario: Linear prompt is still detected without emulation

- **WHEN** a session's selection prompt is linearly present in the raw output
  stream
- **THEN** the system detects it via the raw-text fast path, exactly as before

#### Scenario: Plain alt-screen output without a prompt is not flagged

- **WHEN** a fullscreen agent is producing ordinary work output with no selection
  prompt on the visible screen
- **THEN** the system reports the agent is not waiting for input

#### Scenario: Never-idle alt-screen prompt is flagged once the emulated screen is stable

- **WHEN** a running, never-idle session shows a cursor-addressed selection prompt
  and its EMULATED screen is unchanged across consecutive detection ticks (only
  off-screen repaint / spinner chrome differs)
- **THEN** the system reports the agent is waiting for input

#### Scenario: Streaming alt-screen agent is not flagged

- **WHEN** a fullscreen agent's emulated screen content changes between detection
  ticks
- **THEN** the system reports the agent is not waiting for input

### Requirement: Needs-input flag clears on user input or archive, never on signal decay

The system SHALL keep a session flagged as waiting for user input — including a
flag raised by the trailing-question heuristic (the last transcript line ends in
`?`) — for as long as the signal remains present, with NO time-based or
idle-based decay. The flag SHALL clear only when EITHER (a) the user delivers new
input to that session after the flag was raised, OR (b) the session's task is
archived. Input delivered to a DIFFERENT session SHALL NOT clear this one.

Clear-on-input SHALL be deterministic and MUST NOT depend on the prompt or
question text scrolling out of the recent-output tail: the system records, per
flagged task, the session's last-input timestamp observed when the task first
entered the needs-input set, and removes the task from the set — and suppresses
re-adding it on the same tick — once the session's last-input timestamp advances
past that recorded baseline, even if the question text still matches in the tail.
The recorded baseline SHALL be dropped when the task leaves the set (its signal
disappears), so that a fresh question raised after the user's response re-arms
the flag.

Clear-on-archive SHALL remove an archived task from the needs-input set
regardless of its detection signal, so it stops surfacing `?` and stops rolling
up to ancestor coordinators.

This clear logic SHALL be applied identically by the daemon-side detector and
the TUI-side detector. The trailing-question entry heuristic, the idle gate, the
sticky carry-forward pass, and the content-stability / emulated-screen guards
are unchanged; this requirement governs only when an already-detected signal is
removed from the published set.

#### Scenario: Free-text question is flagged and persists indefinitely without input

- **WHEN** an agent ends a turn on a free-text question and no input is delivered
  to its session across many detection ticks
- **THEN** the system keeps reporting the agent is waiting for input on every
  tick (no time-based or idle-based decay)

#### Scenario: User input clears the flag even while the question still matches the tail

- **WHEN** a session is flagged waiting for input and the user then delivers new
  input to that session, and the question text still matches in the recent-output
  tail
- **THEN** the system removes the session from the needs-input set on the next
  tick and does not re-add it while the same input remains the latest

#### Scenario: Input to a different session does not clear this one

- **WHEN** a session A is flagged waiting for input and the user delivers input
  only to a different session B
- **THEN** session A remains flagged waiting for input

#### Scenario: Archiving a flagged task clears its flag

- **WHEN** a session is flagged waiting for input and its task is archived
- **THEN** the system removes it from the needs-input set regardless of its
  detection signal

#### Scenario: A fresh question after a response re-arms the flag

- **WHEN** a session's flag was cleared by user input, the agent then produces
  output that no longer shows any needs-input signal, and later ends a new turn
  on another question
- **THEN** the system reports the agent is waiting for input again

### Requirement: Content-aware idle for continuously-repainting (fullscreen) agents

The system SHALL recognize a session as idle even when its raw PTY output never
quiesces, provided its VISIBLE screen has stopped changing. A fullscreen
(alt-screen) agent parked at its prompt emits continuous repaint/animation bytes
(cursor blink, spinner timing line, alt-screen redraws), so a raw-byte idle
clock never fires for it. The system SHALL therefore compute a "content-idle"
signal: a running session that is NOT already raw-idle is content-idle when its
animation-stripped EMULATED-screen fingerprint has been UNCHANGED for at least
the idle threshold AND the agent's "working" affordance (the interrupt hint Claude
renders while generating, e.g. "esc to interrupt") is ABSENT.

The fingerprint MUST be taken over the reconstructed (vt-emulated) screen, not
the raw byte stream, because a fullscreen agent's raw bytes never stabilize while
it repaints. The "working"-affordance gate is load-bearing: content stability
ALONE is not sufficient (a busy agent stalled on a spinner frame for a tick is
content-stable yet still working), so a session showing the interrupt affordance
MUST NOT be treated as content-idle regardless of stability. Computation MUST run
off the hot paint path (on the periodic watcher/TUI tick), reusing the shared
screen-renderer and fingerprint machinery — no second emulator path.

Content-idle is an ADDITIVE signal combined with the existing raw-byte idle
classification; a session that already quiesces (a non-fullscreen agent) is
classified IDENTICALLY to before. The session's own raw-byte `IsIdle()`
predicate is unchanged.

#### Scenario: Parked fullscreen agent becomes content-idle

- **WHEN** a running session that never reaches the raw-idle set shows a stable
  emulated screen (only spinner/cursor animation changes) with no "working"
  affordance, across at least the idle threshold
- **THEN** the system classifies it as content-idle

#### Scenario: Working fullscreen agent is not content-idle

- **WHEN** a running session's emulated screen shows the "working" affordance
  ("esc to interrupt"), even if the rest of the screen is momentarily stable
- **THEN** the system does NOT classify it as content-idle

#### Scenario: Streaming fullscreen agent is not content-idle

- **WHEN** a running session's emulated screen content changes from one tick to
  the next
- **THEN** the system does NOT classify it as content-idle (the stability timer
  resets on every content change)

#### Scenario: Already-idle and non-fullscreen sessions are unaffected

- **WHEN** a session is already raw-idle
- **THEN** the content-idle pass skips it (it is already idle) and a
  non-fullscreen agent that quiesces is classified exactly as before

### Requirement: Idle-push fires once on the content-idle transition

The system SHALL fold the content-idle set into the idle set used for the
busy→idle idle-push transition and the `session.idle` event, so a fullscreen
agent that goes content-idle fires an idle notification. Firing MUST remain
exactly-once per work cycle: after a push fires for a task, no further push fires
for it until new input arrives, so a content-idle signal that flaps (or is
re-asserted every tick while the agent stays parked) MUST NOT produce repeated
pushes. Non-fullscreen agents — already in the raw-idle set — MUST see no change
in idle-push behavior.

#### Scenario: Fullscreen content-idle fires one push

- **WHEN** a fullscreen session that received user input goes content-idle and
  remains content-idle across many ticks
- **THEN** the system fires exactly one idle push for that work cycle

#### Scenario: No push without an input cycle

- **WHEN** a session goes content-idle but no input has ever been delivered to it
- **THEN** the system fires no idle push (the input-presence gate is unchanged)

### Requirement: A live Hera role surfaces needs-input regardless of bound-task status

The system SHALL surface the detected needs-input `(?)` indicator on the Hera rail for ANY role that holds a LIVE binding (worker, coordinator, or freelance) whenever its bound task is in the content-aware needs-input set, REGARDLESS of that task's workflow status. A hera worker deliberately remains in `in_review` (with `meta:hera.ready_to_close` set) while its session lingers alive for the coordinator to close it out; rolling a worker to `in_review` keeps its binding live and never touches the session, so a still-alive worker can genuinely block on a prompt in that state and MUST surface `(?)` then.

The worker `in_progress` gate that previously suppressed this MUST NOT be applied to a live role. The needs-input set is content-aware (a task is flagged only while it shows a CURRENT awaiting-input signal and clears on user input or archive), so it already distinguishes "live at a real prompt" from "idling at a stale done summary" — the task-status gate is not needed to suppress stale markers.

The pre-existing protection against a FINISHED worker pinning `(?)` forever on every ancestor (the rollup never clearing) SHALL be preserved via LIVENESS, not task status: a worker is finished when its SESSION EXITS, which ENDS its binding, dropping the role from the live-binding branch so it no longer surfaces or rolls up `(?)`. A worker idling at a done summary with no interactive affordance is additionally never in the content-aware set.

The flat task-list `(?)` indicator (the always-visible, non-tree surface) is OUT of scope of this requirement and remains gated on `in_progress`; only the Hera rail (the orchestration tree, where a role's liveness is the meaningful signal) surfaces a live non-`in_progress` role.

#### Scenario: Live worker in in_review at a prompt surfaces (?)

- **WHEN** a hera worker's bound task has rolled to `in_review` (its binding still live) and it is in the content-aware needs-input set
- **THEN** the system surfaces the needs-input `(?)` on the worker's row and rolls it up to the ancestor coordinator

#### Scenario: Exited worker does not surface (?) even while flagged

- **WHEN** a hera worker's session has exited (its binding ended) but its task still lingers in the needs-input set
- **THEN** the system does NOT surface `(?)` on the worker's row or in the ancestor coordinator's rollup

#### Scenario: Hera rail feed admits a hera-bound task regardless of status

- **WHEN** building the Hera rail needs-input feed from the sticky set
- **THEN** the system keeps any task that is `in_progress` OR is bound to any hera role (worker or coordinator), and drops a task that is neither

### Requirement: An actively-blocked role's needs-input outranks the ready-to-close glyph

The shared role status-icon classifier SHALL rank the needs-input indicator ABOVE the `ready_to_close` review glyph. A worker stamped `ready_to_close` by the done-roll that is ALSO genuinely blocked on a user prompt is not "ready to close" — the actionable `(?)` MUST be shown so the user is not misled into closing out a worker that is waiting on them. Because needs-input is content-aware upstream, a `ready_to_close` worker merely idling at its done summary (no interactive affordance) is never flagged and still renders the review glyph. This precedence is applied identically by the rail and the plan-view node projection (the single shared classifier).

#### Scenario: Ready-to-close worker at a prompt shows the needs-input glyph

- **WHEN** a role is stamped `ready_to_close` AND its needs-input signal (own or subtree rollup) is set
- **THEN** the status icon renders the needs-input glyph, not the review glyph

#### Scenario: Ready-to-close worker not blocked shows the review glyph

- **WHEN** a role is stamped `ready_to_close` and has no needs-input signal
- **THEN** the status icon renders the review glyph

### Requirement: A live Hera role's working spinner is gated on a running, non-idle session, not bound-task status

The system SHALL animate the Hera rail working spinner for ANY role that holds a LIVE binding whose session is RUNNING and NOT idle, REGARDLESS of that role's bound-task workflow status. The activity predicate (`RoleView.IsActive`) that sources the spinner MUST be liveness AND session-running AND not-session-idle; the bound task being `in_progress` MUST NOT be an additional gate. A hera worker deliberately remains in `in_review` (with `meta:hera.ready_to_close` set) while its session lingers alive for the coordinator to close it out (#707); if that still-alive worker keeps producing output it MUST animate the spinner, not fall through to the static review glyph.

This is the display sibling of the needs-input un-gating: the working spinner was the last rail signal still gated on task status.

The two stale-session concerns the task-status gate previously guarded SHALL be preserved without it:

- A stopped / dead / days-old session MUST NOT spin. A hera binding does NOT end when its agent session exits — bindings end only on task-delete, reparent, detach, or the daemon-startup missing-task sweep — so a dead worker's role stays `Live` with its task row lingering, and liveness ALONE cannot exclude it. The protection is therefore the SESSION-RUNNING signal (the per-tick running set): a dead session is absent from it, so the role is not active. (Gating on liveness alone would spin a dead worker, since a dead session is neither in the running set nor in the idle set.)
- A parked continuously-repainting (fullscreen) agent MUST NOT spin forever. It is protected by the content-aware idle signal: the session-idle set unions raw-byte idle with the content-idle classification (a stable emulated screen with the "working" affordance absent), so a live-but-idle role is not active. The idle set is a subset of the running set, so "running AND not idle" is exactly "running and producing output".

The coordinator status LABEL derived from this predicate SHALL follow the same contract: a stale `working` role-status backed by a live, running, content-active session honestly reads "working" regardless of task status; a live-but-session-idle one reads "live"; a live-but-not-running (dead, binding lingering) one reads "live"; a role with no live binding reads "stopped".

#### Scenario: Live worker in in_review with a running session animates the spinner

- **WHEN** a hera worker's bound task has rolled to `in_review` (its binding still live), its session is running, and it is not idle (actively producing output)
- **THEN** the role is active and the rail renders the animated working spinner, advancing with the frame counter

#### Scenario: Live but session-idle role does not spin

- **WHEN** a role holds a live binding and a running session but the session is in the content-aware idle set (a parked fullscreen agent, content stable)
- **THEN** the role is not active and the rail renders a static glyph, for any bound-task status

#### Scenario: Live but not-running role (dead worker, binding lingering) does not spin

- **WHEN** a role's session has exited (dropped from the running set) but its binding still lingers (`Live` remains true because bindings do not end on session exit), even with a stale `working` status and an `in_review` or `in_progress` task
- **THEN** the role is not active and the rail renders a static glyph

#### Scenario: Live active coordinator in in_review labels "working"

- **WHEN** a coordinator holds a live, running, non-idle binding with a stale `working` role-status and a bound task in `in_review`
- **THEN** the coordinator status label reads "working" (with the terminal task state appended), not "live"

