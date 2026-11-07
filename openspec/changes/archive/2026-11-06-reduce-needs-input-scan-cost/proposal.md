## Why

Once per second, the TUI's needs-input scan stalls the UI goroutine that handles keystrokes. The measured median was 143ms per tick, with 72% of the TUI's CPU in `detectNeedsInputSticky`.

Much of that work is redundant. The same tail is read and emulated several times per session, and the idle pass re-reads every idle session even when its log hasn't changed. Removing that waste is cheap and safe, and the measurement afterwards decides how much `move-needs-input-scan-off-ui-thread` still needs to do.

This is the second of three changes and follows `repaint-panes-on-output`.

## What changes

- The idle pass reuses its last reading while a session's log is unchanged, as the other passes already do.
- Each session's tail is read once per scan, and rendered and stripped of ANSI at most once per scan, shared across all detectors.
- Archived sessions are skipped by the idle pass. Their result was already discarded.
- The log-unchanged check starts working for fleets above the batch size of 24. It is currently defeated there by a bookkeeping bug.
- The blocked-role auto-clear reuses the reading the scan already made, instead of rendering again.
- Before merging, the stall and latency figures are measured and reported to Aaron, because they decide how much of the next change is still worthwhile.

Detection results do not change.

## Capabilities

### New capabilities

None.

### Modified capabilities

- `idle-detection`: adds requirements bounding how much work a scan does per session. No detection outcome changes.

## Impact

- **Code:** `internal/tui/app.go` (`detectNeedsInput`, `detectNeedsInputSticky`, `autoClearBlockedHeraRoles`) and `internal/agent/needsinput.go` (a shared per-tail view, with the existing exported detectors delegating to it).
- **Daemon:** `computeNeedsInput` keeps calling the same exported detector functions, with the same results.
- **Deployment:** a TUI relaunch only.
- **Frontend parity:** TUI-internal. The web and macOS clients are unaffected, and the REST surface does not change.
