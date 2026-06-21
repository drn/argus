#!/usr/bin/env bash
#
# reap-orphaned-tests.sh — kill orphaned Go *.test binaries pegging the CPU.
#
# A Go per-package test binary normally runs as a child of `go test`. If that
# parent dies (Ctrl+C, crash, or — the case this guards — the laptop slept past
# the test's -test.timeout so the runtime-monotonic watchdog never accumulated
# enough AWAKE time to fire), the test binary is reparented to PID 1 and can
# spin a CPU forever. One did: a tui.test binary ran orphaned at ~95% CPU for
# 2+ days.
#
# This reaper finds such orphans and kills them. It is safe by construction —
# a live `go test` run is NEVER touched, because its test binaries are children
# of `go test`, never of PID 1:
#
#   1. PPID == 1            the discriminator: in-flight tests are never PID 1.
#   2. Go-test signature    argv must name a *.test binary AND carry a `-test.`
#                           flag, so unrelated processes are ignored.
#   3. Age threshold        older than REAP_MIN_AGE_MINUTES (default 10), which
#                           clears the brief reparent window during a normal
#                           `go test` shutdown.
#
# Env:
#   REAP_MIN_AGE_MINUTES   minimum process age before reaping (default 10)
#   REAP_LOG_FILE          log destination (default ~/.argus/logs/reap-orphaned-tests.log)
#
# Flags:
#   --dry-run              list candidates; kill nothing
#   -h | --help            usage
#
set -euo pipefail

MIN_AGE_MINUTES="${REAP_MIN_AGE_MINUTES:-10}"
LOG_FILE="${REAP_LOG_FILE:-$HOME/.argus/logs/reap-orphaned-tests.log}"
DRY_RUN=0

for arg in "$@"; do
	case "$arg" in
	--dry-run) DRY_RUN=1 ;;
	-h | --help)
		echo "usage: $0 [--dry-run]"
		exit 0
		;;
	*)
		echo "unknown argument: $arg" >&2
		exit 2
		;;
	esac
done

mkdir -p "$(dirname "$LOG_FILE")"

log() {
	printf '%s reaper: %s\n' "$(date '+%Y-%m-%dT%H:%M:%S%z')" "$1" >>"$LOG_FILE"
	printf '%s\n' "$1"
}

# Enumerate orphaned Go-test binaries. ps argv order: pid ppid etime args...
# awk converts etime ([[dd-]hh:]mm:ss) to seconds and applies all three gates.
candidates="$(
	ps -axo pid=,ppid=,etime=,args= | awk -v minmin="$MIN_AGE_MINUTES" '
		function tosecs(e,   d, n, a, parts, h, m, s) {
			d = 0
			n = index(e, "-")
			if (n > 0) { d = substr(e, 1, n - 1) + 0; e = substr(e, n + 1) }
			parts = split(e, a, ":")
			if (parts == 3)      { h = a[1]+0; m = a[2]+0; s = a[3]+0 }
			else if (parts == 2) { h = 0;      m = a[1]+0; s = a[2]+0 }
			else                 { h = 0;      m = 0;      s = a[1]+0 }
			return ((d * 24 + h) * 60 + m) * 60 + s
		}
		{
			pid = $1; ppid = $2; etime = $3
			args = ""
			for (i = 4; i <= NF; i++) args = args (i > 4 ? " " : "") $i
			if (ppid != 1) next                 # not orphaned
			if (args !~ /\.test( |$)/) next     # not a Go test binary
			if (args !~ /-test\./) next         # missing the go-test flag signature
			secs = tosecs(etime)
			if (secs < minmin * 60) next        # within normal shutdown window
			print pid "\t" secs "\t" args
		}
	'
)"

if [ -z "$candidates" ]; then
	log "no orphaned test binaries (min_age=${MIN_AGE_MINUTES}m)"
	exit 0
fi

printf '%s\n' "$candidates" | while IFS=$'\t' read -r pid age args; do
	[ -n "$pid" ] || continue
	if [ "$DRY_RUN" -eq 1 ]; then
		log "DRY-RUN candidate pid=$pid age=${age}s cmd=$args"
		continue
	fi
	log "killing orphaned test binary pid=$pid age=${age}s cmd=$args"
	kill -TERM "$pid" 2>/dev/null || true
	sleep 2
	if kill -0 "$pid" 2>/dev/null; then
		log "pid=$pid survived SIGTERM; sending SIGKILL"
		kill -KILL "$pid" 2>/dev/null || true
	fi
done
