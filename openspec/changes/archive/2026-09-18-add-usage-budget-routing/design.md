## Context

`internal/agent.ResolveBackend(task, cfg)` resolves a task's backend with precedence `task.Backend → project.Backend → cfg.Defaults.Backend`, with no awareness of hera at all. Two call sites decide what `task.Backend` ends up being for a hera-spawned role, and both currently pass the backend straight through with no intermediate tier:

- `internal/mcp/hera.go: toolHeraSpawnWorker` forwards `p.Backend` (the MCP caller's explicit arg, `""` if omitted) into `HeraSpawnInput.Backend`.
- `internal/heragater/heragater.go: materializeNode` (the plan-DAG gater's leaf-worker path) calls `w.materialize(node, taskPrompt, project, branch, "", "")` — it **always** passes an empty backend today; there is no existing per-node backend override at all. The sibling `materializeSubCoord` branch (line 525-528) is structurally separate and untouched by this change — sub-coordinators are out of scope by design (confirmed with Aaron: only worker/freelance, never any coordinator, root or nested).

There is no existing "usage" or "quota" concept anywhere in the codebase (`cost-estimation` is a different concern — per-role token/cost *accrual* from Stop-hook transcripts, not an account-wide weekly cap). We proved today that Claude Code's own `/usage` slash command is reachable non-interactively: spawning a headless session with prompt `/usage` renders the real account-level weekly-cap percentage and reset timestamp into the session's PTY log, at effectively zero token cost (`Total cost: $0.0000` — it's a client-side render, not a model turn).

The daemon already runs a comparable periodic background job — `runPRPoller` (`internal/daemon/daemon.go`, started via `go d.runPRPoller()`) — as the precedent for "poll something external on a timer, cache the result, never block the hot path on it."

## Goals / Non-Goals

**Goals:**
- Let Aaron flip a config switch so hera worker/freelance spawns default to a fallback backend (Codex) either always, or automatically once his weekly Claude usage crosses a configured threshold, reverting automatically at the next weekly reset.
- Keep the check on the spawn hot path synchronous and cheap (a cache read, never a live probe).
- Fail open: any probe/parse/staleness failure behaves exactly like the switch being off.

**Non-Goals:**
- Coordinator or sub-coordinator backend selection — confirmed out of scope; those always keep their existing resolution.
- A TUI/web/macOS settings surface. This is a `config.toml`-only switch for v1 (mirrors `cfg.Hera.Enabled` / `cfg.Supervisor.Enabled` precedent, neither of which has a dedicated UI toggle either). Per CLAUDE.md's Frontend Parity rule this is a **named** non-goal, not silent — a UI toggle is a reasonable future follow-up, not implied by this change.
- A general cost/budget dashboard — that's `cost-estimation`'s territory, not this change's.
- Multi-operator usage tracking — this reads one account's weekly cap (Aaron's, via the daemon's own local Claude CLI session). If the daemon is ever used on behalf of more than one person, this feature's premise no longer holds; out of scope to address here.
- Guaranteeing the cap is never exceeded — this is best-effort budget *steering*, not a hard enforcement mechanism.

## Decisions

1. **New package `internal/usagebudget`** owns the probe, the cache, and the single resolution function `ResolveWorkerBackend(explicit string) (backend string)` that both call sites use. Centralizing here means `toolHeraSpawnWorker` and `heragater.materializeNode` each change by one line, and the generic `ResolveBackend`/task-creation path is untouched — non-hera tasks are structurally unaffected.
2. **Probe mechanism**: reuse the proven headless-`/usage`-session technique, but as a lightweight one-shot PTY subprocess (mirroring how `internal/agent` already shells out to backend CLIs) rather than a full `agent.CreateAndStart` argus task — no worktree, no DB task row, no rail visibility. This avoids littering the task list with a probe task every cadence tick (we did the manual version as a real task earlier today purely for ad hoc investigation; that is not the production mechanism).
3. **Cadence**: a background goroutine started alongside `runPRPoller` at daemon startup, on a configurable interval (default ~30 min). Never probed synchronously inside a spawn call — a spawn always reads the cache, even if stale.
4. **Cache**: in-memory only (percentage + reset timestamp + last-probed-at), not persisted to `data.sql`. A daemon restart just means the first ~30 minutes post-restart fall back to "unknown → switch inactive" until the next tick repopulates it — self-healing, and simpler than adding a persistence path for a value that's only ever advisory.
5. **Config location**: global `~/.argus/config.toml`, new `[hera.worker_budget]` table (not per-project) — usage is one account-wide resource shared across every project's spawns, so a per-project setting would be misleading.
6. **Threshold + manual switch share one effect, two independent activation paths** (`enabled` bool for "always route to fallback" + `threshold_pct` int, `0` = disabled, for "route to fallback only while cached usage% ≥ threshold"). Both, when active, select the same `fallback_backend` (default `"codex"`).
7. **Reset handling**: no separate timer/expiry logic — the cached reset timestamp is exactly what `/usage` itself reports. Once real time passes that timestamp, the *next* probe naturally reads a lower percentage and the threshold check falls below `threshold_pct` on its own. No new state machine needed.
8. **Fail-open contract**: `ResolveWorkerBackend` returns `explicit` unchanged whenever `explicit != ""` (explicit always wins, first line of the function, no cache read needed). When `explicit == ""`: return `""` (defer to normal precedence) unless the switch is genuinely active (enabled, or threshold set AND a non-stale cached reading is at/above it) AND `fallback_backend` resolves to a real configured backend — any missing/invalid config, unset cache, or parse failure returns `""`.

## Risks / Trade-offs

- **[Risk]** `/usage`'s rendered output is a scraped TUI frame, fragile to Claude Code version/format drift. → **Mitigation**: parse defensively (never panic on unexpected shape), treat any parse miss as "unknown" (fail open), log via `uxlog` so drift is visible without ever blocking a spawn — mirrors the existing `argus doctor`-style "advisory, never gates behavior" pattern already used elsewhere in this codebase (e.g. the diligence-profile-library check).
- **[Risk]** Cadence lag — a burst of spawns can cross the real cap before the next probe tick updates a stale cached percentage. → **Mitigation**: this is explicitly best-effort steering, not a hard guarantee (see Non-Goals); default cadence is tunable via config.
- **[Risk]** Even a lightweight subprocess probe has real overhead (process start, PTY, a live Claude CLI invocation) run repeatedly forever. → **Mitigation**: default 30-minute cadence, configurable; zero token cost per the confirmed `/usage` render behavior.
- **[Risk]** Single-operator assumption baked into the design (see Non-Goals) means this silently stops making sense if the daemon is ever shared. → **Mitigation**: none needed now; explicitly out of scope, called out here rather than silently assumed.

## Open Questions

- Exact probe subprocess shape (bare `exec.Command` + PTY vs. some other lightweight mechanism `internal/agent` already exposes) — left to implementation; prefer whatever avoids a full task/worktree if it's not disproportionately more complex than just using one.
- Whether `fallback_backend` should be validated against `cfg.Backends` at config-load time (fail loud) vs. at resolve time (fail open, same as every other miss) — leaning resolve-time fail-open for consistency with decision 8, but worth confirming during review.
