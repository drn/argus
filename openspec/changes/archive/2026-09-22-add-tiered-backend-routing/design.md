## Context

Today's usage-aware routing (`[hera.worker_budget]`) is a one-shot special case: one capped backend (Claude), one fallback (Codex, config-named), one call site family (hera worker/freelance spawn). The request is to generalize this into an ordered chain of N tiers, each independently capped or uncapped, consulted by *default* task-backend resolution generally — plus a real Settings UI, since hand-editing `config.toml` was an explicitly named non-goal last time and the user now wants it reversed.

Three technical facts anchor the design:

1. **Claude usage** is already solved: a headless `claude -- /usage` PTY probe, rendered and regexed (`internal/usagebudget/usagebudget.go`). Reused as-is.
2. **Codex usage** has no clean live API, but Codex's CLI already writes `rate_limits.used_percent` (plus `window_minutes`, `resets_in_seconds`) into `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl` as a side effect of *any* normal Codex session — for free, no dedicated probe cost. This repo already reads Codex's local state for an unrelated purpose (`CaptureCodexSessionID` reads `~/.codex/state_5.sqlite`), so reading another local Codex artifact is consistent with existing practice, not a new kind of coupling.
3. There is **no reorderable-list UI pattern anywhere in the TUI today** (checked keymap editor, cache_dirs editor, sandbox multi-value fields — all either fixed-shape or read-only-in-TUI). The tier-list editor is genuinely new UI, not a reuse.

## Goals / Non-Goals

**Goals**
- An ordered, arbitrary-length tier list drives the *default* backend for new tasks (task/project explicit backend still always wins, unchanged).
- Each tier is tagged with a probe kind: `claude_usage`, `codex_usage`, or `none` (uncapped — e.g. the existing local `pi`/ollama backend). New probe kinds are addable later (e.g. a hypothetical `gemini_usage`) without redesigning the resolver — the probe registry is keyed by kind, not hardcoded to two backends.
- Configurable via `config.toml` (scriptable, shareable) **and** a TUI Settings panel (discoverable, no file editing required) — with a single, unambiguous precedence rule when both could apply.
- Fail-open at every layer: an unprobeable or misconfigured tier is skipped, never blocks task creation, and a fully broken configuration degrades to today's single-`cfg.Defaults.Backend` behavior.

**Non-Goals**
- Touching `[hera.worker_budget]` / `usagebudget.ResolveWorkerBackend` / hera worker or freelance spawn's own resolution call sites. That feature is live and depended on (Aaron, Slack, 2026-09-18: "finally got around to making argus auto-switch between codex and claude... my coordinator will remain claude only"). Reusing the *Claude probe code* is fine; changing *hera's* resolution behavior is out of scope. A follow-up to let hera opt into the new generic resolver instead of its bespoke one is a reasonable future change, not this one.
- Web SPA or macOS Settings surface. TUI + config.toml only, exactly mirroring the `[hera.worker_budget]` v1 scoping (see `openspec/changes/archive/2026-09-18-add-usage-budget-routing/design.md`'s Non-Goals section for the precedent this repeats).
- Adding a `gemini` backend/command-template. The tier list can reference any backend name already in `cfg.Backends`; adding Gemini's own command template is unrelated follow-up work, and `llm-backends`' existing spec already states "No `gemini` backend SHALL be added" for the credential-mapping change — this change keeps that boundary.
- Per-project tier lists. Usage is an account-wide resource; the tier list is global, like the existing hera budget config.
- A cost/spend dashboard. That's `cost-estimation`'s territory (already shipped, separate).
- Coordinator/sub-coordinator routing of any kind (unchanged, permanently out of scope per the original design's stated rationale — the coordinator is "the thing I live in").

## Decisions

### Decision: config.toml and DB storage coexist, config.toml wins wholesale

Every other structured setting in this repo (backend command templates, per-project overrides) follows `DefaultConfig() < DB (Settings menu) < config.toml`. The tier list follows the same rule: if `[backend_routing]` with at least one `[[backend_routing.tier]]` is present in `config.toml`, that list is authoritative and the Settings UI renders it read-only (same UX as `catBackends`' existing "(command is hardcoded)" hint). If `config.toml` has no `[backend_routing]` table, the Settings UI reads/writes a new DB table and the list is fully editable there. No merging of the two sources — whichever governs, governs entirely, avoiding "config.toml sets tiers 1-2, DB sets tier 3" ambiguity.

**Alternative rejected:** merge config.toml tiers with DB tiers (e.g. config.toml entries first, DB entries appended). Rejected for matching no existing precedent in this codebase and adding a second precedence axis (source-per-tier) for no clear benefit at v1 scale.

### Decision: probe kinds are hardcoded per-provider, dispatched by a kind string, not by backend name

A tier's `probe` field is one of `"claude_usage"`, `"codex_usage"`, `"none"` — deliberately not tied 1:1 to the tier's `backend` field, even though today `claude_usage` will only ever pair with the `claude` backend. It's fine, and preferred, for each kind's implementation to be hardcoded, provider-specific Go code (a PTY probe shaped around Claude's exact `/usage` rendering, a rollout-file reader shaped around Codex's exact JSONL schema) — there is no requirement for a generic `Prober` plugin system or runtime registration. The only thing kept generic is the *dispatch*: the resolver's core loop (`for each tier: if usagePct(tier.probe) < tier.threshold_pct { return tier.backend }`) switches on the kind string rather than hardcoding "check Claude, then check Codex," so a future kind (the user named Gemini as an example) is a new `case` in that switch plus one new, fully hardcoded probe function — not a resolver rewrite, and not a new abstraction layer.

**Alternative rejected:** a generic `Prober` interface with dynamic registration (`RegisterProbe(kind, prober)`), so third parties or later code could plug in a probe without touching the switch. Rejected per explicit direction — hardcoding each provider's probe is acceptable and simpler; "flexible to add providers" means small, contained, localized changes (new case + new function), not a plugin architecture.

### Decision: Codex probe prefers the free rollout-file read, falls back to a costed PTY probe

Primary: find the most recent `~/.codex/sessions/**/rollout-*.jsonl`, tail-scan for the last JSON record carrying a `rate_limits` object, read `used_percent`. Cheap, no subprocess, matches the existing local-file-reading precedent (`CaptureCodexSessionID`). Treat a rollout file older than the probe's `CacheMaxAge` (mirroring the Claude probe's 1-hour staleness window) as stale.
Fallback (only when no fresh rollout file exists — e.g. Codex hasn't run recently enough for this account, or the account is Codex-only and Argus itself is the only Codex driver): spawn a headless `codex` PTY session, send a minimal message, read the rendered `/status` output the same way the Claude probe reads `/usage`. This costs a small amount of real Codex usage on every such probe tick, so it only fires as a fallback, on the same ~30 min cadence as the existing probe goroutine, and is itself fail-open (probe error → stale/unknown, never blocks resolution).

**Open question for implementation:** OpenAI's own `/status` output format is tracked as incomplete upstream (openai/codex#15281 was open as of this writing) — the PTY-fallback parser must be defensive and versioned the same way `usagebudget`'s Claude parser is (treat any unexpected shape as a parse failure, not a panic), and may need revisiting if Codex's CLI output changes.

### Decision: new package reuses, not forks, the existing Claude probe's cache/fail-open shape — not a shared interface

Rather than duplicating `internal/usagebudget`'s Claude PTY-probe code, the new Codex prober copies its cache-and-fail-open *shape* (a mutex-guarded `{pct, resetAt, lastProbedAt}` struct, `CacheMaxAge` staleness check, log-and-leave-unchanged on error) as plain, separate, hardcoded Go code in the new `internal/backendtier` package — not a shared interface both types implement. The resolver's dispatch (previous decision) calls each kind's own function directly (`usagebudget.CachedClaudePct()`, `backendtier.CachedCodexPct()`), switched on the kind string. `usagebudget.ResolveWorkerBackend` and its underlying Claude cache are untouched; the new package only reads from it, it does not wrap it in an abstraction.

### Decision: reorderable list gets new, minimal TUI affordances

No existing widget reorders rows. Minimum viable pattern for this change: within the tier-list category, `n` adds a tier (prompts backend name via existing dropdown/cycle pattern, then probe kind, then threshold if capped), `d`/`x` removes the selected tier, and `K`/`J` (matching the Hera rail's existing step-up/step-down convention for reordering-adjacent operations) move the selected tier up/down one position. This is scoped as new, contained code in `internal/tui/settings.go`, not a generic reusable "list editor" abstraction — no second caller exists yet to justify one.

## Risks / Trade-offs

- **Codex PTY-probe fallback spends real Codex budget to check Codex budget.** Mitigated by preferring the free rollout-file read and keeping the fallback on a long (~30 min) cadence, but it's a real, named trade-off, not eliminated.
- **Upstream Codex `/status` format is unstable** (tracked in an open OpenAI issue). The parser must degrade to "unknown" rather than misreport, exactly like the existing Claude parser's contract.
- **Two storage backends for one concept** (config.toml vs DB) adds a small amount of conceptual overhead, but it's the existing pattern every other structured setting in this repo already follows — introducing a different rule here would be the actual inconsistency.
- **New TUI list-editing code has no precedent to lean on** — expect this to be the largest single chunk of implementation and test surface in the change.
