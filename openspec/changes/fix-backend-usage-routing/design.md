## Context

`[backend_routing]` (add-tiered-backend-routing, fix-backend-routing-semantics) resolves a task's default backend by walking an ordered tier list, each tier capped by a cached usage probe. In production on Aaron's machine (config: `claude` @ 90%, then `codex` @ 80%) it has never routed a task to Codex. Hera coordinators are hard-pinned to Claude by `resolveCoordinatorBackend` and stay that way; this change concerns every other task (workers, freelance, plain tasks).

## Discovery findings

Ground truth gathered 2026-10-07:

1. **Claude probe never completes.** `runClaudeUsageProbe` starts `claude -- /usage` in a PTY and calls `cmd.Wait()`. That command opens an interactive session that never exits, so every probe hits the 45s timeout and the function returns `nil, ctx.Err()` — discarding even a fully rendered screen. Reproduced via a one-off test.
2. **Claude probe is blocked by startup dialogs.** Live PTY captures showed the session parked on modal dialogs before `/usage` ran: from the argus repo, an "Allow external CLAUDE.md file imports?" dialog; from `$HOME`, the "Is this a project you trust?" folder-trust dialog. The daemon's cwd (launchd) is equally untrusted.
3. **Claude probe failures are invisible.** `internal/usagebudget` logs via `uxlog`, which only the TUI process initializes. `ux.log` and `daemon.log` contain zero `[usagebudget]` lines ever. (`codexprobe.go` already hit this and uses `slog.Warn`.)
4. **Parser expects one-line layout.** `parseUsageOutput` requires "current week", "all models", and the percentage on the same line; tests use invented fixtures only. Real `/usage` renders a header line with the bar/percentage on following lines — must be confirmed against a captured fixture during implementation.
5. **No non-interactive alternative.** Per Claude Code docs, `/usage` is interactive-only and no hook input carries rate limits; only the statusLine stdin JSON has `rate_limits.seven_day`, which argus can't rely on (users own their statusline).
6. **Codex weekly window is `primary`, not `secondary`.** Real rollouts (plan `self_serve_business_prolite`) carry `primary: {used_percent, window_minutes: 10080, resets_at}` and `secondary: null`.
7. **Codex depleted state has no numbers.** After exhaustion, records read `primary: null, secondary: null, rate_limit_reached_type: "workspace_owner_credits_depleted"`. Today that parses as "no reading".
8. **Codex freshness window is too short.** A rollout is trusted only if its mtime is < 1h old; Codex wasn't run since Oct 4, so every tick logs "found nothing fresh". But every Codex use on this machine writes a new rollout, so the newest rollout's reading remains the best available until its own `resets_at`.
9. **Probes only run after the first 30-minute tick**, so the first half hour after every daemon restart is always unknown.

## Goals / Non-Goals

**Goals:**

- Both probes produce real weekly readings on a stock install, and failures are visible in `daemon.log`.
- A `headroom` strategy that spreads new tasks across backends by remaining room under each tier's threshold.
- Unknown readings can no longer silently steer all work in headroom mode.

**Non-Goals:**

- Routing on 5-hour windows (decision: weekly only; Codex's `worstCodexWindow` changes to weekly-only accordingly).
- Changing the coordinator Claude pin.
- A Settings UI / REST / web / macOS surface for `strategy` — config.toml-only v1, mirroring `codex_pty_fallback_enabled`. Named follow-up: expose `strategy` in Settings alongside the tier editor.
- Multi-account probing (default account only, unchanged).
- Reading the statusline JSON.

## Decisions

### D1. Claude probe streams the screen and stops on first parse

Feed PTY bytes into the emulator as they arrive; after each chunk, attempt `parseUsageOutput` on the rendered screen. On success, store the reading and terminate the process (SIGTERM, then kill). Timeout keeps the 45s bound and is logged. *Alternative:* send `/exit` and wait — still depends on the session reaching the prompt; streaming is strictly more robust.

Implementation notes (found while implementing Stage 3): the screen is evaluated only between frames, because Claude Code wraps every frame in a synchronized update (DEC mode 2026) and mid-frame screens mix old and new content. A screen that still shows "Refreshing…" is also skipped: `/usage` first paints Claude Code's locally cached figures (50% in the captured fixture) and then repaints with the fetched ones (51%).

### D2. Dedicated probe directory with targeted trust-dialog answer

The probe runs with `cmd.Dir = ~/.argus/usage-probe` (created empty, owned by argus; no CLAUDE.md → no imports dialog). If the rendered screen shows the folder-trust dialog **and** names that exact directory path, the probe selects the "trust" option once. Claude persists trust per path, so this happens at most once per machine. The answer (Down, short pause, Enter) is sent only after the dialog has sat with no new output for 1.5s. In a live test, a Down sent right after the first paint was undone by Claude Code's next repaint, so the Enter that followed picked "No, exit". The trust option is located by its label ("Yes, I trust this folder") relative to the `❯` cursor row, and the probe sends that many Down (or Up) arrows before Enter; if the label isn't on screen, the dialog is treated as blocking. Any other modal → logged as "probe blocked by dialog: <first line>" and the probe aborts (fail-open). *Alternatives:* writing `hasTrustDialogAccepted` into `~/.claude.json` (mutates the user's Claude config — rejected); blind keystrokes (could accept an unrelated dialog — rejected).

Detection limit: a dialog is recognized only by its shape (a `❯` cursor row, at least one further option row, and an "Enter to confirm" footer). A modal without that footer is not detected as a dialog at all; the probe simply never sees the weekly reading and the run surfaces as a probe timeout — still fail-open, just logged as a timeout rather than "blocked by dialog".

### D3. Probe logs via slog

Both probes log success (percentage, reset) and every failure with `slog`, matching `codexprobe.go`, so lines land in `daemon.log`. `uxlog` calls are kept alongside for TUI-side parity.

### D4. Probe once at daemon startup

Both pollers run one probe immediately on start, then every 30 minutes.

### D5. Codex weekly window by duration; depleted = 100%; trust until reset

Select the window whose `window_minutes == 10080` (either `primary` or `secondary`). If `rate_limit_reached_type` is non-null and no weekly window is present, record 100% with `resets_at` unknown — held 24h from the rollout file's mtime (not the probe time, so repeated probes of the same file cannot extend it); a newer rollout replaces it. Otherwise the reading is trusted until `resets_at` (no mtime window). After `resets_at` passes, the reading is unknown.

### D6. `strategy` setting and headroom resolution

`[backend_routing].strategy`: `"ordered"` (default; unchanged semantics) or `"headroom"`. Unrecognized value → logged once, treated as `ordered`.

Headroom algorithm over valid tiers (backend exists in roster, known probe kind):

1. **Known candidates:** capped tiers with a fresh reading and `pct < threshold`. Score = `threshold − pct`. Pick highest score; ties → earlier in list.
2. If none: **unknown candidates** — capped tiers with no/stale reading, plus `none`-probe tiers. Pick the first in list order.
3. If none: return `""` (caller falls back to `defaults.backend`).

`none`-probe tiers are treated as "no reading" so they never outrank a backend with known headroom. *Alternative considered:* unknown = full headroom (today's fail-open — the bug), unknown = skip (both probes broken → everything lands on `defaults.backend` silently). See Alternatives.

## Alternatives considered

- **Codex-first ordering** — config-only, but exhausts Codex early in the week and was undercut by Codex itself being depleted on Oct 4.
- **Weighted split** (fixed ratio) — ignores actual usage.
- **Statusline JSON as Claude source** — reliable data but depends on each user's statusline config.
- **Separate 5h/weekly thresholds** — more config; rejected for weekly-only.

## Acceptance criteria

Claude probe:

- it should store a reading as soon as the weekly percentage renders, without waiting for the process to exit
- it should parse the percentage when it appears on a line after the "Current week (all models)" header
- it should answer the folder-trust dialog only when it names the probe directory
- it should abort and log when blocked by any other dialog
- it should log success and failure lines that reach the daemon log
- it should run once at daemon startup

Codex probe:

- it should read the weekly window by its 10080-minute duration regardless of primary/secondary slot
- it should record 100% when the record reports a reached limit with no window data
- it should keep a reading valid until its resets_at even if the rollout is days old
- it should treat a reading as unknown once resets_at has passed

Resolver:

- it should keep ordered behavior when strategy is absent
- it should pick the tier with the most headroom in headroom mode
- it should prefer a known-under-threshold tier over an unknown tier
- it should fall back to list order among unknown tiers when no known tier qualifies
- it should exclude over-threshold tiers
- it should return empty when every tier is over threshold
- it should treat an unrecognized strategy as ordered

## Risks / Trade-offs

- [Claude Code changes the trust dialog or `/usage` layout] → probe fails open with a visible daemon.log line; fixture tests pin the current layout.
- [Probe spawns a real Claude session every 30 min] → unchanged from today's intent; early-stop cuts its lifetime from 45s to a few seconds.
- [Codex depleted reading with unknown reset] → held 24h then refreshed by any new rollout; worst case Codex looks full for a day after a reset with no Codex use. Headroom mode then simply prefers Claude.
- [Headroom drifts workers onto Claude while Codex is fuller] → by design; Aaron reserves coordinator room via Claude's lower threshold.

## Migration Plan

No data migration. To adopt: add `strategy = "headroom"` under `[backend_routing]` in config.toml and lower Claude's `threshold_pct` to the desired coordinator reserve. Rollback: remove the key.

## Open Questions

None blocking. The exact `/usage` rendering is captured as a fixture in task 1.
