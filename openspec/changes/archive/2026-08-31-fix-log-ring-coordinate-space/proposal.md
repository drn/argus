# Fix live-pane history reads assuming the session-handle byte counter is a log-file offset

## Why

`TerminalPane.emuFedTotal` counts bytes in the **session handle's** byte space. Two
places in the live-render path treat that counter as an **absolute offset into the
on-disk session log**:

- `renderLive`'s ring-wrap catch-up calls `readLogRangeForTask(taskID, emuFedTotal, newBytes)`
  (BUG-073 / PR #912).
- `readLiveRebuildHistory` computes `overflow := int64(ringTotal) - logSize` and splices
  ring bytes onto the log tail based on that subtraction (BUG-076 / PR #919).

Both are correct for an in-process `agent.Session`, whose `readLoop` writes the ring
buffer and the log file from the same slice in the same iteration — the invariant
`readLogRangeForTask`'s doc comment states explicitly.

They are **wrong for `daemon/client.RemoteSession`**, which is what the TUI uses in
normal daemon-connected operation. On first stream attach the client sends `Since: 0`
and `Session.AddWriterFromTolerant` replays **at most the daemon's 256 KB ring**, not
the session from byte 0. The client's ring total therefore starts near zero and counts
only the bytes *this client* received, while the log holds the session's full history.

Measured on a live TUI across 25 concurrently-bound tasks, the skew between the client's
`TotalWritten()` and the on-disk log size ranged from **−1.8 MB to +3.4 MB**:

| task | clientTotal | logSize | skew |
| --- | ---: | ---: | ---: |
| 1788157406127059000 | 855,285 | 4,264,194 | +3.41 MB |
| 1788158580826111000 | 129,478 | 3,180,511 | +3.05 MB |
| 1788123287881366000 | 2,652,187 | 806,379 | −1.85 MB |

Consequences, both of which paint valid-but-wrong ANSI into the LIVE emulator:

1. **Ring-wrap catch-up** reads a window megabytes earlier in the session and feeds that
   ancient conversation content on top of the current screen — the reported
   "two different messages' text woven together", plus stray 1–4 character fragments at
   the right margin (residue of the injected window's absolute cursor positioning after
   the agent's next partial repaint).
2. **Full-replay merge** splices a non-contiguous ring suffix onto the log tail whenever
   `ringTotal > logSize`, or records a file size as a ring-space `emuFedTotal`.

This is a distinct defect from PR #937 (committed-width drift), which only governs *when*
the size-drift kill+resume kick fires. It is also not a wrong-width emulation problem:
replaying two real session logs through the exact full-replay path at 88 / 178 / 212
columns renders identically and cleanly, because Claude Code self-wraps and repaints
full-screen.

No existing test caught it because every `mockAdapter` in the suite is constructed with
`totalWritten` equal to the session log's length — the local-mode invariant is hard-coded
into the fixtures, so the daemon-client offset is never exercised.

## What Changes

- Both log reads anchor on **content** (the ring tail that is already in hand) instead of
  on arithmetic over two counters that are only comparable in local mode.
- The ring-wrap catch-up reads the `newBytes` log bytes that end where the ring tail ends,
  falling back to the existing approximate rebuild when the ring tail cannot be located.
- The full-replay merge trims the log at the ring head when the log covers it, appends only
  a *verified*-contiguous ring suffix when the log lags, and falls back to the ring alone
  when the two cannot be reconciled at all.
- `emuFedTotal` is only ever recorded in the session handle's own byte space.

## Impact

- Affected specs: `terminal-rendering`
- Affected code: `internal/tui/terminal/terminalpane.go`
- No wire-protocol change and no `ProtocolVersion` bump: making the client's ring total
  absolute would be the more structural fix but introduces a daemon/supervisor skew
  surface, which is what produced two earlier false alarms on this same visual symptom.
