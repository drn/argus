## Context

The scan runs inside the tick's `QueueUpdateDraw` closure with `a.mu` held. It stalls keystrokes, and it also stalls every background goroutine that takes `a.mu`, such as `spinnerLoop`.

### Why the last attempt failed

`gotchas/ui-threading.md` records two reverted attempts:

- **Moving the scan:** `archivedTaskSet()` reads `a.tasks`, whose `*model.Task` pointers are shared with `TaskListView`'s rows, so a background scan would race with `Draw()` and reconciliation.
- **A time-based cache of DB reads:** served a stale read because nothing told it about a write.

This design shares no task pointers and adds no time-based cache.

### What the code does today

All line numbers were checked at `d44efd3c`.

| Item | Finding |
| --- | --- |
| Scan state | Only `app.go` touches it, and only from the scan functions, `contentIdleSignalOf` and `refreshTasksWithIDs`. `Draw()` reads none of it. |
| Fields the scan owns | `needsInputFP`, `needsInputEscalation`, `needsInputSince`, `needsInputCleared`, `needsInputResume`, `needsInputSettle`, `needsInputLogStat`, `needsInputRawSignals`, `needsInputScanCursor`, `sustainedActiveTicks`, `heraBlockedResume`, `contentIdle`, `needsInputScreen`. |
| Fields it publishes | `needsInputIDs`, `scraperNeedsInputIDs`, `sustainedActiveIDs`, `hadBridgeStates`. |
| `ScreenRenderer` | Not safe for concurrent use. The TUI uses one instance in the scan, `ContentIdle` and `autoClearBlockedHeraRoles`. All three must move together. |
| `lastSessionInput` | Reads `a.runner` with no lock, while `restartDaemon` swaps it. `runner.Get` can make a `SessionStatus` RPC on a cache miss, so this can already block the UI goroutine today. |
| Callers of `refreshTasksWithIDs` | Beyond the tick: `refreshTasksAsync` and `refreshTasksLocal` (17 call sites between them), `refreshTasks`, and the prune path. **Each one runs the full scan on the UI goroutine and advances the tick counters.** |
| Test references | `detectNeedsInputSticky` is referenced 73 times in `app_test.go`. Tests reference `refreshTasks` and `refreshTasksWithIDs` 104 times and read needs-input IDs 24 times. |

### Could the TUI use the daemon's results instead?

**No, not as a drop-in.** The handoff asked for this to be checked, and the results do not match. The daemon's watcher (`internal/api/push.go`) is a real second scan of the same sessions, which confirms the duplicate suspected from the daemon's CPU sample. It differs in ways that matter:

| | TUI scan | Daemon watcher |
| --- | --- | --- |
| Cadence | 1 second | 5 seconds |
| BUG-029 escalation (`EscalateParkedSelection`) | Yes | **No** |
| Sustained-active set for the Hera rail | Yes | **No** |
| Content-idle set for the Hera rail | Yes | **No**, in `computeNeedsInput` |
| Log-unchanged check and batching | Yes | **No** |
| Tail source | On-disk log | Live ring |
| Sees input from all surfaces | Only input through this TUI | **Yes, which is better** |
| Reachable from the local TUI | n/a | **Only over REST.** No socket RPC exposes it, and it needs a daemon deploy. |

Adopting it would lose escalated prompts, slow flags to 5 seconds, and still leave the TUI computing the rail's two sets. Merging the two scans is a real improvement but a separate project. It's recorded below as a follow-up.

## Goals / non-goals

**Goals:**

- No scan work on the UI goroutine in the running TUI, including from non-tick refreshes.
- No shared task pointers, and no data race under `-race`.
- `detectNeedsInputSticky`'s logic unchanged, and still callable synchronously.
- A target of median tick stall below 30ms. The closure work outside the scan is estimated at about 24ms: the closure was 87% of profile CPU and the scan 72%, and that remaining 15 of 87 parts of the 138ms per tick is about 24ms.

**Non-goals (named follow-ups, not part of this work):**

- **One scan instead of two:** make the TUI use the daemon's needs-input results after a parity audit of the differences above. This needs a daemon pprof listener first to justify it.
- **Daemon profiling:** the daemon has no pprof listener, so its CPU could not be broken down.
- **Remaining closure work:** `readHeraRoles`, `readPRStates`, `readManagedTasks` and the rail rebuild stay on the UI goroutine. Any cache needs real change detection, for the reason the earlier attempt failed.
- **TUI memory:** 3.9 GB RSS after 4 days, about 1 GB of heap in `Scrollback.Push`, and 75 goroutines blocked in `Emulator.Read`. Possibly another leak like #959/#963; unconfirmed.

## Decisions

### 1. One scanner goroutine with a single-slot input

`Run` starts one goroutine and sets `needsInputScanAsync`. Each tick builds a snapshot on the UI goroutine at the end of `refreshTasksWithIDs` and does a non-blocking replace of a single-slot channel, so the newest input wins. The scanner takes a snapshot, runs, publishes with `QueueUpdateDraw`, and only then takes the next.

So at most one scan runs at a time, and a slow scan means skipped scans, not a queue. The scanner checks `tickDone` before publishing, because `QueueUpdateDraw` after shutdown would block forever.

A dedicated goroutine is used rather than the tick goroutine, because the tick goroutine also runs the daemon health check, which can block for seconds on RPC.

### 2. The snapshot contains values only

```go
type needsInputScanInput struct {
    idleIDs, runningIDs []string // copies
    archived            map[string]bool
    runner              agent.SessionProvider // captured under a.mu
}
```

`archived` is built from `a.tasks` on the UI goroutine and contains only IDs and booleans. No `*model.Task` crosses. `runner` is captured under `a.mu`, closing the existing race with `restartDaemon`.

### 3. `detectNeedsInputSticky` keeps its body and signature

The body moves to `runNeedsInputScan(input, prevNeedsInput)` with reads of `a.tasks` and `a.runner` replaced by the input. `detectNeedsInputSticky(idle, running, prev)` builds an input from the App's current state and calls it. Tests keep their meaning.

### 4. The scanner owns its own previous result

Sticky carry-forward needs the previous tick's flags. It must use the scanner's own last result, not a value from the snapshot.

This is a real trap. A snapshot built while scan N is running would carry scan N-1's result. If scan N cleared a flag, scan N+1 would then carry the old flag forward and resurrect it. Today's `prevNeedsInput` always equals the last scraper result, because `bridge.Override` with no states returns its input unchanged, so using it directly keeps behaviour the same.

### 5. Only the tick starts a scan

`refreshTasksWithIDs` gains a check. In async mode, a non-tick caller doesn't scan. It applies the last published result and uses the current bridge states. Those calls run on the UI goroutine after task actions, and would otherwise stall keystrokes at just the wrong moment.

This fixes a quirk as a side effect: extra refreshes advance the tick counters today, so a two-tick threshold can currently be reached by two rapid refreshes. Counters will advance once per completed scan.

### 6. Publication is a small UI-goroutine step

The publish closure sets `scraperNeedsInputIDs`, `sustainedActiveIDs` and the content-idle list. It then applies `bridge.Override` using `a.bridgeStates` at that moment and calls the existing setters on the task list and rail, followed by `updateAttentionBar`. The rest of `refreshTasksWithIDs` stays where it is.

This code sits in one helper, used by both publication and non-tick refreshes, so the two can't drift apart.

### 7. Synchronous mode stays for tests

`needsInputScanAsync` is false until `Run` sets it. Without it, `refreshTasksWithIDs` scans inline exactly as today, so 104 test references keep working, and `New()`'s startup refresh still fills the first frame.

**Alternative rejected:** converting those tests to wait on publication. It's a large rewrite for no extra confidence, since the scan logic is the same function either way. The async path gets its own tests.

### 8. Failure handling

The scanner recovers panics, logs them with `[scan]`, keeps the last result, and continues. Today a panic in the closure would crash the TUI. A scan taking over 1 second logs a `[scan] slow` line.

## What could change after the PR 2 measurements

`reduce-needs-input-scan-cost` measures the remaining stall, and Aaron sees the figures before this starts. Each of these outcomes would change the plan:

- **Median and p90 stalls are already well under 16ms:** this change may not be worth its risk, and could be dropped or deferred.
- **The remainder is mostly outside the scan:** moving the scan helps less than expected, and the target above becomes unreachable. The DB reads named under non-goals would need their own change.
- **The scan is cheap except on large fleets:** a smaller design might be enough, such as scanning fewer sessions per tick on the UI goroutine.
- **The timing log shows `stall_ms` is small but `queue_ms` is not:** something other than the tick is blocking the UI goroutine, and needs finding first.

### PR 2 measurements

To be filled in from `reduce-needs-input-scan-cost` before implementation starts.

## Frontend parity

TUI-internal. The web and macOS clients are unaffected, and no REST endpoint, event or field changes.

## Supervisor compatibility

No `ProtocolVersion` bump and no change to either surface digest. Only `internal/tui` changes, which is in neither `SupervisorSpawnPaths` nor `SupervisorStreamPaths`, and no wire format changes.

## Risks / trade-offs

- **[A data race, like the earlier attempt]** → Values-only snapshot, sole ownership of scan state, and a `-race` test that mutates `a.tasks` during a scan.
- **[A stale result is published after a state change]** A task can be deleted or a session restarted mid-scan. → The task list and rail ignore IDs they don't show. The next scan corrects within a tick.
- **[Flags change later than today]** Up to one scan's duration. → Acceptable for a status glyph.
- **[Tick thresholds take longer under load]** Counters advance per completed scan, so a scan over 1 second stretches `NeedsInputEscalationTicks` in wall-clock time. → Bounded, and the `[scan] slow` line makes it visible.
- **[`autoClearBlockedHeraRoles` writes to the DB off the UI goroutine]** → `*db.DB` is safe across goroutines. This write doesn't invalidate the task gate today either, and that doesn't change.
- **[A nil `runningIDs` tick]** Today it resets the scan's maps. → Deliberately preserved, to keep logic unchanged. Worth a later look, but not here.
- **[Deadlock]** → The scanner holds no locks when calling `QueueUpdateDraw`, and the closure takes `a.mu` only briefly.

## Migration plan

Merge and relaunch. To roll back, revert the commit, which restores inline scanning.

## Open questions

Whether to proceed at all, once the PR 2 measurements are in. That's Aaron's decision.
