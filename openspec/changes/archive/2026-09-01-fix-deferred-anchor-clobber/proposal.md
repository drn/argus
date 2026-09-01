# Fix a deferred rerender kick erasing the width its own scrollback is still committed at

## Why

A session's scrollback is baked at whatever PTY width it was emitted at; SIGWINCH re-flows
only the live UI. The only repair is the kill+resume "rerender kick", and the decision to
fire it (`agent.ShouldKickRerender`) compares the current panel width against two anchors:

- `initCols` — immutable, the width the session started at.
- `committedCols` — a caller-tracked second anchor added by PR #937 (BUG-078) for the gap
  `initCols` cannot cover: a mid-session bind at a width that crosses the margin but never
  resolves (deferred while busy, then the pane moves on) leaves the scrollback drifted to
  that width while `initCols` stays fixed at session start.

PR #937 records that second anchor from the **deferred** outcomes — the three "a kick was
owed but not taken" branches in `App.maybeKickRerenderAtWidth` each do
`a.committedCols[taskID] = panelCols`. The write is unconditional, so it **overwrites an
existing anchor that is itself still unreconciled**, and because the replacement equals
`panelCols` by construction, every subsequent evaluation at that same width reads
"committed == panel, no drift" and drops the still-owed kick permanently.

Live `ux.log` trace for task `1788286700878673000` (session started at 80 columns, content
committed at 142):

```
11:53:06 [tui] rerender deferred: task=… busy (init=80 committed=142 panel=90)
11:55:24 [tui] rerender: skipping kick task=… — panel cols unchanged since last attach (90)
11:55:37 [tui] rerender: skipping kick task=… — panel cols unchanged since last attach (90)
11:56:36 [tui] rerender: skipping kick task=… — panel cols unchanged since last attach (90)
11:58:49 [tui] rerender: skipping kick task=… — panel cols unchanged since last attach (90)
```

The 11:53:06 defer read the correct `committed=142` and then clobbered it to 90. From that
point `MarginExceedsRerenderThreshold(80, 90)` is 10 (< `RerenderMargin` 15) and
`MarginExceedsRerenderThreshold(90, 90)` is 0, so both anchors report "no drift" while the
pane renders 142-column content in a 90-column emulator, for as long as the pane keeps that
width.

Replaying that task's real session log through the live full-replay path confirms the
resulting artifact is exactly the one reported. At the authored 180 columns the same bytes
render cleanly; at 90 they produce mid-word splits and stray single characters piled in the
last column:

```
180 |⏺ Honest answer: no, not the actual gauntlet — I ran the equivalent check via the unit test suite (gauntlet-appraiser-v2.test.ts's stub-mode
    |  internal-consistency gate, which asserts 0 envelope failures across every template × posture), but I didn't run raictl run or hit

 90 |⏺ Honest answer: no, not the actual gauntlet — I ran the equivalent check via the unit tes
    |t D gfood's deployed harness is current enough (post the multi-workstream content-resoluts
    |uite                                                                                     (
    |gauntlet-appraiser-v2.test.ts's                                                          s
    |tub-mode  RAICTL=/private/tmp/claude-501/-Users-aaron--argu…
```

Claude Code redraws differentially (`ESC [ n G` column jumps that assume the previous
frame's cells are still present), so a column-clamped emulator both truncates those jumps
into the last column and re-wraps every authored line mid-word.

This is not a `terminal-rendering` read-path defect: the same log replays cleanly at its
authored width through the current (post-#964) code, so the log bytes, the log/ring
anchoring, and `emuFedTotal` are all correct. It is purely the kick predicate's anchor
bookkeeping.

## What Changes

- The three "kick owed but not taken" branches (`RerenderDeferBusy`, `RerenderDeferPrompt`,
  a failed `Stop`) record the committed-width anchor through one helper instead of writing
  the map directly.
- That helper refuses to overwrite an existing anchor that still differs from `panelCols`
  by more than `RerenderMargin` — that anchor names a width the scrollback is genuinely
  still committed at and which no kick has re-emitted. When the existing anchor is *within*
  the margin of `panelCols` it describes effectively the same width, so the fresher value
  wins (PR #937's original behavior for the case it was written for).
- The anchor's clears are unchanged: a kick that actually fires deletes it (the resumed
  session's own `InitialPTYSize` becomes the fresh reference), as does task deletion.

The change can only WIDEN which candidates stay kickable, never narrow it, so it cannot
regress #937.

## Impact

- Affected specs: `idle-detection`
- Affected code: `internal/tui/app.go`
- The web/REST resize handler (`Server.maybeKickRerender`) has no `committedCols` equivalent
  at all — it gates on `initCols` only, so it carries the *original* pre-#937 gap rather
  than this clobber. Out of scope here and named as a follow-up rather than silently fixed;
  see Non-Goals.
- The macOS app is a REST client of that same handler and inherits the same follow-up.

## Non-Goals

- Porting the `committedCols` second anchor to `Server.maybeKickRerender` (REST/web/macOS).
  That is #937's own unported half, not a regression introduced here.
- Making the kick land while an agent is continuously busy. The live log shows tens of
  thousands of consecutive `rerender deferred: … busy` lines across the fleet; deferring is
  correct (killing an agent mid-tool-call is worse than a mis-wrapped pane), but it means a
  perpetually-busy session can stay visibly drifted for a long time even with this fix. A
  render-side mitigation — emulating the live pane at the authored width and clipping, the
  way `replayEmuDims` already does for the scroll-replay emulator — is the structural
  answer and is deliberately left for its own change.
