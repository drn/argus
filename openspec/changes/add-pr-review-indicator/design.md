# Design: PR review indicator

## Context

Argus tasks each own a git worktree and an `argus/<task>` branch. After an agent pushes and a PR is opened, argus shows nothing about the PR's review state, so the user leaves the tool to check GitHub. Exploration confirmed `gh` is already shelled out for the "open PR in browser" action (`internal/tui/app.go` ~2240), reading the remote from the worktree dir — so detection needs no new dependency, and PR review state is fully derivable from one `gh pr view` JSON call.

## Goals

- Surface, per task, whether its branch has an open PR and that PR's review state.
- Keep the indicator non-blocking: it must never run network I/O on the UI thread, and a transient failure must never clobber a known-good value.
- Show it in both the TUI task list and the web PWA, fed from a single cached source.

## Non-Goals

- Auto-advancing `task.Status` to `in_review` when a PR opens (conflates user workflow intent with GitHub state).
- A project-level rollup glyph (possible later; not v1).
- Acting on PR title/body content — gh output is data, parsed for JSON fields only.

## Decisions

### Rendering: second indicator cell (coexist), not replacement

The task row draws one status glyph chosen by a priority chain (`tasklist.go drawTaskRow`, status at line 1149, `col += 2` at 1150). PR state is **orthogonal** to that glyph: a task can be actively running *and* have an open PR, or `in_review` (a manual workflow state) with no PR yet. So we add a *reserved second cell* after the status glyph rather than overloading it (the `needsInput` replacement pattern would hide live agent state). The cell is always reserved (blank when no indicator) so the name column never jitters as PRs appear/vanish. `maxNameW` (line 1154) is derived from `col`, so it adapts automatically once the cell advances `col` by 2.

### Granularity: distinct glyph + color per review state

Awaiting-review (purple), changes-requested (red), approved (green). Draft and merged/closed render nothing. The full `PRState` enum is always stored, so changing granularity later is a render-only change.

### Detection: `gh pr view`, behind a test seam

`gh pr view <branch> --json state,isDraft,reviewDecision,url` with `cmd.Dir = worktreeDir`, `context.WithTimeout(5s)` (matches `runGit`, `gitcmd.go:204`). Prefer `view` over `pr list --head` — it returns a single object (or non-zero "no pull requests found"), matching argus's 1:1 branch↔PR model. Exposed behind a package-level `var prFetcher = func(ctx, worktreeDir, branch) (model.PRState, string, error)`, mirroring the existing `prOpener` var so tests inject a fake without spawning processes.

State mapping:

| gh result | PRState |
|---|---|
| non-zero exit "no pull requests found" | `PRNone` |
| `state=OPEN, isDraft=true` | `PRDraft` (renders nothing) |
| `state=OPEN, reviewDecision="" \| REVIEW_REQUIRED` | `PRAwaitingReview` (purple) |
| `state=OPEN, reviewDecision=CHANGES_REQUESTED` | `PRChangesRequested` (red) |
| `state=OPEN, reviewDecision=APPROVED` | `PRApproved` (green) |
| `state=MERGED \| CLOSED` | `PRMergedClosed` (renders nothing) |
| gh absent / unauthenticated | `PRUnknown` (log once via uxlog, render nothing) |

### Where it runs: daemon-side poller → task_meta

The daemon already owns the cross-task background-loop pattern (the MCP idle sweep, `daemon.go:404-422`, gated by `d.done`). A sibling goroutine polls every **60s** with a bounded worker pool (≤4 concurrent `gh` procs), over **non-archived tasks that have a branch** (covers complete/in_review tasks — exactly when a PR is usually up). It persists via `db.SetMetaBatch(taskID, "pr", {state, url})`. Daemon-only fetch means: the PWA gets fresh data with no TUI attached, there's a single writer, and the TUI/API just read cached state.

Data path:

```
daemon prPoller (60s) ── prFetcher(worktree, branch) ──> db.SetMetaBatch(taskID,"pr",{state,url})
TUI app tick ── db.ListMetaByNamespace("pr") ──> tl.SetPRStates(map[taskID]PRState) ──> drawTaskRow
API handlers ── read task_meta "pr" ──> taskJSON.pr_state ──> PWA badge
```

The TUI reads cached state inside the existing tick that already calls `SetNeedsInput` within `QueueUpdateDraw` (`app.go` ~1547) — `SetPRStates` slots in there, no new threading, no `screen.Sync()`. A new `db.ListMetaByNamespace(namespace) (map[taskID]map[key]value, error)` helper lets the tick do one indexed query instead of one per task.

## Risks / Trade-offs

- **Rate limits:** 60s cadence × non-archived-with-branch tasks × ≤4 concurrent procs stays well under GitHub's authenticated limit. Add jitter / raise to 120s if task counts are large.
- **gh auth/offline:** degrades to invisible (`PRUnknown`), logged once, never per-tick. Daemon's gh auth is what matters (documented).
- **Staleness:** up to 60s; `updated_at` stamped by `SetMeta` enables a future "grey out stale" enhancement. On daemon restart, last-known values show until the first tick.
- **Keep-stale-on-error:** transient fetch errors must not overwrite a good cached value — only a successful parse or an unambiguous `PRNone` writes.

## Alternatives considered

- **Replace the status glyph (needsInput pattern):** simplest, zero layout change, but hides live agent state while a PR is open. Rejected — the two facts are independent.
- **Summary bar elsewhere (attentionbar-style) / project rollup:** clean rows, good for an at-a-glance count, but not a per-task scan in the list. Possible later complement, not the primary surface.
- **TUI-side batch poll instead of daemon:** would not refresh the PWA without a TUI attached and risks two clients hammering gh. Rejected.
- **`gh pr list --head <branch>`:** returns a list needing more parsing than `view`'s single object. Rejected.

## Acceptance criteria

### Detection / mapping (capability: pr-status)

- it should report `awaiting-review` when `gh pr view` returns an open, non-draft PR with empty or `REVIEW_REQUIRED` review decision.
- it should report `changes-requested` / `approved` for the corresponding `reviewDecision` values.
- it should report `none` when gh exits non-zero with "no pull requests found".
- it should report `draft` for an open draft PR and `merged-closed` for merged or closed PRs.
- it should report `unknown` (not an error to the UI) when `gh` is absent or unauthenticated, logging once.
- it should leave the previously cached `task_meta` value intact on timeout or network error.

### Poller (capability: pr-status)

- it should poll only non-archived tasks that have a branch, skipping archived and branchless tasks.
- it should write `state` and `url` to `task_meta` namespace `pr` on a successful fetch.
- it should stop cleanly when the daemon shuts down.

### TUI rendering (capability: pr-status)

- it should draw a distinct glyph/color in a reserved cell beside the status glyph for awaiting-review, changes-requested, and approved.
- it should draw a blank reserved cell (no name-column shift) for none/draft/merged-closed/unknown.
- it should keep the existing status glyph unchanged regardless of PR state.

### Web parity (capability: pr-status)

- it should include a `pr_state` field on the task DTO populated from cached `task_meta`, without the handler shelling out to gh.
- it should render a matching PR badge in the PWA task list for awaiting-review/changes-requested/approved.

## Migration Plan

Additive only — new enum, new `task_meta` namespace, new render cell, new DTO field. No schema migration (task_meta is generic key/value). No backward-compat concerns (single user). Service worker `SW_VERSION` bump required so installed PWAs pick up the new shell.

## Open Questions

- Exact Nerd Font codepoints for the three glyphs — must be render-tested in a real terminal for distinctness before commit (git-pull-request family, e.g. `0xF407` and siblings).
