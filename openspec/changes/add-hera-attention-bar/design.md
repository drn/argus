# Design: Hera-view needs-input summary box

## Context

In the native Hera view (the always-present 2nd tab) the operator works inside one orchestration — coordinator pane, worker terminals, the rail tree. There is currently no signal that a task **outside** the Hera tab needs the operator's input. The agent/task view solves the analogous problem with `widget.AttentionBar`, a bordered box at the top of the left column that lists other tasks blocked on a prompt. Hera has no equivalent, so a freelance/standalone task can sit in the `(?)` needs-input state indefinitely while the operator is heads-down in Hera.

The needs-input set is already computed and already piped into the Hera page: `App` runs `a.heraPage.SetNeedsInput(a.needsInputIDs)` every tick (`internal/tui/app.go:1953`), where `a.needsInputIDs` is the authoritative, idle-gated `agent.DetectNeedsInput` set across **all** tasks. The page stores it as `p.needsInput map[string]bool` and today consumes it only to stamp the per-role rollup (`hera-needsinput-rollup`).

## Goals

- Surface a heads-up in the Hera rail column whenever a needs-input task has **no presence in the Hera tab**.
- Reuse the existing, already-piped needs-input set — no new data feed, no App-side name plumbing.
- Respect the Hera page's rendering invariants: no `screen.Sync()`, full-rect coverage, pure-`Draw` geometry, goroutine-free.

## Non-Goals

- Listing **which** tasks need input. In the Hera tab those tasks are not actionable (they belong to the Tasks tab), so naming them would imply a reachability the view does not offer. A count is the honest signal.
- Any new keybinding, focus target, or click-to-jump. The box is passive, mirroring the agent-view bar (a non-focusable `tview.Box` with no input handlers).
- Touching the agent-view `widget.AttentionBar` or its `updateAttentionBar` feed.
- Counting tasks that are already represented in the rail (managed roles, including those whose subtree row is folded — their cue bubbles up via the subtree rollup — and Hera freelance-roles, which have their own rail section).

## Decisions

### What the box counts

The box counts **unmanaged** needs-input tasks: the residual after removing every task the Hera model knows about.

```
count = | needsInput  −  managedTaskIDs |
```

where `managedTaskIDs` is the union, over every orchestrator section (Pinned, Active, Archived) and the Freelance section, of each role's `TaskID` and `BridgeTaskID`. Collecting both the live binding (`TaskID`) and the latest-binding structural key (`BridgeTaskID`) makes the exclusion robust if a role's binding ends while its task is still running.

Rationale for excluding folded-subtree workers and freelance-roles: both are already cued *in the rail* — a folded parent shows the `SubtreeNeedsInput` rollup `(?)`, and freelance-roles render in the rail's Freelance section. Counting them in the box would double-report a cue the operator can already see in this tab.

### Count-only, not a named list

A fixed one-line box — `"2 tasks need input"` / `"1 task needs input"` — rather than the agent-view's growing named list. This:

- removes the need to resolve names (the residual tasks are by definition absent from the Hera model, so the page has no names for them; a count needs none),
- keeps `SetNeedsInput([]string)` unchanged (no App plumbing change),
- gives a fixed small footprint (3 rows when shown, 0 when hidden) with no overflow/"+N more" logic.

### A dedicated widget, not a mode on `AttentionBar`

A new `widget.AttentionSummary` (`SetCount(int)`, `DesiredHeight() int`, `Draw`) lives beside `AttentionBar`. Overloading `AttentionBar` with a list-vs-count mode would entangle two render paths in one widget; a focused new widget keeps both pure and independently testable. It reuses the shared `widget.DrawBorderedPanel` and the `StyleInReview`/`StyleNeedsInput` theme so it reads as the same "needs attention" family as the agent-view bar.

`DesiredHeight()` returns `0` when count is `0`, else `3` (one text line + the 2 border rows).

### Geometry — pure Draw math in the rail column

In `HeraPage.Draw`, before laying out the rail:

1. Compute `count` and `summary.SetCount(count)`; `barH = summary.DesiredHeight()`.
2. Clamp so the rail keeps a usable minimum on short terminals: if `h - barH` would leave the rail too short, the box yields (drop `barH` to 0). Concretely, only draw the box when `h - barH >= minRailHeight`.
3. If `barH > 0`: draw the box at `(x, y, railW, barH)` and the rail at `(x, y+barH, railW, h-barH)`. Else the rail occupies `(x, y, railW, h)` exactly as today.

No `OnHeightChange`/flex `ResizeItem` is needed — unlike the agent view (a `tview.Flex`), the Hera page recomputes rects every frame, so the box appears/disappears purely as a function of the current count. This preserves the no-Sync / full-rect-coverage invariants because the box paints through `DrawBorderedPanel` (which blanks its interior) and the rail still paints its own full rect just below it.

### Where the count is computed

In `Draw`, from the two live sources the page already holds: `p.needsInput` (refreshed per tick by `SetNeedsInput`) and `p.rail.Model()` (refreshed by the debounced `doRefresh`). Computing in `Draw` keeps the box always-consistent with whatever was last fed, needs no extra cached state, and matches the page's existing "geometry recomputed each frame" style. The model walk is O(roles) over a small in-memory snapshot.

### Remote mode

The page is inert in remote mode (`p.remote` → it draws only the unavailable banner and returns early before any rail/region layout). The summary box is drawn in the same body that the remote path short-circuits, so it is never drawn remotely with no extra guard.

### Logging

`uxlog.Log("[hera-view] attention summary: %d unmanaged task(s) need input", count)` on the show/hide transition (count crossing 0↔N), not every frame — mirroring the codebase rule to log state transitions and silently-skipped work, not steady state.

## Alternatives considered

- **Named list (reuse `AttentionBar` directly).** The original design. More informative, but requires App to push task names (the residual tasks aren't in the Hera model), implies the tasks are actionable from Hera, and grows the rail. Rejected in favour of the count after establishing that "which" is not useful in this tab.
- **Count only tasks not visible in the current rail viewport (fold/scroll-aware).** Considered, then explicitly rejected: a folded-away managed worker's cue still bubbles up to its visible parent via the rollup, so it is not genuinely hidden. Scoping to "no presence in the model at all" is both simpler and the correct notion of "invisible from this tab."
- **Click/Enter to jump to the Tasks tab.** Rejected: adds a keybinding (help-modal + README churn) and cross-tab navigation for a heads-up whose whole point is "go look over there," and the agent-view bar it mirrors is itself passive.

## Risks / Trade-offs

- **Reduced rail height when the box is shown.** Mitigated by the short-terminal clamp (box yields rather than starving the rail) and the fixed 3-row footprint.
- **Per-frame model walk.** Negligible — the model is a small in-memory snapshot already walked elsewhere each Draw (e.g. rollup, bridge index).
- **Double-signal perception.** A managed worker needing input shows in the rail but not the box; an unmanaged task shows in the box but not the rail. The two sets are disjoint by construction, so there is no double counting.

## Acceptance criteria

Layout / visibility (area 5):

- it should draw a `"N tasks need input"` box at the top of the rail column when N ≥ 1 unmanaged tasks need input
- it should pluralise correctly: `"1 task needs input"` for N=1, `"N tasks need input"` for N>1
- it should hide the box (zero height) and give the rail the full column height when no unmanaged task needs input
- it should reduce the rail's drawn height by exactly the box height when the box is shown
- it should yield (not draw the box) on a terminal too short to keep the rail usable
- it should never draw the box in remote mode

Counting / exclusion:

- it should count a needs-input task that has no presence in the Hera model
- it should not count a coordinator, a managed worker (even when its subtree row is folded), or a Hera freelance-role
- it should report zero when every needs-input task is known to the Hera model

Widget:

- it should report `DesiredHeight() == 0` for count 0 and a fixed positive height for count ≥ 1
- it should render through the shared bordered-panel/theme so it matches the agent-view attention styling

## Migration Plan

None — additive UI. No schema, config, or API changes.

## Open Questions

None.
