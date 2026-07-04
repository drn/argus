## 1. Pure sanitizer (ArgusKit)

- [x] 1.1 Add `TerminalInput.sanitize(_:)` to `macos/Sources/ArgusKit/TerminalInput.swift` — strips `0x1A` (Ctrl+Z / SUB), preserves all other bytes in order, fast-path identity on clean input
- [x] 1.2 Add `macos/Tests/ArgusKitTests/TerminalInputTests.swift` covering lone Ctrl+Z, embedded Ctrl+Z, repeated Ctrl+Z, clean-input identity, empty input, and only-Ctrl+Z-is-stripped

## 2. Wire the guard into the SwiftTerm delegate (ArgusMac)

- [x] 2.1 In `TerminalCoordinator.send`, filter outbound bytes through `TerminalInput.sanitize` before enqueueing; early-return when the result is empty
- [x] 2.2 Log an `os.Logger` info line whenever a Ctrl+Z byte is dropped

## 3. Docs

- [x] 3.1 Add a gotcha bullet to `context/knowledge/gotchas/macos-app.md` documenting the guard, its placement, and the swallow-not-remap decision
- [x] 3.2 Bump the `macos-app.md` bullet count in `context/knowledge/index.md`

## 4. Verify

- [x] 4.1 `make mac-build` clean
- [x] 4.2 `make mac-test` green (new suite passes)
- [x] 4.3 `make pre-pr` green (Go gate stays green — Swift change does not touch Go)
