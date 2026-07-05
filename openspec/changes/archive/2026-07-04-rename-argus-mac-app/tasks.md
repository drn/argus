# Tasks

- [x] Generate `AppIcon.icns` from `internal/api/static/icon.svg` (rsvg-convert
      + iconutil) and commit it at `macos/Sources/Argus/Resources/AppIcon.icns`
- [x] Rename `macos/Sources/ArgusMac` → `macos/Sources/Argus`,
      `ArgusMacApp.swift` → `ArgusApp.swift`, struct `ArgusMacApp` → `ArgusApp`
- [x] `macos/Package.swift`: rename product/target `ArgusMac` → `Argus`, add
      `resources: [.copy("Resources/AppIcon.icns")]`
- [x] `AppDelegate.applicationDidFinishLaunching`: set
      `NSApplication.shared.applicationIconImage` from `Bundle.module`
- [x] Update comment-only `ArgusMac` mentions in `AppState.swift`,
      `Keychain.swift`, `NotificationManager.swift`, `DiffParser.swift`
- [x] `Makefile`: `mac-run` target + section comments
- [x] `scripts/mac-app.sh`: `BINARY_NAME`/`APP_DIR` rename, copy
      `AppIcon.icns` into `Contents/Resources`, add `CFBundleIconFile` to the
      generated `Info.plist`
- [x] `.gitignore` comment
- [x] `CLAUDE.md`, `README.md`, `context/knowledge/gotchas/macos-app.md`
- [x] Verify: `make mac-build`, `make mac-test`, `make mac-app`, `make mac-run`
- [x] Archive this change into `openspec/specs/macos-app/spec.md` and move the
      folder to `openspec/changes/archive/2026-07-04-rename-argus-mac-app/`
