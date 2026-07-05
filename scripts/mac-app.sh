#!/usr/bin/env bash
#
# mac-app.sh — assemble macos/dist/Argus.app, a real double-clickable
# bundle, from the release Argus executable.
#
# `swift run`/`swift build` alone produce a bare Mach-O binary: launched
# outside a bundle it starts with AppKit's default `.accessory` activation
# policy (no menu bar, no Dock icon, no focus) — see the AppDelegate workaround
# in ArgusApp.swift. A real .app bundle with an Info.plist fixes that for
# double-click / `open` launches, which is what the follow-up screenshot task
# needs.
#
# Invoked via `make mac-app` (see the Makefile's macOS section). Idempotent:
# safe to re-run, always rebuilds the bundle from scratch.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MACOS_DIR="$REPO_ROOT/macos"
DIST_DIR="$MACOS_DIR/dist"
APP_DIR="$DIST_DIR/Argus.app"
BINARY_NAME="Argus"

ARGUS_SIGN_IDENTITY="${ARGUS_SIGN_IDENTITY:-Argus Code Signing}"
BUNDLE_ID="com.drn.argus.mac"

echo "[mac-app] building release binary..."
# --product Argus: a plain `swift build -c release` also builds the
# ArgusKitTests executable target, which fails in release mode
# (`@testable import ArgusKit` requires a testability-enabled build — see
# [#ModuleNotTestable]). Scoping to the Argus product sidesteps that
# entirely; it doesn't need testability.
(cd "$MACOS_DIR" && swift build -c release --disable-sandbox --product "$BINARY_NAME")

RELEASE_BIN="$MACOS_DIR/.build/release/$BINARY_NAME"
if [ ! -x "$RELEASE_BIN" ]; then
    echo "[mac-app] ERROR: release binary not found at $RELEASE_BIN" >&2
    exit 1
fi

echo "[mac-app] assembling $APP_DIR..."
rm -rf "$APP_DIR"
mkdir -p "$APP_DIR/Contents/MacOS" "$APP_DIR/Contents/Resources"
cp "$RELEASE_BIN" "$APP_DIR/Contents/MacOS/$BINARY_NAME"
cp "$MACOS_DIR/Sources/Argus/Resources/AppIcon.icns" "$APP_DIR/Contents/Resources/AppIcon.icns"

cat > "$APP_DIR/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key>
	<string>$BUNDLE_ID</string>
	<key>CFBundleName</key>
	<string>Argus</string>
	<key>CFBundleDisplayName</key>
	<string>Argus</string>
	<key>CFBundleExecutable</key>
	<string>$BINARY_NAME</string>
	<key>CFBundleIconFile</key>
	<string>AppIcon</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>0.1.0</string>
	<key>LSMinimumSystemVersion</key>
	<string>15.0</string>
	<key>NSHighResolutionCapable</key>
	<true/>
	<key>NSSupportsAutomaticGraphicsSwitching</key>
	<true/>
</dict>
</plist>
PLIST

# Codesign — mirror the install-signed pattern in the Makefile: sign with a
# STABLE identity when the developer has opted in (see install-signed's
# comment for setup), else fall back to an ad-hoc signature so the bundle is
# still launchable everywhere (other devs, CI, Linux-built-but-never-run).
id=$(security find-identity -p codesigning 2>/dev/null | grep -F "$ARGUS_SIGN_IDENTITY" | head -1 | awk '{print $2}' || true)
if [ -n "$id" ]; then
    codesign --force --identifier "$BUNDLE_ID" --sign "$id" "$APP_DIR"
    echo "[mac-app] signed with stable identity '$ARGUS_SIGN_IDENTITY' ($id)"
else
    codesign --force --sign - "$APP_DIR"
    echo "[mac-app] no '$ARGUS_SIGN_IDENTITY' code-signing identity found — ad-hoc signed instead (see install-signed in the Makefile to opt in)"
fi

echo "[mac-app] bundle ready: $APP_DIR"
echo "[mac-app] open it with: open '$APP_DIR'"
