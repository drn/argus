# mobile-pwa (delta)

## ADDED Requirements

### Requirement: Terminal Ctrl+Z guard prevents session orphaning

The SPA terminal SHALL intercept a bare `Ctrl+Z` at the xterm.js key-event layer (before any byte is emitted to the input path) so the `0x1a` (SIGTSTP) byte can never reach the agent's PTY through the keyboard. A bare `Ctrl+Z` is `Ctrl` held with neither `Meta` nor `Alt` and the `z` key (`Shift` is tolerated because `Ctrl+Shift+Z` yields the same byte). The guard SHALL swallow the key (no PTY byte, no default browser action) rather than remap it, and SHALL surface a brief explanatory notice. `Cmd+Z` (the browser/textarea undo, `metaKey`) SHALL NOT be intercepted.

Rationale: Claude Code's CLI backgrounds a session that receives `SIGTSTP` into its own supervisor, reparenting it out of argus's process tree permanently and invisibly — orphaning the worker. The TUI already guards this on every surface; this is the parity guard for the web/PWA terminal.

#### Scenario: Ctrl+Z is swallowed, not forwarded to the PTY

- **WHEN** the terminal has keyboard focus and the user presses `Ctrl+Z`
- **THEN** no `0x1a` byte is written to the task's input endpoint and the browser's default action for the key is suppressed

#### Scenario: Ctrl+Z surfaces an explanatory notice

- **WHEN** the user presses `Ctrl+Z` with the terminal focused
- **THEN** a brief toast explains that the key is disabled because it would background the agent

#### Scenario: Normal keystrokes still forward

- **WHEN** the terminal has keyboard focus and the user types a printable key
- **THEN** the corresponding byte is forwarded to the task's input endpoint as before

#### Scenario: Cmd+Z is not intercepted

- **WHEN** the user presses `Cmd+Z` (metaKey) with the terminal focused
- **THEN** the guard does not intercept the key (browser/textarea undo behavior is unaffected)
