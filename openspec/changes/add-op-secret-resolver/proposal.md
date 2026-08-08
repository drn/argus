# Add a configurable op (1Password) secret resolver

## Why

`add-foreign-backend-envmap` (PR #822, merged to `drn/master` 2026-06-28) gave
`agent.BuildCmd` a per-backend credential environment mapping (`EnvVars`:
TARGET env var -> SOURCE descriptor) resolved through a pluggable
`secretResolver` seam. It shipped exactly one resolver: `envSecretResolver`,
a thin `os.LookupEnv` passthrough. That PR's own doc comments and proposal
explicitly deferred the production resolver:

> The production resolver is an `op`/1Password-CLI resolver that shells out at
> spawn time — e.g. `op read op://claude/shell-env/HERA_OPENAI` — and drops
> into the existing `agent.SetSecretResolver` seam without touching `BuildCmd`.
> [...] is a SEPARATE story, out of scope for this change.

This is that follow-up: a second resolver mode, selectable via config, that
resolves an `EnvVars` source by shelling out to `op read`. It must work for
any argus user's own 1Password layout — nothing about one person's vault,
account, or item naming is hardcoded — and it must be safe to leave
unconfigured: an argus install that never touches the new config keys behaves
exactly as it does today.

## What Changes

- **`config.Config` gains a `Secrets SecretsConfig` field** (`[secrets]` in
  `config.toml`, config.toml-only — no DB table, no Settings UI, mirroring
  `Keybindings`). `SecretsConfig.Resolver` selects `"env"` (default, unchanged
  behavior) or `"op"`. `SecretsConfig.Op` holds `ReferenceTemplate`, `Command`,
  and `TimeoutSeconds` — all user-supplied, none defaulted to a real
  vault/account/item.
- **`agent.BuildCmd` gains a resolver-selection step** before its existing
  `EnvVars` merge loop: given the live `cfg.Secrets` it already receives as a
  parameter, it picks the plain env resolver (today's behavior, and the
  existing `SetSecretResolver` test seam) or builds an op-backed resolver from
  `cfg.Secrets.Op`. No change to the `SecretResolver` function signature, and
  no change to the existing per-target unresolved-source warning contract.
- **The op resolver shells out to `op read --no-newline <reference>`** under a
  bounded timeout, substituting the literal token `{source}` in
  `ReferenceTemplate` with the `EnvVars` mapping's source descriptor. A failed
  or timed-out read is treated exactly like today's unresolved-source case
  (target left unset, non-sensitive warning) — see design.md D-FAIL for why
  this does not "fail louder."
- **Degrades to the existing `os.LookupEnv` behavior** whenever `op` isn't a
  fit for this install: `resolver` unset/`"env"`/unrecognized, or `resolver =
  "op"` with an empty `reference_template`, or the configured `op` command
  isn't resolvable. No install is forced to adopt 1Password to keep working.
- **Docs**: README `[backends.<name>]`-adjacent Reference table entry for
  `[secrets]` / `[secrets.op]`, plus a gotcha note per this repo's
  documentation requirements.

## Non-Goals (see design.md for the full list and rationale)

Most importantly: **bootstrapping the argus daemon's own process environment
with whatever credential lets `op` authenticate non-interactively** (e.g. an
`OP_SERVICE_ACCOUNT_TOKEN`-shaped variable) is explicitly out of scope. That is
host/OS-specific one-time operator setup (launchd `EnvironmentVariables` on
macOS, systemd `Environment=` on Linux, container env injection, ...) — every
operator running the daemon as a background service faces the identical need
regardless of which secrets backend they pick, so it is not a problem argus
config can solve generically. Argus's contract stops at: "the resolver assumes
whatever `op` itself needs to authenticate is already present in the daemon's
own process environment" — documented as a precondition, not solved in code.

## Impact

- Affected specs: `agent-execution` (resolver-selection + op-resolver
  behavior), `config-management` (new `[secrets]` default configuration +
  validation/fail-open semantics).
- Affected code (implementation, NOT part of this proposal — see tasks.md for
  the follow-up execution plan): `internal/config/config.go`
  (`SecretsConfig`/`OpResolverConfig` + `DefaultConfig`), `internal/agent/secret.go`
  (resolver-selection function + op-resolver implementation), `internal/agent/agent.go`
  (`BuildCmd`'s resolver-selection call site), `README.md` (Reference table),
  `context/knowledge/gotchas/misc.md` (gotcha note).
- No secret value ever enters the DB, logs, test fixtures, or git — only the
  mapping (target -> source descriptor) and the operator's own reference
  template/command/timeout are config, never a credential.
- This proposal ships NO implementation code and does NOT run `openspec
  archive` — per this repo's CLAUDE.md, behavioral changes require approval
  before implementation begins.
