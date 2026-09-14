## 1. Default terminal pane (`internal/tui/terminal/terminalpane.go`)

- [x] 1.1 Split the `MouseHandler` click case into `MouseLeftDown` (decision
  point: focus-vs-forward) and `MouseLeftUp` (delivers the matching half of a
  forwarded click); ignore `MouseLeftClick` for click handling so the
  press/release pair isn't duplicated.
- [x] 1.2 Add a pane-scoped flag recording whether the in-flight click is
  being forwarded, set on `MouseLeftDown`, consumed on `MouseLeftUp`.
- [x] 1.3 On `MouseLeftDown`: if unfocused, `setFocus(tp)` + `OnClick()` as
  today (do not forward). If focused with a live session, write an SGR left
  press and arm the flag (do not call `setFocus`/`OnClick`). If focused with
  no live session, call `OnClick()` as today (do not forward).
- [x] 1.4 On `MouseLeftUp`: if the flag is armed, write the matching SGR
  release and clear the flag; otherwise no-op.
- [x] 1.5 Do not touch `forwardWheel`/`agentOwnsWheel`.

## 2. Plugin-view terminal surface (`internal/tui/terminalpane/terminalpane.go`)

- [x] 2.1 Add a `MouseLeftDown`/`MouseLeftUp` case to `MouseHandler` mirroring
  the existing wheel case — unconditional forwarding (`send`), SGR press on
  Down, SGR release on Up, no focus branch needed (the surface is always the
  focused widget while its page is shown).

## 3. Tests

- [x] 3.1 Default pane: table-driven test covering unfocused→focuses+no
  forward, focused+live session→forwards press+release+no `OnClick`,
  focused+no session→falls back to `OnClick`+no forward. Mirror
  `forward_wheel_test.go`'s `recAdapter` mocking approach.
- [x] 3.2 Plugin-view pane: extend/adjust the existing
  `TestTerminalPane_MouseHandlerIgnoresNonWheelActions` case list (left
  down/click will now be consumed) and add a forwarding test mirroring
  `TestTerminalPane_MouseHandlerForwardsWheel`'s table shape.
- [x] 3.3 `make test-cover` on both touched packages; target ≥95%.

## 4. Docs

- [x] 4.1 Add a gotcha bullet to `context/knowledge/gotchas/pty-terminal.md`
  documenting that clicks were never forwarded at all (unlike wheel, since
  BUG-026) and that click passthrough is gated on focus state, not
  alt-screen.
