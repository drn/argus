## 1. Implement

- [x] 1.1 Flip `Preferences.notifyOnIdle`'s default from `true` to `false` in `macos/Sources/Argus/Preferences.swift` (key, property, and Settings toggle stay unchanged)
- [x] 1.2 Confirm no existing test asserts the old `true` default (none found in `macos/Tests/` — `Sources/Argus` has no test harness today; note this as a pre-existing gap, not introduced by this change)

## 2. Verify

- [x] 2.1 `make mac-build` passes
- [x] 2.2 `make mac-test` passes (existing Swift suite, unaffected by this change)

## 3. Archive

- [x] 3.1 `openspec archive fix-mac-idle-notification-default` on this branch before opening the PR
