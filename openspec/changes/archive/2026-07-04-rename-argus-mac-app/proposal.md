# Rename ArgusMac to Argus and add an app icon

## Why

The macOS app's SwiftPM product/target, binary, and `.app` bundle are all
named `ArgusMac` — an internal disambiguation from the early build-out phase
that no longer earns its keep now the app is the only native macOS surface.
The user wants the app to simply be "Argus" everywhere it's user-visible
(Dock, Finder, `open`/`swift run` invocations), and to carry a real app icon
instead of AppKit's generic fallback — the other two frontends (web PWA, TUI
favicon) already use the shield/all-seeing-eye mark for this purpose.

## What Changes

- **Rename** the SwiftPM executable product/target from `ArgusMac` to
  `Argus` (package name `ArgusKit` is unaffected — it's the library, not the
  app), the source directory `macos/Sources/ArgusMac` → `macos/Sources/Argus`,
  the app-entry file/struct `ArgusMacApp` → `ArgusApp`, and the assembled
  bundle `macos/dist/ArgusMac.app` → `macos/dist/Argus.app`. `make mac-run`,
  `make mac-app`, and `scripts/mac-app.sh` are updated accordingly.
- **Add an app icon**: `macos/Sources/Argus/Resources/AppIcon.icns`,
  rasterized once (via `rsvg-convert` + `iconutil`, not part of the build
  chain) from the existing shield/eye mark (`internal/api/static/icon.svg` —
  the same mark already used for the web PWA's `apple-touch-icon.png` /
  `icon-512.png`). The packaged `.app` gets it via `Info.plist`'s
  `CFBundleIconFile`; the bare `swift run Argus` path (no bundle) gets it via
  `NSApplication.shared.applicationIconImage`, loaded from the SwiftPM
  target's bundled resource at launch.
- No REST/daemon-facing behavior changes — this is a rename plus a static
  asset addition, confined to `macos/`, `Makefile`, `scripts/mac-app.sh`, and
  docs (`CLAUDE.md`, `README.md`, `context/knowledge/gotchas/macos-app.md`).
- `ARGUS_MAC_SELECT_TASK` / `ARGUS_MAC_INITIAL_TAB` env var names and the
  `com.drn.argus.mac` bundle identifier / `com.thanx.argusmac` Keychain
  service string are left unchanged — stable external identifiers,
  independent of the display/executable name.

## Impact

- Affected spec: `macos-app` (Purpose section + two Build-and-run scenarios
  updated for the new name; one new requirement added for the app icon).
- Affected code: `macos/Package.swift`, `macos/Sources/Argus/**`,
  `Makefile`, `scripts/mac-app.sh`.
- Affected docs: `CLAUDE.md`, `README.md`,
  `context/knowledge/gotchas/macos-app.md`.
- No Go build/test/CI impact (`make pre-pr` doesn't touch `macos/`).
