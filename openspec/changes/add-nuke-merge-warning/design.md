## Context

`heraOpenDelete` (`internal/tui/heraactions.go:333`) builds a confirm message synchronously, entirely on the tview goroutine, then calls `openHeraConfirm(title, msg, do)` — a plain y/N modal (`internal/tui/modal/confirm.go`) that word-wraps a message string but has no scrolling and no post-construction update mechanism. All three nuke entry points (`heraNukeRole`, `heraCascadeNukeFrom`, `heraClearArchive`) already funnel through this same confirm-then-act shape, so this change touches the message-building step at each of the three call sites, never the underlying reclaim mechanics (`heraReclaimAndArchiveTask` and its callers are untouched).

This codebase has a hard, previously-violated-and-fixed invariant: git operations never run synchronously on the UI thread (`context/knowledge/gotchas/ui-threading.md`; the canonical off-thread idiom is `go a.fetchGitStatus(...)` doing blocking work then `a.tapp.QueueUpdateDraw(func(){...})` with a staleness guard, `app.go:3675`). `add-merge-safety-classifier`'s Tier A is local-only (no network) but is still a git subprocess call, so it must follow this idiom, not be called inline from `heraOpenDelete`.

## Goals / Non-Goals

**Goals:**

- Every nuke confirm reflects the classifier's Tier A verdict for every task it's about to reclaim, computed off the UI thread, before the confirm modal is shown.
- Never block or gate the nuke — a "not confirmed merged" verdict changes the confirm's WORDING, never whether Enter/y proceeds.
- Uniform across all three nuke entry points (single role, cascade, clear-archived), consistent with how `fix-hera-archive-status` treated all three call sites uniformly for the status-advancement fix.

**Non-Goals:**

- No Tier B (network/`gh`) calls from this interactive path. A nuke confirm must never wait on GitHub or be at the mercy of network latency/outage — that's exactly the "daemon owns `gh`, TUI only reads cache" boundary this codebase already enforces for PR status, and it applies here for the same reason (interactivity), even though this classifier isn't a daemon-side poller. A task whose branch is already gone (so Tier A can't confirm it) will show as "not confirmed" at nuke time even if it was actually merged and Tier B would have found it — that's an accepted trade-off (see Risks), and the recovery path is `add-hera-cleanup-ui`'s retroactive Tier B classification for anything nuked before this lands, or that slips through as "not confirmed" here.
- No new modal-update capability. Rather than opening the confirm immediately and mutating its message once the async check resolves, this change computes the verdict(s) FIRST (in one background goroutine) and only opens the confirm once ready — see Decision below.
- No change to what gets reclaimed, archived, or how multi-binding preservation works. Purely additive to the confirm's message.

## Decisions

**Decision: compute the Tier A verdict(s) BEFORE opening the confirm modal, rather than opening it immediately and updating its text asynchronously.**

Alternatives considered:

- *Open the confirm immediately with a "checking…" placeholder, update it via `QueueUpdateDraw` once the check resolves.* Rejected: `ConfirmModal` has no message-update method today, so this would require adding one (a `SetMessage` + redraw) purely to support a placeholder state that exists for, at most, tens of milliseconds (Tier A is local-only). The added surface area (an update path, a "modal closed before the async result landed" race to guard against) isn't justified by that short a window.
- *Compute first, open the confirm once ready.* Chosen. Ctrl+D dispatches a goroutine that runs Tier A for the relevant task(s), then calls `a.tapp.QueueUpdateDraw(func(){ ...build the message with the verdict folded in...; a.openHeraConfirm(...) })`. Because Tier A is local-only (no `gh`, no network), the gap between key-press and modal-appearing is bounded by however long `git merge-base --is-ancestor` takes locally — negligible for any repo size this codebase operates on. This exactly mirrors the existing `fetchGitStatus` idiom (goroutine → blocking local work → `QueueUpdateDraw`), including its staleness guard (re-verify the role/orchestrator selection is still the same one the operator acted on before opening the confirm — if it changed or vanished in the interim, drop silently rather than popping up a stale confirm).

**Decision: single-role nuke checks one task; cascade and clear-archived check every task they're about to reclaim, concurrently, in the one background goroutine.**

A cascade or clear-archived confirm already aggregates counts (orchestrators/agents/worktrees/preserved) computed from the same subtree walk. This change adds one more aggregate: how many of the reclaimed tasks are Tier-A-confirmed vs. not. Because Tier A per task is a couple of local git subprocess calls (branch-exists + merge-base), and cascade/clear-archived subtrees in practice run to at most dozens of tasks, the per-task checks run concurrently (bounded worker pool, not one-at-a-time) inside the single background goroutine so total added latency stays proportional to the slowest single check, not the sum.

**Decision: message wording is a plain statement, not a scary banner.**

Confirmed-safe: no change to today's message (avoids noise on the common, fine case). Not-confirmed: append one line, e.g. *"Could not confirm this branch's work has been merged into `<default>`. Deleting it may lose unmerged work."* — descriptive, not alarming, consistent with Aaron's "warn but allow" framing and with this repo's existing confirm-message tone (`heraCascadeNukeFrom`'s existing message is already matter-of-fact about what's being reclaimed).

## Risks / Trade-offs

- **[Risk]** Tier-A-only means a task whose branch was ALREADY deleted by a prior partial action (or whose worktree/branch is otherwise gone before this specific nuke click) always shows "not confirmed," even if it was genuinely merged — Tier B could have found it via GitHub, but Tier B is out of scope here (see Non-Goals). → **Mitigation**: this only ever produces an unnecessary warning (safe direction), and `add-hera-cleanup-ui`'s Tier B pass is exactly the follow-up path for retroactively confirming those cases after the fact — the two changes are complementary, not redundant.
- **[Risk]** A coordinator/orchestrator task whose branch has genuinely never carried any code (common for coordinator-kind roles, which don't write code themselves) will show "not confirmed" every time, training operators to click through the warning reflexively rather than reading it. → **Mitigation / Open Question below**: worth considering a cheap additional local-only check — a branch with zero commits ahead of its recorded base branch has nothing to lose regardless of merge status, and could suppress the warning. Not included in this change's scope; flagged as a follow-up rather than blocking this proposal, since it's an orthogonal refinement to wording/precision, not to safety (omitting it never produces a false "confirmed safe," only an occasional unnecessary warning on already-known-empty branches).
- **[Risk]** Concurrent per-task Tier A checks in a cascade could momentarily spike subprocess/file-descriptor usage for a very large subtree. → **Mitigation**: bound the worker pool (e.g. a small fixed concurrency cap), consistent with how other bulk operations in this codebase (bulk cascade-nuke's own BUG-062 fix) already learned to bound concurrent work rather than firing it all at once.

## Open Questions

- Should a branch with zero commits ahead of its base branch (verified locally, no network) be treated as trivially safe regardless of the Tier A ancestor result, to avoid nagging on coordinator-kind roles that never had code? Leaving this to the coordinator/Aaron to decide as in-scope-now vs. a fast-follow — it doesn't change what's reclaimed either way, only whether the warning fires.
