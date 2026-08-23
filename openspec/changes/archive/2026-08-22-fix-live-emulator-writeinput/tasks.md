## 1. Fix the query-response delivery path

- [x] 1.1 Add `WriteInputSystem(p []byte) (int, error)` to `agentview.TerminalAdapter` (`internal/app/agentview/terminal.go`).
- [x] 1.2 `TerminalPane.forwardEmulatorResponse` (`internal/tui/terminal/terminalpane.go`) calls `sess.WriteInputSystem(p)` instead of `sess.WriteInput(p)`.
- [x] 1.3 Update fakes that implement `agentview.TerminalAdapter` to add `WriteInputSystem` so they keep compiling against the widened interface: `mockAdapter` and `raceAdapter` (`internal/tui/terminal/terminalpane_test.go`), `recAdapter` (`internal/tui/terminal/forward_wheel_test.go`), `altScreenAdapter` (`internal/tui/bug031_test.go`), `fakeSession` (`internal/tui/hera/panes_test.go`).

## 2. Tests

- [x] 2.1 Update `TestTerminalPane_ForwardEmulatorResponse_ForwardsToSession` and `TestTerminalPane_ForwardEmulatorResponse_DropsStaleOwner` to assert delivery went through `WriteInputSystem` (not `WriteInput`) — the exact distinction this bug turned on.
- [x] 2.2 Add a regression test proving the needs-input flag is NOT cleared by a query-response forwarded via `WriteInputSystem`: `Session.LastUserInput()` (the `NeedsInputClear` clear-on-input source) must be unchanged after `WriteInputSystem`, while `Session.LastInput()` (work-cycle signal) advances — mirroring the existing `WriteInput` vs `WriteInputSystem` test pattern in `internal/agent`.

## 3. Docs

- [x] 3.1 Add a gotcha bullet to `context/knowledge/gotchas/pty-terminal.md` documenting the focus-gated needs-input bounce and its fix (live-emulator query-response forwarding must use `WriteInputSystem`, not `WriteInput`).
