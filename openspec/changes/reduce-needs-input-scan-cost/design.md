## Context

The needs-input scan runs inside the tick's `QueueUpdateDraw` closure, so everything it does blocks keystrokes. From the 2026-11-04 profile, taken with 23 tasks in progress under high load:

- `detectNeedsInputSticky` was 72% of TUI CPU, and the `onTick` closure 87%.
- `detectNeedsInput` was 27%.
- `ScreenRenderer.render` was 48%, `memclrNoHeapPointers` 40% (allocation churn), and `sanitize.StripANSI` 19%. These overlap, because they are nested.

### What the code does today

All line numbers were checked at `d44efd3c`.

| Handoff claim | Finding |
| --- | --- |
| Each session's tail renders up to four times | **Up to five.** `DetectNeedsInputScreen` is called twice: by the idle pass and the settlement pass. Each is a separate render on a raw miss. |
| `detectNeedsInput` re-reads and re-emulates every ID, with no log-changed check | Confirmed. It also does its own tail read, separate from the `getTail` memo the other passes share. |
| `scraperIdleIDs` starts as `idleIDs` (`app.go:3509`); confirm how it is filtered | **Not filtered at all.** It is passed straight through `app.go:3631`. `bridge.Override` applies only to the separate `idleIDs` variable. So the idle pass has no archive filter, no batching and no log check. |
| `logUnchanged` must not record a stat on a tick where the log was not read | Confirmed, but this exposes a bug (see below). |
| Reuse one render per tick "if that is safe" | **Safe.** See decision 2. |

Work per session, per scan, today:

| Session state | Tail reads | Renders (most) |
| --- | --- | --- |
| Idle, log unchanged | 1 | 1, from the idle pass, which has no check |
| Idle, log changed | 2 | 5 |
| Running, not idle, log changed | 1 | 3 |
| Running, not idle, log unchanged | 0 | 0 |
| Archived, running, not raw-idle | 1 | 2, every tick, through `ContentIdle`'s fallback |
| Archived, idle | 1 | 1, whose result `NeedsInputClear` then discards |

Each detector tries a raw regex first and renders only on a miss, except `ContentIdleFingerprint`, which always renders. The figures above are worst cases, but a session with no prompt showing is a miss on all of them, so they are the common case.

### Two findings beyond the handoff

**The log-unchanged check does nothing above 24 sessions.** `logUnchanged` writes to `newLogStat`, and `a.needsInputLogStat = newLogStat` replaces the whole map at the end of the scan. A session out of rotation gets no entry, so its previous stat is lost. On its next due tick there is nothing to compare, and it is always re-read.

The comment on `reuseCached` describes the intended behaviour correctly ("compare fresh-vs-last-actually-read"), but the code doesn't do that. The measured fleet was 23, just under the threshold, so this wasn't in the profile. A fleet over 24 pays more than the profile showed. Only wasted work results, not wrong answers.

**`autoClearBlockedHeraRoles` renders again.** It calls `ContentIdleFingerprint` for each blocked role, immediately after the scan has computed the same value.

## Goals / non-goals

**Goals:**

- Remove redundant reads, renders and ANSI stripping from the scan.
- Keep every detection result identical.
- Measure, and report the stall numbers to Aaron before `move-needs-input-scan-off-ui-thread` starts.

**Non-goals (named follow-ups, not part of this work):**

- **Moving the scan off the UI goroutine:** that is `move-needs-input-scan-off-ui-thread`.
- **The daemon's scan:** `computeNeedsInput` has the same redundancy, but runs off the UI on a 5-second tick. The daemon has no pprof listener, so its CPU can't be broken down.
- **Archived sessions in `ContentIdle`:** classifying them less often would change results, so only the render sharing applies here.
- **TUI memory:** 3.9 GB RSS after 4 days, about 1 GB of heap in `Scrollback.Push`, and 75 goroutines blocked in `Emulator.Read`. Possibly another leak like #959/#963; unconfirmed.

## Decisions

### 1. The idle pass uses the same dirty check as everything else

The idle pass moves after the `reuseCached` closures are defined, and uses them. For a non-archived idle session:

- If `reuseCached(id)` and last tick's `awaitingComputed` both hold, it replays `awaitingNow`.
- Otherwise it reads through the shared `getTail` and computes.

The settlement pass then uses the idle pass's value for the same session, rather than computing a fifth render.

This must call `needsInputScanBatch` exactly once per scan, since it advances the rotation cursor. It must also keep idle results first in `fresh`, because later steps append to that slice.

An ID in `idleIDs` that isn't in `activeRunningIDs` has no previous raw signal, so it's read fresh. That covers the tick where `runningIDs` is nil. A session newly idle has `awaitingComputed` false, so it's read fresh too. Batching therefore delays nothing here in practice: new output means the session wasn't idle.

### 2. A per-tail view shares the render and the strip

A new `agent.TailView` holds one tail, its PTY size and a `ScreenRenderer`. It lazily computes and memoizes the windowed ANSI-stripped text, the whole-buffer stripped text and the rendered screen.

The four detectors become methods on it, and the existing exported functions become one-line wrappers that build a view. The daemon and existing tests keep calling the same functions with the same results. The scan keeps one view per session per scan, replacing the `getTail` map.

The raw-first detectors stay lazy, so they never add a render on a raw hit. `ContentIdleFingerprint` always renders today and keeps doing so, which means a running session with a changed log costs exactly one render after this change, rather than none. Removing that render would need a different way to detect the working affordance, and is out of scope.

Sharing is safe because:

- `render` resets the emulator with RIS before every call, so it has no state across calls. `TestScreenRenderer_RepeatedIdenticalRenderIsIdempotent` already proves this.
- The result is an immutable string.
- `InputDraft` is the one caller that reads emulator cells after rendering. It's only used by `internal/notify` with its own renderer, and doesn't go through a view.

Two stripped forms are needed because `DetectNeedsInput` and friends strip the last `needsInputTailWindow` bytes, while `ContentFingerprint` strips the whole buffer. `SubstantiveTail` can return more than the window, so they can differ. Merging them would change fingerprints.

**Alternative rejected:** a one-entry memo inside `ScreenRenderer`. The passes loop over all sessions in turn, so a one-entry memo would miss every time.

### 3. Skip archived sessions in the idle pass

`NeedsInputClear` drops every archived candidate unconditionally, so reading them can't affect the output. This follows the same reasoning as the existing archive pre-filter.

### 4. Carry the old stat forward for out-of-rotation sessions

When a session isn't due, its previous stat is copied into `newLogStat`. This doesn't break the rule about not recording a stat without reading the log: the old stat is what was last actually read, and no new stat is recorded. A change during the gap is still caught on the next due tick.

### 5. Blocked-role auto-clear reuses the scan's reading

`autoClearBlockedHeraRoles` runs straight after the scan. For a task with a `needsInputRawSignals` entry where `hasTail` is true, it uses that `working` value. Otherwise (archived, or not in the scan) it computes as today.

## Measurement

Before merging, the worker builds a binary from this branch outside the repository and hands the path to the coordinator. Aaron relaunches with it and picks Skip on the skew modal, as in "Before capture" in `repaint-panes-on-output/design.md`. Nothing is deployed. The worker then repeats "Capture window" and "Reading results back" from that document, using the same `argus-latency` commands.

Measuring before merge keeps the results in this change's `measurements.md`, which is archived with it. The figures are also copied to "PR 2 measurements" in `move-needs-input-scan-off-ui-thread/design.md`.

**The worker reports the stall numbers to Aaron through the coordinator before sending `status=done`,** because that status starts `move-needs-input-scan-off-ui-thread`. They decide how much of that change is still worthwhile.

A rough, unmeasured estimate: the idle pass was 27% of CPU, and rendering and stripping dominate the rest, so the median stall could fall from 143ms to around 50 to 70ms. The remaining closure work outside the scan is about 24ms.

## Frontend parity

TUI-internal. The web and macOS clients are unaffected, and no REST endpoint, event or field changes.

## Supervisor compatibility

No `ProtocolVersion` bump and no change to either surface digest. `internal/agent/needsinput.go` and `internal/tui` are not in `SupervisorSpawnPaths` or `SupervisorStreamPaths`, and no wire format changes. The daemon compiles `needsinput.go`, but its behaviour is unchanged and it runs in the daemon only, not the supervisor.

## Risks / trade-offs

- **[A shared view changes a detector's result]** → Run the existing detector tests and `needsinput_realbytes_test.go` unchanged, plus a test that views and independent calls agree on every real-bytes fixture.
- **[Replay is stale after a resize with no output]** The dirty check ignores PTY size. → Existing behaviour for the other passes, and Claude repaints on SIGWINCH, which changes the log.
- **[Fewer reads hide a bug that relied on them]** Many of these maps exist because of a specific past bug. → Tick counters still advance every scan, and no `agent.*Tick` function changes. Tests cover idle replay, a change while out of rotation, and first-tick reads.

## Migration plan

Merge and relaunch. To roll back, revert the commit.

## Open questions

None.
