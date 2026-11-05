## Why

Even after `reduce-needs-input-scan-cost` removes redundant work, the needs-input scan still reads files and runs a terminal emulator on the UI goroutine. It can only be as fast as disk and fleet size allow, and any time it takes is time keystrokes wait.

An earlier attempt to move it off the UI goroutine was reverted for a real data race on shared task pointers. This change moves it safely.

This is the third of three changes. **It is the current best design, and depends on the measurements from `reduce-needs-input-scan-cost`.** `design.md` names what could change.

## What changes

- The scan runs on one background goroutine, from an immutable snapshot taken on the UI goroutine.
- Results are published back through `QueueUpdateDraw`.
- Only the tick starts a scan. Other refreshes, which today each run a full scan on the UI goroutine, use the last published result.
- The scan's persistent state is owned by the scan alone while it runs. No `*model.Task` pointer crosses to the background goroutine.
- `detectNeedsInputSticky` stays callable synchronously with its logic unchanged. Tests, and an App with no running event loop, keep today's inline path.

Detection logic does not change. A needs-input flag can appear or clear up to one scan's duration later than today.

## Capabilities

### New capabilities

None.

### Modified capabilities

- `idle-detection`: the needs-input scan runs off the UI goroutine, with snapshot, publication and single-flight rules.

## Impact

- **Code:** `internal/tui/app.go` (`onTick`, `refreshTasksWithIDs`, `detectNeedsInputSticky`, `lastSessionInput`, `archivedTaskSet`, `autoClearBlockedHeraRoles`, `Run`).
- **Deployment:** a TUI relaunch only.
- **Frontend parity:** TUI-internal. The web and macOS clients are unaffected, and the REST surface does not change.
