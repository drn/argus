#!/usr/bin/env bash
#
# install-reaper.sh — install/uninstall the orphaned-test reaper LaunchAgent.
#
# macOS only. Copies reap-orphaned-tests.sh to a stable location (so the
# LaunchAgent keeps working even if this repo/worktree moves or is deleted),
# renders a LaunchAgent plist, and bootstraps it into the user's launchd domain
# to run on an interval. Re-running re-copies the script and reloads the job.
#
# Usage:
#   ./script/install-reaper.sh [install|uninstall]
#
# Env:
#   REAP_INTERVAL_SECONDS  how often launchd runs the reaper (default 300)
#   REAP_MIN_AGE_MINUTES   passed through to the reaper (default 10)
#
set -euo pipefail

LABEL="com.drn.argus.test-reaper"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
DEST="$HOME/.local/bin/argus-reap-orphaned-tests.sh"
INTERVAL="${REAP_INTERVAL_SECONDS:-300}"
MIN_AGE="${REAP_MIN_AGE_MINUTES:-10}"
LOGDIR="$HOME/.argus/logs"

domain="gui/$(id -u)"

uninstall() {
	if launchctl print "$domain/$LABEL" >/dev/null 2>&1; then
		launchctl bootout "$domain/$LABEL" || true
	fi
	rm -f "$PLIST"
	echo "reaper LaunchAgent uninstalled ($LABEL)"
}

case "${1:-install}" in
uninstall)
	uninstall
	exit 0
	;;
install) ;;
*)
	echo "usage: $0 [install|uninstall]" >&2
	exit 2
	;;
esac

if [ "$(uname)" != "Darwin" ]; then
	echo "reaper LaunchAgent is macOS-only; nothing to do on $(uname)" >&2
	exit 0
fi

SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SRC="$SRC_DIR/reap-orphaned-tests.sh"
[ -f "$SRC" ] || {
	echo "reaper script not found at $SRC" >&2
	exit 1
}

mkdir -p "$(dirname "$DEST")" "$LOGDIR" "$(dirname "$PLIST")"
cp "$SRC" "$DEST"
chmod +x "$DEST"

cat >"$PLIST" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>$LABEL</string>
	<key>ProgramArguments</key>
	<array>
		<string>/bin/bash</string>
		<string>$DEST</string>
	</array>
	<key>StartInterval</key>
	<integer>$INTERVAL</integer>
	<key>RunAtLoad</key>
	<true/>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>/usr/bin:/bin:/usr/sbin:/sbin</string>
		<key>REAP_MIN_AGE_MINUTES</key>
		<string>$MIN_AGE</string>
	</dict>
	<key>StandardOutPath</key>
	<string>$LOGDIR/reap-orphaned-tests.out.log</string>
	<key>StandardErrorPath</key>
	<string>$LOGDIR/reap-orphaned-tests.err.log</string>
</dict>
</plist>
PLIST

if launchctl print "$domain/$LABEL" >/dev/null 2>&1; then
	launchctl bootout "$domain/$LABEL" || true
fi
launchctl bootstrap "$domain" "$PLIST"

echo "reaper LaunchAgent installed: $LABEL"
echo "  runs every ${INTERVAL}s, reaps *.test orphans older than ${MIN_AGE}m"
echo "  script:  $DEST"
echo "  plist:   $PLIST"
echo "  logs:    $LOGDIR/reap-orphaned-tests*.log"
