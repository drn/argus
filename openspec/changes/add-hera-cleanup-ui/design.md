## Context

The stuck-task predicate (`archived=1`, `status=in_review`, no live Hera binding — the same one `fix-hera-archive-status`'s design doc defines) currently has 737 matches across 9 projects, found by a one-time manual/LLM-driven classification pass. That pass validated the exact evidence tiers `add-merge-safety-classifier` now encodes (106/243 confirmed via Tier A local ancestry, 137/243 via Tier B merged-PR lookup with a branch-name-reuse guard that demoted 4 initially-plausible matches) and surfaced the two things this design has to account for:

- Repo resolution for an already-archived task can't rely on its worktree (usually gone) or a cached PR url (archived tasks are excluded from the existing PR-status poller, so none exists) — it has to come from the project's configured `path` in the `projects` table, keyed by the task's `project` column. For a project whose row has since been deleted (found once, `Hera`, 15/737 tasks), no such lookup is possible; those tasks land in Needs Review via the classifier's own "unresolvable repo" case, no special-casing required.
- Classifying ~500 still-undetermined candidates the manual pass didn't already resolve via Tier A means several dozen Tier B GraphQL calls across up to 9 repos — bounded (one batched query per repo, per the classifier's design) but not free, and definitely not something to run on every popup open given the documented GraphQL-budget incident.

This is a maintenance action, not a hot path — the popup is opened rarely and deliberately, unlike the nuke confirm in `add-nuke-merge-warning`, which is on the critical path of a common interactive action. That difference is why this change can afford network calls and a caching layer that change deliberately avoided.

## Goals / Non-Goals

**Goals:**

- Give the operator a way to see the classified backlog and act on it repeatably, in-product, without hand-rolling SQL or a one-off script.
- Never touch a Needs Review task automatically. The only path a Needs Review task's status changes is the explicit "mark all complete" bulk action, which the operator chooses knowing what it includes (the popup shows the count and the reasons before that action is available).
- Reuse the existing, already-hardened `PruneCompleted` flow for actual deletion rather than building a parallel one (see the open decision).
- Cache classification results so the shared GitHub GraphQL budget is spent once per candidate, not once per popup open.

**Non-Goals:**

- No web PWA or macOS parity in this stage — named explicitly, per this repo's Frontend Parity rule, as a follow-up (mirrors the existing standing "Hera mutations are TUI-only" gap already accepted for other Hera actions).
- No standing background poller. Classification is triggered by opening the popup (or an explicit refresh), never a periodic tick.
- No per-row toggle-then-confirm (deselecting individual tasks within a section before acting). The two bulk actions (Safe-only / all) match Aaron's original framing exactly; per-row selection is a plausible future enhancement (the codebase already has a checklist-toggle precedent, `AppleEventsPickerModal`) but adds meaningful scope for a first cut and isn't required to close the actual gap.

## Decisions

**Decision (recommended, needs sign-off): "mark complete" flips `status → complete` only. It does NOT delete the task, worktree, or branch.**

Alternatives considered:

- *Extend this popup to also delete (fold into `PruneCompleted`'s mechanics directly).* Rejected as the default: `PruneCompleted` already has its own audited safety properties (the live-Hera-binding guard from PR #927, per-repo cleanup locking, panic recovery from the bulk cascade-nuke incident, BUG-062) built up over several prior fixes. Reimplementing or forking that logic into a second bulk-deletion code path doubles the surface that has to stay correct for the exact same failure modes this whole workstream exists to prevent. It would also collapse two genuinely separate human decisions — "I've reviewed the evidence and these are safe to consider done" vs. "I'm ready to actually reclaim the disk/branches now" — into one click, which cuts against the very carefulness this entire feature exists to add.
- *Flip status only, and stop there* — chosen. The popup's job ends at making the backlog reachable by the tool that already exists for the next step. The operator runs `Ctrl+R` separately, on their own schedule, exactly as they do today for any other completed task — no new behavior for them to learn, and the two-step shape keeps "mark reviewed" and "actually delete" as two distinct, separately-reversible-up-to-a-point decisions (status flips are trivially reversible via `s`/`S`; a prune is not).
- The cost of this choice: two actions instead of one to fully clear the backlog. Judged acceptable — the backlog is a one-time historical cleanup, not a repeated workflow, so the friction is paid once.

**Decision: the popup is a NEW, globally-scoped view, not a per-coordinator action.**

The stuck-task predicate spans every project and every orchestrator; unlike `C` (clear archive, scoped to the selected coordinator's own hidden subtree), this maintenance action has no natural single-coordinator scope. It's reachable from the Hera page regardless of what's currently selected in the rail.

**Decision: reachable via the Ctrl+K command palette, not a new dedicated keybinding.**

This is a rare, deliberate maintenance action, not a frequent navigation/mutation key — it fits the same shape as other occasional global actions already surfaced through the palette (`context/knowledge/gotchas/keybindings.md`'s "ctrl+k global command palette... per-context action registries"), and avoids allocating a new mnemonic in an already-dense keymap. If a literal keybinding turns out to also be wanted, that's an additive follow-up, not a blocker — and per this repo's CLAUDE.md, would require its own `keymap` entry + help-modal update in the same PR as that addition.

**Decision: classification runs daemon-side, on demand, cached in `task_meta`.**

Mirrors the existing PR-status poller's storage shape (`task_meta` namespace, e.g. `cleanup`, holding the last-computed tier/verdict/reason/timestamp per task) rather than an in-memory cache, so a daemon restart doesn't lose already-paid-for classification work. Unlike the PR-status poller, there is no periodic tick — a `POST /api/maintenance/cleanup-candidates/compute` call (idempotent; a no-op if a computation is already in flight) kicks off one background pass over currently-eligible tasks that don't already have a cached verdict (or whose cache is older than some staleness bound, e.g. re-check anything cached before the classifier itself last changed), grouped by repo for Tier B exactly like the classifier's batch entry point does. `GET /api/maintenance/cleanup-candidates` returns the current cached set plus a `computing: bool` flag; the TUI polls this after triggering compute and renders a spinner meanwhile, then the sectioned list once ready.

**Decision: apply acts on the last-computed snapshot, re-validated at apply time, not on a fresh live classification.**

"What the operator saw is what they approved" — `POST /api/maintenance/cleanup-candidates/apply {scope: "safe"|"all"}` iterates the CACHED result set (not a fresh classification), so the exact set the operator reviewed is the exact set acted on. Before flipping each task's status, it re-checks the task still matches the stuck-task predicate (still archived, still in_review, still no live binding) — cheap, no git/network involved — and skips (never errors) any that no longer qualify, e.g. because the operator or another process already touched it in the meantime.

## Risks / Trade-offs

- **[Risk]** First-ever popup open on a large backlog pays the full classification cost (dozens of Tier B calls) with a visible wait. → **Mitigation**: this is a one-time cost per candidate (cached thereafter), and the popup shows a "scanning N repositories…" state rather than appearing frozen — consistent with treating this as a deliberate maintenance action, not a snappy interactive one.
- **[Risk]** A project deleted from the `projects` table (the `Hera` case) can never be classified past "unresolvable repo," permanently landing its tasks in Needs Review with no path to "Safe" short of a human manually checking GitHub. → **Mitigation**: accepted — this is correct, fail-closed behavior for a project the system can no longer even locate, not a bug to route around; the Needs Review reason text should say so plainly ("project no longer configured") rather than a generic message, so the operator understands why.
- **[Risk]** Caching means a task classified "Needs Review" today because its branch genuinely wasn't merged YET could later actually get merged (e.g. someone finds the old branch and lands it under a new PR) without the cache ever refreshing. → **Mitigation**: a manual refresh action re-runs classification for anything not already "Safe" (Safe is terminal — once merged, always merged, mirroring the PR-status poller's own terminal-state-never-changes assumption); Needs Review is never treated as terminal.

## Migration Plan

Additive: new endpoints, new `task_meta` namespace, new TUI popup. No existing behavior changes. Depends on `add-merge-safety-classifier`; independent of `add-nuke-merge-warning` (can land before or after it).

## Open Questions

- **(Needs Aaron/coordinator sign-off, called out explicitly per the redirect message)** Confirm the "mark complete = status flip only, not deletion" recommendation above, or direct otherwise.
- Is the Ctrl+K-palette-only reachability sufficient, or is a dedicated keybinding also wanted for this specific action?
- Should "Safe" have its own manual re-check action too (in case a Tier A false-not-yet-merged flips to merged later), or is "Safe is terminal" acceptable given it already required direct proof?
