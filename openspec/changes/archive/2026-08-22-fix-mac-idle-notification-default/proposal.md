## Why

With many concurrent argus tasks open, the macOS companion app floods the user with native "is idle" notifications — every `session.idle` event across the whole fleet posts a banner, with no dedupe or coalescing (unlike the needs-input path, which dedupes via `NotificationManager`'s `pendingNeedsInput` set). The user only wants to be notified for the needs-input ("?") signal, not idle. Idle notifications stay useful for some workflows, so this flips the default to opt-in rather than removing the feature.

## What Changes

- `Preferences.notifyOnIdle` (`macos/Sources/Argus/Preferences.swift`) defaults to `false` instead of `true` when the `argusmac.notifyIdle` UserDefaults key has never been written. The property, its key, and the existing Settings toggle are unchanged — this only flips the out-of-the-box default from opt-out to opt-in.
- `notifyOnNeedsInput` and `showMenuBarExtra` are unaffected; they stay default-on.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `macos-app`: the "Events integration" requirement's idle-notification behavior is now opt-in (default off) rather than unconditional-when-enabled-and-unfocused with a default-true preference.

## Impact

- `macos/Sources/Argus/Preferences.swift` — `notifyOnIdle` getter.
- `openspec/specs/macos-app/spec.md` — "Events integration" requirement gains an explicit default-notification-preference scenario split between needs-input (default on) and idle (default off).
