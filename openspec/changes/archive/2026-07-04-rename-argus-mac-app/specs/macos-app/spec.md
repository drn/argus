# macos-app Specification

## MODIFIED Requirements

### Requirement: Build & run without Xcode

The system SHALL build and test as a pure SwiftPM package (`swift build`,
`swift test`, wired to `make mac-build` / `make mac-test`), SHALL run
directly via `make mac-run`, and SHALL assemble a runnable, ad-hoc-codesigned
`.app` bundle via `make mac-app` — with no `.xcodeproj` or `.xcworkspace`
checked into the repo.

#### Scenario: Make targets build without Xcode project files

- **WHEN** `make mac-build` runs in an environment with only the Swift
  toolchain (no Xcode project generation step)
- **THEN** the build succeeds via `swift build --package-path macos` and
  produces the `Argus` executable

#### Scenario: mac-app produces a launchable bundle

- **WHEN** `make mac-app` runs after a successful `mac-build`
- **THEN** it assembles `Argus.app` with an ad-hoc code signature and the
  bundle launches from Finder or `open` without a Gatekeeper "unidentified
  developer" prompt requiring override

## ADDED Requirements

### Requirement: App branding

The system SHALL present the Argus shield/eye mark as the app's icon in the
Dock, Finder, and Cmd+Tab switcher, both for the packaged `.app` bundle (via
`Info.plist`'s `CFBundleIconFile`) and for the bare `swift run` executable
(via `NSApplication.shared.applicationIconImage`, set at launch from a
resource bundled with the SwiftPM target) — no user-visible surface SHALL
fall back to AppKit's generic executable icon.

#### Scenario: Packaged bundle carries the icon

- **WHEN** `make mac-app` assembles `Argus.app`
- **THEN** `Contents/Resources/AppIcon.icns` exists and `Info.plist`
  declares it via `CFBundleIconFile`, so Finder and the Dock show the Argus
  mark without launching the app

#### Scenario: Bare executable also shows the icon

- **WHEN** the app is launched via `swift run Argus` (no `.app` bundle)
- **THEN** the Dock icon shows the Argus mark rather than AppKit's generic
  fallback, because `AppDelegate.applicationDidFinishLaunching` sets
  `NSApplication.shared.applicationIconImage` from the bundled resource
