## Context

The main agent view (`internal/tui/app.go`) already carries a fix for "a session's committed scrollback was authored at a different PTY width than the current viewer": `agent.ShouldKickRerender` (pure decision function, `internal/agent/rerender.go`) gates on a `RerenderMargin` (15 cols) width delta between the session's `InitialPTYSize()` and the current panel width, plus idle/needs-input/pending/session-ID guards, and `App.maybeKickRerender` + `App.isRedundantAttach` wire it into agent-view entry (`onTaskSelect`). When it fires, `Runner.KickRerender` stops the session; the EXISTING, already-global exit-hook (`App.handleSessionExitUI`, driven by `Runner`'s `onFinish` / the daemon client's exit callback — NOT agent-view-specific) resumes it via `--session-id` at the new dimensions once the process actually exits. The web API mirrors the same predicate independently (`Server.maybeKickRerender`, `internal/api/handlers.go`) for xterm.js resize events.

The native Hera view (`internal/tui/hera/panes.go`) never wired ANY of this in. `bindPane` only calls `ForceResyncPTY()` (resize the PTY to match the pane — a plain SIGWINCH, not a kill+resume), on literally every single pane (re)bind, for both the coordinator (HERA) pane and the worker/agent pane. Reproduced offline against a real dogfood session log (task `1785307013068092000`, "sketch-links", ~767KB — well under any log-size-window concern) that this causes visible, persistent corruption: the session's PTY was genuinely resized at least once during its real life, and a fresh full-history replay (what any Hera bind does — `bindPane`'s `SetTaskID→ResetVT→SetSession` sequence forces `emuMissing=true` on the shared pane widget) bakes writes authored for the OLD size and writes authored for the NEW size into the same grid, unreconciled, because a raw byte replay has no resize events to replay. Confirmed via direct emulator-cell inspection (`tp.emu.CellAt`), bypassing tcell/paint-cache entirely, so this is a data-reconstruction defect, not a rendering/damage-tracking one.

## Goals / Non-Goals

**Goals:**
- Give both Hera panes (coordinator/HERA and worker/agent) the same kill+resume protection the main agent view already has, reusing the existing `agent.ShouldKickRerender` / `Runner.KickRerender` mechanism as-is.
- Evaluate the drift check using the Hera pane's OWN current width, not the main agent view's (they can differ, and Hera's panes are typically narrower).
- Keep it a new call site, not new gating logic in the common case — the shared predicate, its margin, and its idle/prompt/pending guards are unchanged for the two existing callers.

**Non-Goals:**
- Redesigning `ShouldKickRerender`'s cols-only predicate to also gate on rows in this change (see Open Questions — flagged as a real gap, not silently fixed here; it touches the API/web surface too, which is out of scope for a Hera-focused bug).
- Any change to `Runner.KickRerender`, `handleSessionExitUI`, or the resume-at-new-dimensions mechanism itself — all reused unmodified.
- Per-task emulator caching / retaining a pane's state across a rail navigate-away-and-back (a bigger architectural change flagged separately during investigation; this fix reduces how often that path produces a WRONG reconstruction, it doesn't eliminate the rebuild-on-every-bind design).

## Decisions

**1. Extract `App.maybeKickRerender`'s width-parameterized core into a new method, called from both the existing agent-view-entry site and a new Hera-facing entry point.**

`maybeKickRerender(task, sess)` currently computes `panelCols` itself via `a.computePTYSize()` (the main agent view's own pane rect) — wrong width for a Hera-bound session. Alternative considered: duplicate the whole method for Hera with its own cache. Rejected — it would fork the busy/prompt/pending gating logic across two copies that must stay in sync by hand, exactly the failure mode the existing code comments already warn about for the TUI/API split. Instead: `maybeKickRerenderAtWidth(task, sess, panelCols uint16)` holds the unchanged logic; `maybeKickRerender` becomes a 2-line wrapper that computes `panelCols` via `computePTYSize()` and delegates; a new `heraKickRerender(taskID string, panelCols uint16)` resolves `task`/`sess` by ID (mirroring `SetSessionResolver`'s pattern) and delegates to the same core.

**2. Reuse the existing `isRedundantAttach` / `lastAttachCols` cache as-is (keyed by taskID only, not by which surface asked).**

A task viewed alternately from the main agent view (wide) and a Hera pane (narrow) will see the cache "miss" each time it switches surfaces, since the two views legitimately have different widths — this can produce a kick each time the SAME task is reopened from a different surface at a different width. Accepted: this is the correct, intended behavior of a width-based cache (a genuinely different width is not a redundant reopen), not a regression introduced by this change, and the case (repeatedly toggling between viewing the same task in both the main view and Hera) is expected to be rare in practice. A per-surface cache key was considered and rejected as unnecessary complexity for an edge case that self-heals (each surface stabilizes at its own width after one kick).

**3. Evaluate the kick from `Draw()`, right after each pane's `SetRect`, NOT from `bindPane`'s genuine-rebind path — revised during implementation.**

The original plan was to evaluate the check inside `bindPane`, mirroring the main agent view's "check once on entry" semantics, using the pane's own tracked width (`tp.ptyCols`, seeded by `SetSession`). Implementation surfaced a real gap in that plan: unlike the main agent view (one persistent pane, always shown at a stable width whenever the view is active), a Hera pane can be HIDDEN (coordinator selected → details mode → the agent pane is never given a rect at all) and then shown for the first time in the session — at that exact bind moment, `tp.ptyCols` is still 0, so a `bindPane`-time check would be silently skipped for the very first coordinator→worker transition every session (reproduced with a test before fixing it). `Draw()` calls `HeraPage.maybeKickPaneRerender(bound, kickedFor, cols)` right after each of its four `SetRect` call sites (fullscreen coord/agent, split coord/agent) instead — `cols` there is always the JUST-computed, correct value, regardless of mode-transition timing. `reconcileOne`'s tick-driven late-bind/dead-handle path is still NOT touched, preserving the "evaluate on bind, not continuously" intent the original plan aimed for.

**4. Track "already evaluated" per pane via new `coordKickedFor`/`agentKickedFor` string fields, not the pane's own state.**

Since the check now runs on every `Draw()` (not just on a genuine `bindPane` rebind), it needs its own idempotency marker to avoid a DB lookup + `runner.Get` on every frame. `HeraPage.coordKickedFor`/`agentKickedFor` (paired with the existing `coordBound`/`agentBound`) record which bound task has already been evaluated; `bindPane` resets the relevant marker to `""` on unbind so a later rebind to the SAME task still gets a fresh evaluation rather than being silently suppressed forever. This is purely a per-Draw-call optimization — `App.isRedundantAttach` (Decision 2) remains the actual correctness gate against re-kicking at an unchanged width.

**5. New `HeraPage.SetRerenderKicker` callback, mirroring `SetSessionResolver`'s existing wiring pattern.**

`internal/tui/hera` has no access to `*db.DB`/`*agent.Runner`/`agent.SessionHandle` today (deliberately — `SessionResolver` already narrows this to `agentview.TerminalAdapter`). A callback keyed by taskID (`func(taskID string, panelCols uint16)`), wired once by `App` the same way `SetSessionResolver` is, keeps that boundary intact rather than widening `agentview.TerminalAdapter` or exporting `Runner`/`db` into the hera package.

## Risks / Trade-offs

- **[Risk] The shared predicate is cols-only; this reproduction's clearest evidence (holding cols fixed, varying rows) is row-driven.** → Real terminal resizes change both dimensions together in the overwhelming majority of cases (Argus's own pane-size math derives both from the same terminal window dimensions), so the existing margin should catch most real drift even though it wouldn't catch a synthetic rows-only change. Flagging as an explicit open question below rather than silently expanding the predicate's scope (which would also touch the web API surface, outside a Hera-focused fix) or silently shipping a fix I can't fully verify addresses 100% of the reported cases.
- **[Risk] Kicking a live CONDUCTOR session is a bigger interruption than kicking a worker** (the coordinator may be mid-orchestration). → Unchanged from the existing predicate's own mitigations: gated on idle and on not being blocked on a prompt; a coordinator mid-tool-call or awaiting input is never kicked. Accepted per the coordinator's explicit "both coordinator + worker panes" direction.
- **[Risk] Toggling between the main agent view and a Hera pane for the same task could kick on every switch** if their widths genuinely differ and both exceed the margin from `initCols`. → See Decision 2; expected to self-heal (the FIRST kick at either width rebases `initCols` for that surface's next attach).

## Migration Plan

No data migration. Purely additive TUI wiring; both existing callers of `ShouldKickRerender`/`MarginExceedsRerenderThreshold` (TUI agent-view-entry, API resize handler) are unchanged. Rollback is a plain revert (no persisted state format changes).

## Open Questions

- Should `ShouldKickRerender` gain a symmetric row margin (mirroring `RerenderMargin`) so a height-only drift is also caught? This change deliberately does NOT do so (it would touch the shared predicate used by the web API too, beyond this Hera-scoped fix) — flagging for a follow-up decision once this fix is dogfooded and we know whether the cols-only margin catches the corruption in practice.
