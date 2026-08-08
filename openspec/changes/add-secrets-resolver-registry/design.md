## Context

Argus's daemon, session-supervisor, and spawned agent processes sometimes need a
credential that must not be hardcoded or committed — today, specifically
`OP_SERVICE_ACCOUNT_TOKEN`, so a downstream `op read` call can fetch other
secrets (e.g. a per-backend API key, or a Socket Firewall registry password
needed by an agent working in another project).

Argus already has one piece of this designed: `internal/agent/secret.go`'s
`SecretResolver` seam, landed in PR #822. A backend definition carries an
`EnvVars` mapping (target env var → source descriptor), and `BuildCmd` resolves
each source through a pluggable resolver function before injecting it into a
spawned agent's environment. The default resolver is `os.LookupEnv` against the
daemon's own process environment. The PR's own doc comment named the production
follow-up explicitly and left it undone: "a production deployment can swap in a
resolver that shells out to the `op` (1Password) CLI... That production
resolver, and how the daemon authenticates to 1Password without a plaintext key
on disk, is a separate follow-up." This change is that follow-up.

**Why now:** a live incident (2026-08-08) made the gap concrete. A hand-written
LaunchAgent wrapper script (`~/.argus/argusd-launcher.sh`) was introduced to
fetch `OP_SERVICE_ACCOUNT_TOKEN` from macOS Keychain and export it before
exec'ing the daemon binary, with the plist's `ProgramArguments` pointed at the
wrapper. This works only when launchd itself starts the daemon. Argus has two
*other* ways the daemon/supervisor process gets (re)started that bypass the
wrapper entirely:

1. The TUI's own auto-reconnect (`dclient.AutoStart` → `autoStartFork`,
   `internal/daemon/client/autostart_fork.go`) forks the raw binary via
   `exec.Command` with no explicit `Env`, inheriting whatever environment the
   TUI process itself has. It fires automatically, sub-second, on any daemon
   disconnect.
2. The daemon's own "supervisor socket is silent, spawn a fresh one" logic
   (`buildSupervisorStartCmd` / `autoStartSupervisorFork`, same package) forks
   the raw binary the same way, inheriting the daemon's own environment.

Because paths 2 and the TUI's path bypass the wrapper, any daemon/supervisor
hiccup silently drops the credential — confirmed live: two independent restart
attempts (an agent inside argus, then a manual restart) both left the
credential missing, and the only thing that actually worked was forcibly
routing through `launchctl kickstart` while the TUI (which races to
auto-respawn) was closed. Separately, `internal/launchagent.Install()`
generates the LaunchAgent plist from a Go template that points
`ProgramArguments[0]` directly at the daemon binary — the hand-written wrapper
is out-of-band with this generated artifact, and re-running `argus daemon
install` (or toggling auto-start-at-login in Settings) silently reverts to the
un-wrapped binary, dropping the credential a fourth way.

**Key architectural finding:** `BuildCmd` (which resolves the `EnvVars`
mapping) is called from `internal/agent/runner.go`'s `Start`. In supervisor
mode (`cfg.Supervisor.Enabled`, default ON), the real `*Runner` — and therefore
the real `BuildCmd`/`exec.Command` call — runs **inside the session-supervisor
process**, not the daemon. The daemon just forwards RPCs to it. Any design that
assumes the daemon is where secret resolution happens is wrong for the default
configuration.

## Goals / Non-Goals

**Goals:**

- Replace the wrapper-script approach entirely: no LaunchAgent wrapper, no
  reliance on which process happens to fork the daemon/supervisor. The plist
  goes back to pointing directly at the daemon binary, matching
  `internal/launchagent`'s generated contract.
- A secret source descriptor is self-describing (URI-scheme prefixed) and
  resolves through a small, config-driven resolver registry — not hardcoded to
  any one person's Keychain item names or 1Password vault/item paths.
- Existing behavior (bare-string sources resolving against the process
  environment) keeps working unchanged — this is additive, not breaking, for
  the existing `backends.env_vars` seed data and any user-customized mappings.
- A secret is resolved fresh, at the point of use, in whichever process
  actually needs it — never assumed to have propagated via process-environment
  inheritance from some other process.
- A misconfigured or unreachable secret source fails open (matches the
  existing per-backend resolver philosophy) but is visible somewhere a human
  actually looks (`argus doctor`, Settings), not just a log line.

**Non-Goals:**

- No multi-user or remote secret store. This is single-machine, single-user,
  matching the project's existing Breaking Changes Policy scope.
- No credential rotation, expiry, or refresh policy — a resolver either
  resolves right now or it doesn't; that's the whole contract.
- Not building a general plugin system for arbitrary third-party resolvers in
  this change — three built-in schemes (`env`, `keychain`, `op`) cover the
  known need. The registry is structured so a fourth scheme is a small,
  additive change later, but that's not required now.
- Not touching how `op` itself authenticates beyond feeding it
  `OP_SERVICE_ACCOUNT_TOKEN` — no interactive `op signin` flow, no biometric
  unlock handling.

## Decisions

### Decision: URI-scheme-prefixed source descriptors, dispatched through a small resolver registry

A source descriptor like `keychain://op-service-account-claude` or
`op://claude/shell-env/HERA_OPENAI` names its own resolver via a scheme prefix.
A bare string with no `://` (today's only format) is treated as `env://` for
full backward compatibility. This mirrors the established pattern for this
problem space (Vault secret engines, Kubernetes external-secrets-operator
`SecretStore` providers, sops key providers, direnv) — the descriptor is
self-describing, so different secrets can mix resolver types freely with no
global switch, and config only supplies resolver-specific *parameters* (which
Keychain, which `op` invocation), never "the" resolver for the whole system.

**Alternative considered:** a single global `[secrets] resolver = "op"` setting
with descriptors staying bare strings. Simpler, but can't express "bootstrap
via Keychain, everything else via `op`" without a special case, and any future
second resolver type reopens the same problem. Rejected.

### Decision: Resolve at point of use, never rely on env-inheritance propagation

A shared resolve helper is called directly by whatever code needs a secret
(specifically: the `op` resolver, when it needs its own bootstrap credential),
scoping the resolved value to that one `exec.Command`'s `cmd.Env` rather than
the calling process's ambient environment. Nothing calls `os.Setenv` on its own
process environment.

This is deliberately the opposite of the wrapper-script model. Because the
real `BuildCmd`/`op read` invocation runs inside the session-supervisor process
in the default configuration, an "inject into my own env at startup" model
(Approach A, considered and rejected) only actually protects the path where
the *daemon* is what forks the supervisor — one of several ways the supervisor
can come up, not a guarantee. Resolving fresh at the point of use means the
supervisor (or daemon, if supervisor mode is ever disabled) fetches its own
Keychain-backed credential directly, in Go, on demand, regardless of process
ancestry — which is exactly the class of assumption that caused the live
incident this change is fixing.

A successful resolve is memoized in-process (keyed by source descriptor) so a
busy supervisor spawning many agents doesn't hit Keychain/`op` on every single
spawn. A *failed* resolve is not memoized, so a transient failure (e.g. a
`op` CLI network blip) can succeed on the next attempt rather than being
poisoned for the process's lifetime.

**Alternative considered:** ambient injection via `os.Setenv` at daemon startup
(Approach A), relying on `exec.Command`'s default env inheritance to propagate
it to the supervisor and then to agents. Rejected — it doesn't hold in the
default supervisor-mode configuration, and it spreads a sensitive value across
a whole long-lived process's environment indefinitely rather than scoping it
to the one subprocess invocation that needs it.

### Decision: `op` resolver's own bootstrap is just another resolve, not a special code path

`[secrets.op].bootstrap_source` can be any scheme (typically `keychain://...`,
but nothing stops it from being `env://` for a user who already has
`OP_SERVICE_ACCOUNT_TOKEN` ambiently available, e.g. in a signed-in interactive
shell). The `op` resolver calls the *same* registry `Resolve` function to fetch
its own bootstrap credential — there's no separate "how does op auth" code
path, which keeps the registry the single source of truth and makes adding a
future fourth scheme (e.g. a file-based one) automatically available as a
bootstrap option too, for free.

### Decision: Fail-open, but visible via `argus doctor` and Settings

Matches the existing per-backend resolver philosophy (`agent-execution`'s
"Per-backend credential environment mapping": unresolved source → target left
unset, warning logged naming only the variable). A misconfigured or
unreachable `op`/`keychain` source must never prevent the daemon or supervisor
from starting — argus itself must stay usable even if 1Password is signed out
or a Keychain item was renamed. But a purely-log-line failure is exactly what
made today's incident take hours to diagnose, so this change adds one
advisory check (same shape as the existing diligence-profile-library check):
if `[secrets.op]` is configured, do one resolve-and-discard of
`bootstrap_source` at startup and record RESOLVED / NOT RESOLVED / NOT
CONFIGURED, surfaced in `argus doctor`'s output and a Settings → System row.

## Risks / Trade-offs

- **[Risk]** A resolver shells out to `security` or `op` — both are
  synchronous subprocess calls on whatever goroutine first needs a secret
  (e.g. the first agent spawn after a supervisor restart) → **[Mitigation]**
  memoization means this cost is paid once per process lifetime per source,
  not per spawn; a slow/hung `op` call is bounded by a context timeout on the
  resolver's own `exec.CommandContext` call (matches the existing pattern in
  `internal/gitutil` for external command calls with a deadline).
- **[Risk]** Removing the wrapper script changes an already-working (if
  fragile) setup → **[Mitigation]** this change explicitly re-runs
  `internal/launchagent.Install()`'s existing plist-generation path (already
  spec'd, unmodified) once the config-driven resolver covers the same need, so
  the net behavior for a correctly-configured `[secrets]` block is a strict
  improvement, not a regression; the wrapper script and its plist edit are
  removed as part of this change's migration.
- **[Risk]** A resolver source descriptor is logged in error paths for
  debugging → **[Mitigation]** matches the existing invariant from
  `secret.go`'s doc comment: only the descriptor (never a resolved value) is
  ever logged; this change doesn't loosen that.

## Migration Plan

1. Land the resolver registry + config schema + `op`/`keychain` resolvers +
   doctor/Settings visibility (this change).
2. Aaron adds a `[secrets]` block to his own `config.toml` pointing at his
   existing Keychain item (`op-service-account-claude`), matching his current
   setup but now config-driven instead of hardcoded into a wrapper script.
3. Revert the plist to `internal/launchagent`'s generated form (drop the
   wrapper): re-run `argus daemon install` (or toggle auto-start-at-login off
   then on in Settings, which calls the same `Install()` path) so the plist's
   `ProgramArguments[0]` points at the daemon binary directly again.
4. Delete `~/.argus/argusd-launcher.sh` (no longer referenced by anything).
5. Rollback: if the new resolver misbehaves, the old wrapper-script plist can
   be restored by hand and `[secrets]` left empty (the `env` resolver's
   behavior is unchanged, so an empty `[secrets]` block is a no-op — full
   backward compatibility, not a hard cutover).

## Open Questions

None outstanding — resolved during brainstorm: source-descriptor format
(URI-scheme), resolution model (point-of-use, not ambient), and failure
visibility (fail-open + doctor/Settings) were each confirmed with the user.

## Alternatives considered

Captured inline under each Decision above (Approach A ambient-injection vs
Approach B point-of-use; global-resolver-setting vs scheme-prefixed
descriptors).

## Discovery findings

- `internal/launchagent.Install()`/`renderPlist()` (`internal/launchagent/launchagent_darwin.go`)
  generates the LaunchAgent plist from a Go template pointing directly at the
  daemon binary — confirmed via the `os-integration` base spec's "Plist
  content contract" and "LaunchAgent installation" requirements. The
  hand-written wrapper script is entirely out-of-band with this.
- `BuildCmd`'s only caller is `internal/agent/runner.go`'s `Start` — confirmed
  via grep. In supervisor mode this executes inside the session-supervisor
  process, not the daemon, which is the basis for the point-of-use resolution
  decision above.
- The existing `SecretResolver` seam (`internal/agent/secret.go`, PR #822) is
  already spec'd under `agent-execution`'s "Per-backend credential environment
  mapping" requirement and already deliberately designed to be swapped without
  touching `BuildCmd` — this change fulfills that seam's own stated intent
  rather than introducing a new one.
- A related-sounding symptom (`secret BUNDLE_PACKAGES__THANX__COM` failing
  inside an argus-spawned Thanx sandbox) led to a detour building
  [thanx/sketch#160](https://github.com/thanx/sketch/pull/160) (Socket
  Firewall proxy routing for Sketch's own CI/Docker package installs) — a
  legitimate, wanted, already-merged change for Sketch's own infrastructure,
  but unrelated to argus's local daemon credential bootstrap. Ruled out as
  prior art for this change; left in place, not reverted.

## Acceptance criteria

**Resolver registry & dispatch:**
- it should resolve a bare string source (no `://`) against the process
  environment, unchanged from today's `envSecretResolver` behavior
- it should resolve a `keychain://<service>` source via `security
  find-generic-password -s <service> -w`
- it should resolve a `keychain://<service>/<account>` source via `security
  find-generic-password -s <service> -a <account> -w`
- it should resolve an `op://<vault>/<item>/<field>` source by first resolving
  `[secrets.op].bootstrap_source` into `[secrets.op].bootstrap_target`, then
  running `op read op://<vault>/<item>/<field>` with that credential set only
  in the subprocess's own environment
- it should memoize a successful resolve for a given source for the life of
  the process, and not memoize a failed resolve

**Backward compatibility:**
- it should leave a backend's existing bare-string `EnvVars` mapping working
  exactly as before, with no config changes required

**Failure visibility:**
- it should leave the target environment variable unset and log a warning
  naming only the variable (never a value) when a source fails to resolve,
  and this should not prevent the daemon or supervisor from starting
- it should report RESOLVED / NOT RESOLVED / NOT CONFIGURED for the
  `[secrets.op]` bootstrap source in `argus doctor`'s output
- it should surface the same tri-state in a Settings → System row

**Migration:**
- it should work correctly with an empty/absent `[secrets]` block (pure
  backward compatibility, equivalent to today)
- it should work correctly when the daemon is started via the
  `internal/launchagent`-generated plist with no wrapper script involved
