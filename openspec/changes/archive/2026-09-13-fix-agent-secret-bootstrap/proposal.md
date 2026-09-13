## Why

Aaron gets repeated 1Password desktop-integration authorization prompts ("Allow argusd to get CLI access") after every reboot. Root cause: `internal/agent/agent.go`'s `BuildCmd` spawns every agent session via `exec.Command("sh", "-c", cmdStr)` — a non-interactive shell that never sources `~/.zshrc` and never gets `OP_SERVICE_ACCOUNT_TOKEN`. When an agent inside a spawned session needs a secret via `op read op://...`, `op` has no service-account token and falls back to interactive desktop auth, which expires after 10 min idle or on 1Password lock (always locked after reboot) — hence the recurring prompt.

## What Changes

- `BuildCmd` now force-exports the resolved `[secrets.op]` bootstrap credential into every spawned session's `cmd.Env`, mirroring the existing forced `TERM`/`COLORTERM`/`GOCACHE`/`PLAYWRIGHT_BROWSERS_PATH` injection. It resolves `cfg.Secrets.Op.BootstrapSource` through the existing `Resolve()` registry (`internal/agent/secretregistry.go`) — the SAME function the `op://` scheme's own self-referential bootstrap already uses — and, on success, appends `BootstrapTarget=<value>` to the child env.
- No-ops safely when `[secrets.op]` is unconfigured (`BootstrapSource == ""`): no behavior change for operators without it configured.
- The resolved credential is never logged, printed, or persisted — matching the existing discipline for every other secret resolve in this package.

## Capabilities

### Modified Capabilities

- `agent-execution`: adds a new requirement describing the forced op-bootstrap env export, alongside the existing forced-terminal-capability and forced-cache-redirect requirements.

## Impact

- **Modified code:** `internal/agent/agent.go` (`BuildCmd`).
- **New tests:** `internal/agent/secret_test.go` — asserts the bootstrap target lands in `cmd.Env` when bootstrap resolves, and is absent when `[secrets.op]` is unconfigured.
- **Security note:** every spawned agent session's env now carries a resolved, broad-vault-access secrets-bootstrap credential when `[secrets.op]` is configured — same trust model as the existing per-backend `EnvVars` credential mapping, just force-injected rather than opt-in per backend.
- Specs are LOCAL DOCS only (`openspec/project.md`) — no CI/Make/Go-build wiring. Quality gate stays `make pre-pr`.
