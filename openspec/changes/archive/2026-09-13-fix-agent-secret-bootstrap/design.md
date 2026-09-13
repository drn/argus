## Context

Already diagnosed and decided (see proposal.md's Why) — this is documenting a small, pre-approved fix, not exploring alternatives. Aaron explicitly chose this approach (force-export via `[secrets.op]` bootstrap) over a point-of-use `secret <VAR>` wrapper, because the wrapper would require hardcoding his personal `op://claude/shell-env/<VAR>` convention into argus's shared code, and he's fine with broad vault access reaching every spawned session (removing the least-privilege case for the wrapper).

## Decision

`BuildCmd` resolves `cfg.Secrets.Op.BootstrapSource` unconditionally (independent of any backend's `EnvVars` mapping) via the existing `Resolve(cfg.Secrets, source)` registry call, and force-appends `BootstrapTarget=<value>` to `cmd.Env` when it resolves. This reuses the exact resolution path `opSchemeResolve` already uses for its own self-referential bootstrap — no new resolver, no hardcoded vault/item names, no separate credential path.

Placed alongside the existing forced `TERM`/`COLORTERM`/`GOCACHE`/`PLAYWRIGHT_BROWSERS_PATH` injection in `BuildCmd`, since it is the same kind of unconditional, operator-configured environment force-injection — not a per-backend opt-in like `backend.EnvVars`.

## Alternatives considered

- **Point-of-use `secret <VAR>` wrapper function inside spawned sessions.** Rejected: would require hardcoding Aaron's personal `op://claude/shell-env/<VAR>` path convention into argus's shared code, conflicting with "argus isn't about me and my patterns." Also loses the least-privilege framing since Aaron is fine with broad vault access anyway.

## Risks / trade-offs

- Every spawned session (not just ones with a per-backend `op://` credential mapping) now carries a live, broad-vault-access token in its env once `[secrets.op]` is configured. Accepted: mirrors the existing unconditional force-injection pattern (TERM/GOCACHE), and Aaron explicitly approved the broad-access trade-off.
- No new failure mode: unconfigured or failing bootstrap resolves to a no-op, identical to today's behavior.
