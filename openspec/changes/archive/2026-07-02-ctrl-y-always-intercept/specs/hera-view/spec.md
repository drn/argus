## MODIFIED Requirements

### Requirement: Copy agent-staged clipboard from the focused pane (ctrl+y) (area 6)

The system SHALL bind `ctrl+y`, while a TERMINAL pane is focused (the coordinator
pane, or a worker/leaf agent pane in terminal mode — NOT the rail and NOT the
coordinator details/tree region), to copy the agent-staged clipboard payload for
THAT pane's bound argus task to the OS clipboard. Because the Hera view shows
several tasks at once, the payload SHALL be resolved from the FOCUSED pane's task
(`FocusedTerminalTaskID`), never a single global active task.

The interception SHALL be unconditional whenever a terminal pane is focused:
`ctrl+y` SHALL always be stolen from the PTY, regardless of whether a payload
is currently staged for the focused pane's task — it SHALL NEVER fall through
to the pane's PTY. When a payload is staged, the system SHALL clear the
daemon-side slot and flash a "Copied" notice, reusing the existing
`clipboardAccessor` (`ClipboardGet`/`ClipboardClear`) and `copyToClipboard` — no
second clipboard path is introduced. When the runner is not daemon-backed
(in-process fallback) or nothing is staged, `ctrl+y` SHALL flash a notice
indicating there is nothing to copy instead of copying, and SHALL still consume
the key. In remote mode the page is inert, so `ctrl+y` does nothing.

The per-tick staged-ness hint state (`clipReady`) no longer gates the
interception — it drives only the discoverability affordance described below.

When the focused terminal pane's task has a staged payload, the system SHALL
surface a discoverability affordance by appending a `(ctrl+y copy)` marker to
that pane's border title (the Hera-view analogue of the main agent view's header
hint), refreshed each tick for the single focused pane's task. The affordance
SHALL appear on at most the focused terminal pane and SHALL disappear when focus
leaves a terminal pane or nothing is staged.

Derived from: `internal/tui/hera/page.go` (`InputHandler` `ctrl+y` trap,
`OnCopyClipboard`, `clipReady`/`SetClipboardHint`, `Draw` border-title hint),
`internal/tui/hera/panes.go` (`FocusedTerminalTaskID`), `internal/tui/clipboard.go`
(`copyStagedClipboardForHeraPane`, `flashNotice`), `internal/tui/app.go`
(`OnCopyClipboard` wiring + `refreshHeraClipboardHint` tick),
`internal/tui/modal/help.go:70` (help overlay Hera section).

#### Scenario: Copy a staged payload from a focused worker pane

- **WHEN** a worker terminal pane is focused, a payload is staged for that pane's task, and the user presses `ctrl+y`
- **THEN** the staged payload is written to the OS clipboard, the daemon-side slot is cleared, a "Copied" notice flashes, and the key is consumed (not forwarded to the PTY)

#### Scenario: Copy is scoped to the focused pane's task

- **WHEN** the coordinator pane is focused and a payload is staged for the coordinator's task
- **THEN** `ctrl+y` copies the COORDINATOR task's payload (resolved from the focused pane), not any worker pane's payload

#### Scenario: ctrl+y is intercepted with a notice when nothing is staged

- **WHEN** a terminal pane is focused but no payload is staged for its task and the user presses `ctrl+y`
- **THEN** no copy occurs, a "nothing to copy" notice flashes, and the keystroke is consumed rather than forwarded to the pane's PTY

#### Scenario: ctrl+y on the rail or coordinator details is an inert no-op

- **WHEN** the rail or the coordinator details/tree region is focused and the user presses `ctrl+y`
- **THEN** nothing is copied (there is no terminal pane and no bound task to copy from)

#### Scenario: The focused pane advertises a staged payload

- **WHEN** the focused terminal pane's task has a staged payload
- **THEN** that pane's border title shows a `(ctrl+y copy)` affordance, which clears when focus leaves the pane or the payload is consumed/expires

#### Scenario: Help overlay lists the ctrl+y copy key

- **WHEN** the help overlay is opened
- **THEN** its "Hera View (rail)" section lists `ctrl+y` for copying staged text
