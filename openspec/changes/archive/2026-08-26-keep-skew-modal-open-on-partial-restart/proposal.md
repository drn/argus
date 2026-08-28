# Keep the skew modal open when only one of two stale processes is restarted

## Why

When both the daemon and the session-supervisor are stale, the startup skew modal offers three buttons: "Restart daemon", "Restart supervisor", and "Skip". Choosing either restart action unconditionally dismisses the whole modal — so restarting the daemon leaves the still-stale supervisor unaddressed for the rest of the session (it is only re-offered on the next full TUI launch). The modal should only auto-dismiss when a single restart action was available to begin with.

## What Changes

- Choosing "Restart daemon" while the supervisor is also stale removes only the daemon button and leaves the modal open, now offering "Restart supervisor" / "Skip".
- Choosing "Restart supervisor" continues to dismiss the whole modal in one step: restarting the supervisor also bounces the daemon (see the existing supervisor-restart double-confirm behavior), so it resolves any pending daemon restart too.
- The modal still auto-dismisses immediately whenever only one restart action (or none) is offered — the existing daemon-only and supervisor-only behaviors are unchanged.

## Impact

- Affected spec: `binary-coherence` (Startup binary-skew detection and prompt requirement)
- Affected code: `internal/tui/modal/restartdaemon.go`, `internal/tui/app.go` (`handleRestartDaemonKey`)
