## Why

Claude Code (not Argus) deletes session transcripts and other local state older than `cleanupPeriodDays` (default **30 days**, minimum 1) at every `claude` process startup, sweeping globally across all of `~/.claude` regardless of which project or tool launched that process. Any Argus task left untouched past that window fails to resume: `claude --resume <id>` prints its own `No conversation found with session ID: <uuid>` and exits 1, which Argus today captures and surfaces identically to a generic crash — the task just lands in `InReview` with no explanation. There is no way to scope retention to Argus-created sessions only (confirmed via research: no per-launch override, no `CLAUDE_CONFIG_DIR`-style isolation without duplicating auth/plugins/MCP config), so the only real fix is raising `cleanupPeriodDays` in `~/.claude/settings.json` — but nothing in Argus today tells the user this is even the cause, let alone that the setting exists.

This mirrors two problems the codebase has already solved once each: `argus doctor`'s Stop-hook / profile-library / secrets-bootstrap sections are exactly this shape of advisory, external-file diagnostic (read `~/.claude/settings.json` or `~/.argus/...`, classify a small state, print the fix — never gating the exit code), and the secrets-bootstrap tri-state is already mirrored live in Settings → System. This change applies that same established pattern to `cleanupPeriodDays`, plus adds a distinct, actionable message when a resume failure is caused specifically by a swept transcript (today indistinguishable from any other crash).

## What Changes

- A new shared query, `agent.QueryClaudeCleanupPeriodDays()`, reads `~/.claude/settings.json` and returns the effective `cleanupPeriodDays` (`nil` when the key is absent, i.e. the 30-day default applies) alongside any read/parse error — mirroring `agent.QueryOpBootstrapStatus`'s shape, so both `argus doctor` and the Settings TUI can call the same primitive.
- `argus doctor` gains a fourth independent advisory section: **Claude session retention** — **OK** (explicitly raised above 30), **LOW** (unset or ≤30, prints the fix snippet), or **UNKNOWN** (`~/.claude/settings.json` unreadable/unparseable). Purely advisory; never changes the exit-code contract.
- **Settings → System** gets a new row mirroring the same OK / LOW / UNKNOWN tri-state, with a detail pane showing the current effective value and the JSON snippet to raise it — same shape as the existing Secrets Bootstrap row.
- A resumed session whose last output contains Claude Code's exact `No conversation found with session ID:` signature is classified distinctly from a generic crash. The TUI surfaces a status-bar notice explaining the transcript was likely swept by Claude Code's own retention sweep (not an Argus bug) and pointing at Settings → System / `argus doctor`, instead of leaving the task in `InReview` with no explanation.
- README: new paragraphs under "Diagnosing binary skew (`argus doctor`)" documenting the new check, and a mention alongside the existing "Claude Code session retention" section (added in a prior, non-behavioral doc-only edit) that Settings now mirrors it live.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `agent-execution`: two additions — (1) `agent.QueryClaudeCleanupPeriodDays()`, a new query reading `~/.claude/settings.json`'s `cleanupPeriodDays`, following the existing "Per-backend credential environment mapping" / secrets-bootstrap precedent of this capability owning small external-config queries consumed by `binary-coherence` and `settings-view`; (2) a new pure classifier recognizing Claude Code's `No conversation found with session ID:` resume-failure signature in a session's last output, distinct from the existing "Process exit notification and last output" requirement's generic error/output capture.
- `binary-coherence`: `argus doctor` adds a Claude-session-retention diagnostic section (OK / LOW / UNKNOWN) — same shape and same non-gating contract as the existing Stop-hook, profile-library, and secrets-bootstrap sections in this capability.
- `settings-view`: the System category gains a row mirroring the same OK / LOW / UNKNOWN tri-state, precedented directly by the existing Secrets Bootstrap status row (also sourced from `binary-coherence`'s sibling check and computed via the same underlying query as `argus doctor`).
- `tui-shell`: the status-bar transient-notice mechanism (`SetError`/`SetInfo`, `StatusNoticeTTL`) gains a new triggering condition — a resume failure matching the retention signature sets an explanatory info/error notice instead of surfacing no message at all.

## Impact

- `internal/agent/` — new query function (new file or alongside `secret.go`) reading `~/.claude/settings.json`; new pure classifier for the resume-failure signature.
- `internal/doctor/` — new `CleanupPeriodStatus` enum + `DiagnoseCleanupPeriod` + `RenderCleanupPeriod`, mirroring `secretsstatus.go`.
- `cmd/argus/doctor.go` — new `gatherCleanupPeriodStatus()` wired into `runDoctor()` alongside the existing three advisory `fmt.Print` calls.
- `internal/tui/settings.go` — new `settingsRowKind`, a `SettingsView` field populated in `Refresh()`, a row in `catSystem`'s `rebuildRows`, and a `renderClaudeRetentionDetail` mirroring `renderSecretsBootstrapDetail`.
- `internal/tui/app.go` — `handleSessionExitUI` (and its two callers, `NotifySessionExit` and `HandleSessionExit`, both of which already have the last-output bytes in scope) gains the resume-failure signature check and a status-bar notice call on match.
- `README.md` — extends the existing "Diagnosing binary skew" section and the already-landed "Claude Code session retention" section with one sentence each documenting the new doctor check / Settings mirror.
- Tests: `internal/agent` (query + classifier), `internal/doctor` (status classification), `cmd/argus` (gather step), `internal/tui` (settings row + exit-notice trigger), following each area's existing test shape.
- No changes to the `argus doctor` exit-code contract, no new config schema, no new DB columns or migrations.
