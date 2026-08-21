## Context

macOS gates writes under `~/Library/{Application Support,Containers,Caches}` behind a TCC prompt ("<app> would like to access data from other apps"), attributed to the responsible process — for argus, that's the launchd-rooted `argus`/`argusd` even when a spawned agent's child tool (not argus itself) is the actual writer. This was previously misdiagnosed as purely a binary-signing problem (ad-hoc signatures churning per build, fixed by `make install-signed` — see `context/knowledge/gotchas/sandbox.md`), and that fix is real and necessary, but it doesn't stop *this* class of prompt: two specific tools spawned by agents write into the gated tree by default regardless of how `argus` itself is signed.

`internal/agent/agent.go`'s `BuildCmd` already has exactly this shape of fix in place for an unrelated problem: it force-sets `TERM`/`COLORTERM` on every spawned agent (`agent-execution` capability, "Forced terminal capability environment" requirement) because a launchd-started daemon has no TERM and agents would otherwise render colorless. This change adds two more forced env vars at the same call site, for the same reason (make behavior independent of what a child tool would otherwise pick as a default), but to fix TCC prompt recurrence rather than color rendering.

## Goals / Non-Goals

**Goals:**
- Stop `GOCACHE` and `PLAYWRIGHT_BROWSERS_PATH` writes from landing under `~/Library/{Application Support,Containers,Caches}` for any agent argus spawns, so those two tools stop contributing to TCC prompt volume.
- Keep the fix unconditional and zero-config, matching the existing `TERM`/`COLORTERM` precedent — no new settings surface for something that should always be true.

**Non-Goals:**
- Fixing Chrome's crashpad write to `~/Library/Application Support/Google/Chrome` — structurally not redirectable (`--user-data-dir` is ignored by crashpad; already documented) and comparatively low-cost since it's a one-time grant, not a per-build churn source.
- Fixing the periodic `Argus Code Signing` cert trust lapse — separate, already-tracked issue, unrelated mechanism (keychain ACL/trust, not env vars).
- Migrating or cleaning up existing `~/Library/Caches/go-build` / `~/Library/Caches/ms-playwright` contents.
- Covering every conceivable TCC-triggering cache (e.g. Yarn classic's `~/Library/Caches/Yarn`, Cypress's `~/Library/Caches/Cypress`) — scoped to the two tools with confirmed historical evidence in `context/knowledge/gotchas/sandbox.md`. Widening later is a cheap follow-up at the same call site if new offenders show up.

## Decisions

**Injection point: `BuildCmd`, alongside the existing `TERM`/`COLORTERM` force (`internal/agent/agent.go` ~line 826-829).** Every spawned agent process (regardless of task, worktree, or backend) goes through this one function, and child processes it forks (go build, npx playwright, etc.) inherit its env. Adding the two vars here means every descendant process gets the redirect automatically — no reliance on a per-repo CLAUDE.md convention or an agent remembering to set it per-invocation. Alternative considered: document the env vars as a convention in gotchas/CLAUDE.md for agents to set themselves — rejected because it depends on agent compliance and would only help until the next context compaction or a repo whose CLAUDE.md doesn't mention it; a spawn-time default has no such gap.

**Target paths: `~/.argus/cache/go-build` and `~/.argus/cache/ms-playwright`.** Consistent with argus's existing `~/.argus/` data-dir convention (`CLAUDE.md` "Config & Persistence"). Alternative considered: a generic `/tmp`-based location — rejected because `/tmp` caches are wiped on reboot, defeating the purpose of a build cache (every reboot would force a full go-build cache rebuild) and because `~/.argus/` is already the established location for anything argus-owned on disk.

**Unconditional, no config toggle.** Matches the `TERM`/`COLORTERM` precedent directly above this code — that force is also unconditional. Alternative considered: a `config.toml` opt-out — rejected as unnecessary complexity; there's no scenario where redirecting these two specific caches is undesirable, and CLAUDE.md's breaking-changes policy (single user, no back-compat burden) supports skipping the toggle.

**No cache migration.** The relocated caches simply start empty. Alternative considered: copying `~/Library/Caches/go-build` contents to the new path on first run — rejected as unnecessary engineering for a one-time, self-healing cost (a cold Go build cache costs a few extra minutes once; a migration step adds permanent code and its own edge cases for marginal benefit).

## Risks / Trade-offs

- **First build/test after this ships is slower** (cold `GOCACHE`, cold Playwright browser download) → Mitigation: none needed, it's a one-time cost per machine, explicitly accepted in the proposal.
- **A worktree with disk-space constraints under `~/.argus/`** could see cache growth it didn't before (previously shared under `~/Library/Caches` across all Go tools system-wide, now argus-scoped) → Mitigation: none planned; `~/.argus/` already hosts data/worktrees at comparable or larger scale, and this is consistent with CLAUDE.md's "one user, no back-compat" posture.
- **Doesn't fully eliminate TCC prompts** (Chrome crashpad path remains) → Mitigation: none needed for this change; it's explicitly scoped out and separately low-frequency (one-time per stable signature, not per-build).

## Migration Plan

None required — this is a pure default-env change with no data migration, no schema change, and no rollback complexity beyond reverting the two `cmd.Env` lines (old caches under `~/Library/Caches/*` are untouched and would simply resume being used if reverted).

## Open Questions

None.
