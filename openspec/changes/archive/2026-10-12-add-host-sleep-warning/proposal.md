## Why

The remote API / web app is served by the daemon on the host Mac. When macOS idle-sleeps (the default `sleep 1` on AC), the web app silently becomes unreachable until the Mac wakes; a ~1 hour outage was traced to exactly this (dark-wake cycling, every gap logged by the `hostwatch` watchdog). The README documents the remedy, but nothing tells the user their setup is exposed. Users who don't enable the remote API are unaffected and must not be nagged.

## What Changes

- New `internal/hostsleep` package: diagnoses the host's sleep posture (`pmset -g custom` AC `sleep`, plus whether a `caffeinate` assertion is bound to the daemon PID) into `not_applicable | safe | held | at_risk | unknown`. Only `at_risk` warns.
- **Applies only when the remote API is enabled** and the host is macOS; otherwise `not_applicable` and silent.
- `argus doctor` prints a "Host sleep" block (advisory, never affects the exit code), with the permanent and temporary fixes.
- `GET /api/system-metrics` gains a `host_sleep` object; the web Settings tab and the macOS System tab show a warning banner when `at_risk`.
- TUI Settings > Remote API shows a warning (and a rail-row suffix) when enabled and `at_risk`.

## Non-Goals

- Argus does not change power settings or hold a power assertion itself (a daemon-held assertion is a named follow-up).
- TUI `--remote` mode does not show the warning: the check would run on the client machine, not the host. The web/macOS clients get it over REST.
- `held` is not a warning: a daemon-bound `caffeinate` does not survive a daemon restart, so doctor suggests the permanent fix but Settings/web/macOS stay quiet.
