## Context

`heraReattach` (`internal/tui/heraactions.go:963`) is reached from TWO physical key presses on a closed-out worker/freelance row, both routed through the same function:

1. The rail's `Enter` (`HeraPage.handleRailMutation`) fires `p.OnReattach(sel)` — this is the FIRST press — then unconditionally advances focus into the agent pane (`p.focus.SetRegion(FocusAgent)`).
2. With focus now on the agent pane, a SECOND `Enter` press is captured by `TerminalPane.InputHandler()`, which (session still dead) fires `tp.OnReattach()` (a no-arg callback wired per-pane in `panes.go`'s `bindPane`) → `HeraPage.reattachPane(tp)` → the SAME `p.OnReattach(sel)` → `App.heraReattach(sel)` again.

So both "first" and "second" Enter already funnel into the identical `heraReattach(sel)` call with the identical `taskID` — there is no separate call site or parameter that distinguishes them. The state that distinguishes press #1 from press #2 has to live somewhere `heraReattach` can read and mutate on each call.

`TerminalPane.Draw()` already has a passive, no-Enter-required rendering pattern for a dead session: `if tp.scrollOffset > 0 || !alive { ... }` unconditionally enters the replay path whenever the session isn't alive, regardless of whether Enter was ever pressed. When `tp.HasContent()` is true (a session log was loaded via `SetTaskID`/`loadSessionLog`), this already shows the last output with zero new code; when it's false, the earlier `sess == nil && !tp.HasContent()` branch shows the "Session not running - press Enter to start" placeholder instead. Both branches spawn no process — they only read `tp.replayData`/on-disk log tail into an `x/vt` emulator (`asyncReplayRebuild`/`paintEmu`).

## Decision 1: banner state lives on `TerminalPane`, not `App`

Alternatives considered:

- **A map on `App` keyed by task ID.** Works, but needs its own explicit reset on navigate-away/back (there's no existing per-task "you left" callback at the App level to hook), duplicating exactly what `bindPane`'s `SetTaskID`→`ResetVT`→`SetSession` sequence already does for every OTHER piece of per-binding pane state (`replayData`, `replayEmu`, `scrollOffset`, …).
- **A field on `TerminalPane`, reset in `ResetVT()`.** `ResetVT` already runs on EVERY hera pane rebind (`bindPane`'s unbind branch AND its rebind branch — see panes.go:167-221), including a rebind back to the SAME task after navigating away. A bare `bool` field piggybacks on that existing lifecycle for free: no new reset call site, and it stays correct if `bindPane`'s ordering ever changes, because it changes with everything else `ResetVT` owns.

Chose the second: `closedOutBannerShown bool` on `TerminalPane`, reset in `ResetVT()`. Since there is exactly ONE `TerminalPane` instance reused across every rail selection (`HeraPage.agentPane`), this field is inherently scoped to "whatever task is currently bound" — which is exactly the "resets per visit" requirement, for free.

`App.heraReattach` reaches the pane via the ALREADY-EXPORTED `HeraPage.AgentPane()` accessor. `heraReattach`'s close-out branch only ever runs when `sel.IsWorkerOrFreelance()` is true, and `applySelection` (panes.go) only ever binds the agent pane — never the coordinator pane — for a worker/freelance selection (coordinators have no close-out concept and are excluded from this branch entirely). So `AgentPane()` is always the pane bound to `taskID` whenever this code runs; no taskID-matching accessor is needed.

## Decision 2: reuse `TerminalPane`'s existing replay path, add no new one

The task's own framing already settled this ("reuse the EXISTING replay mechanism ... rather than building a new rendering path"), but it's worth recording WHY that's not just economical but the RIGHT choice given what's already there: `TerminalPane.Draw()` unconditionally renders replay content for any dead session with `HasContent()`, with no involvement from `Enter`/`OnReattach` at all. The banner therefore only needs to REPLACE that (and the placeholder) while armed, and do nothing extra when dismissed — no separate emulator, no separate "override" data structure. `PreviewVT` (the Tasks-list preview panel's persistent emulator) was considered and rejected: it exists for a genuinely different call site (a lightweight preview fed from a ring/log tail with no live `TerminalPane` binding at all), and using it here would stand up a SECOND parallel "read the log, feed an emulator" path for the exact content `TerminalPane`'s own replay machinery already owns.

## Decision 3: banner priority and gating in `Draw()`

The banner check is placed right after `Draw()` computes `alive` (`sess != nil && sess.Alive()`), guarded on `!alive`, and returns before either of the two existing dead-session branches (placeholder, replay). This means:

- The banner can never show over a live session, even if some future caller armed it and the session came back alive before the next `Draw()` (defensive, not expected to happen — `heraReattach` only arms it from the dead-session branch).
- Once dismissed, `Draw()`'s pre-existing branches decide what shows: replay content if `HasContent()`, else the (slightly repurposed-in-context, but unchanged) "Session not running - press Enter to start" placeholder. A closed-out task with a genuinely empty session log falls into the latter — a known, accepted edge case; adding a distinct "no output was ever recorded" message for this one case was judged not worth a third piece of banner state for something this rare.

## Decision 4: no separate third state — further Enters toggle

The task names exactly two states (first Enter → banner, second → read-only). It does not specify a third press. Because BOTH presses reach `heraReattach` through identical wiring with no side-channel to remember "already overridden, ignore future Enters," the two cheapest options were: (a) a `bool` that flips on every call (a toggle — third press re-arms the banner, fourth dismisses it again, …), or (b) a tri-state (`unset` / `shown` / `dismissed`) where dismissal sticks for the rest of the visit and further Enters no-op.

Chose (a): a plain toggle. It satisfies both stated requirements with the smallest possible state (one `bool`, matching every other simple per-binding flag on `TerminalPane` like `pending`/`forceResync`), and toggling back to the reminder on demand is a reasonable, discoverable behavior in its own right (the operator can re-surface the `hera_revive` hint without leaving and returning to the row). This is called out explicitly here in case a future report frames a third-press re-arm as a bug — it is intentional.

## Risks / Trade-offs

- A closed-out task with no recorded session log shows the ordinary "Session not running - press Enter to start" placeholder after dismissal — technically correct (no PTY starts on it) but the wording is written for the general dead-session case, not this specific one. Accepted per Decision 3.
- The toggle means a rapid double-tap of `Enter` (arm then immediately dismiss) is indistinguishable from a single deliberate "show me the output" gesture once the render settles — this is expected and matches the task's own two-press design.
