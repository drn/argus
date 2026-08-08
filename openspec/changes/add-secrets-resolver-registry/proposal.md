## Why

Argus's daemon and session-supervisor sometimes need a credential (today, specifically `OP_SERVICE_ACCOUNT_TOKEN`) that must not be hardcoded or committed, and the hand-written LaunchAgent wrapper script standing in for a real resolver is fragile: a live 2026-08-08 incident confirmed it silently drops the credential across three of the four ways the daemon/supervisor process can actually (re)start, because in the default supervisor-mode configuration the real `BuildCmd`/credential-fetch call runs inside the session-supervisor process, not wherever happened to fork it. This change delivers the production secret resolver that `internal/agent/secret.go`'s `SecretResolver` seam (PR #822) was explicitly left open for.

## What Changes

- Replace the wrapper-script approach entirely: the LaunchAgent plist reverts to `internal/launchagent`'s existing generated form (no wrapper, no dependence on which process forks the daemon/supervisor).
- Add URI-scheme-prefixed secret source descriptors (`env://`, `keychain://`, `op://`) dispatched through a small, config-driven resolver registry; a bare string with no `://` keeps resolving exactly as today (implicit `env://`) for full backward compatibility.
- Resolve a secret fresh, at the point of use, in whichever process actually needs it (daemon or session-supervisor) — never via `os.Setenv`-based ambient injection or reliance on process-environment inheritance across forks.
- Memoize a successful resolve for the life of the process, keyed by source descriptor; never memoize a failed resolve, so a transient `op`/Keychain blip can succeed on the next attempt.
- The `op` resolver bootstraps its own credential (e.g. `OP_SERVICE_ACCOUNT_TOKEN`) through a configurable `[secrets.op].bootstrap_source`, resolved via the *same* registry `Resolve` function — no special-cased "how op authenticates" code path.
- Keep the existing fail-open philosophy (unresolved source → target left unset, warning logs only the variable name, never blocks daemon/supervisor startup) but make failure newly visible: an `argus doctor` diagnostic and a Settings → System row report RESOLVED / NOT RESOLVED / NOT CONFIGURED for the `[secrets.op]` bootstrap source.
- Migration: add a `[secrets]` config block pointing at the existing Keychain item, revert the plist via the existing `Install()` path, delete `~/.argus/argusd-launcher.sh`.

## Capabilities

### New Capabilities

- `secrets-resolution`: the resolver registry itself — URI-scheme source-descriptor parsing and dispatch across the `env`/`keychain`/`op` schemes, the `op` resolver's self-referential bootstrap resolve, process-lifetime success-only memoization, and the fail-open/visibility contract (RESOLVED/NOT RESOLVED/NOT CONFIGURED tri-state) that `agent-execution`, `binary-coherence`, and `settings-view` each consume. This is genuinely distinct machinery — external-store dispatch and caching, not "turning a task into a running agent" — and the codebase already has precedent for splitting a consumed resolution subsystem into its own capability: `diligence-profiles` owns all profile-resolution semantics as its own capability even though `agent-execution` only *consumes* the result (exporting `ARGUS_PROFILE`/`ARGUS_ARCHETYPE`/`ARGUS_MODEL`), never re-implementing resolution itself. Folding the registry into `agent-execution` would also strand its `argus doctor`/Settings visibility, which per that same precedent (see below) belongs in the capabilities that own those surfaces, not in `agent-execution`.

### Modified Capabilities

- `agent-execution`: the "Per-backend credential environment mapping" requirement changes from resolving only bare strings via `os.LookupEnv` to dispatching every source descriptor (bare string or scheme-prefixed) through the new `secrets-resolution` registry. Bare-string behavior is unchanged (implicit `env://`), so this is additive, not breaking.
- `config-management`: needs a new requirement for the `[secrets]` / `[secrets.op]` TOML block (fields, and the default-when-absent behavior of a pure no-op equivalent to today). This follows the existing pattern in this spec of documenting a config section's schema and default resolution as its own requirement (e.g. "Coordinator context budget configuration", "Worker context window configuration") rather than treating config-management as too generic to enumerate fields — it already enumerates fields extensively.
- `binary-coherence`: needs a new `argus doctor` diagnostic section for the `[secrets.op]` bootstrap resolve. Confirmed via the existing spec text that `argus doctor`'s advisory checks are documented in `binary-coherence` regardless of which subsystem they check — both the "Stop-hook registration diagnostic" and the "Diligence-profile library diagnostic" are requirements owned by `binary-coherence`, not by `coordinator-context-management` or `diligence-profiles` respectively. The new secrets check follows that same established placement, each printed as its own independent section that never alters the doctor exit-code contract.
- `settings-view`: needs a new System-category row surfacing the same RESOLVED/NOT RESOLVED/NOT CONFIGURED tri-state. Precedented by the existing "Install default profile seeds from the Hera settings category" requirement, which shows `settings-view` already hosts capability-specific advisory/action rows sourced from other capabilities.

`os-integration` was checked and does **not** need a delta: its "Plist content contract" and "LaunchAgent installation" requirements describe `internal/launchagent.Install()`/`renderPlist()` exactly as they exist today, unmodified by this change. The Migration Plan's plist revert is calling that existing, already-spec'd path again (via `argus daemon install` or the Settings auto-start-at-login toggle) — an operational step, not a behavior change to any requirement in that spec.

## Impact

- `internal/agent/secret.go` — the existing `SecretResolver` seam (its origin PR is closed/unmerged under any number we could find; the seam itself is real and working on `origin/master` today, verified directly) gains a companion `internal/agent/secretregistry.go` with the scheme-dispatch registry, `keychain`/`op` resolvers, and memoization cache.
- `internal/agent/agent.go`'s `BuildCmd` — the `backend.EnvVars` resolution loop gains scheme-prefixed dispatch through the new registry (built fresh from the `cfg` parameter it already receives), while a bare-string source keeps resolving through the existing swappable `secretResolver` var unchanged.
- `internal/agent/runner.go`'s `Start` (the sole `BuildCmd` caller) — unchanged call site.
- `internal/daemon` (`buildSupervisorStartCmd` / `autoStartSupervisorFork`) and `internal/daemon/client/autostart_fork.go` (`AutoStart` / `autoStartFork`) — the two daemon/TUI-owned respawn paths identified as silently dropping the credential; fixed structurally by resolving at point-of-use inside the session-supervisor rather than depending on env inheritance from whichever process forked it.
- `internal/launchagent/launchagent_darwin.go` (`Install()` / `renderPlist()`) — unchanged; re-invoked as part of migration to drop the wrapper.
- `~/.argus/argusd-launcher.sh` — deleted; no longer referenced by the plist or anything else.
- New config schema: `[secrets]` / `[secrets.op]` in `config.toml`.
- New surfaces: an `argus doctor` diagnostic section, and a Settings → System row, both reporting the `[secrets.op]` bootstrap tri-state.
