## 1. Spec

- [x] 1.1 Scaffold change folder (proposal.md, design.md, delta spec, tasks.md).

## 2. Implementation

- [x] 2.1 `BuildCmd` (`internal/agent/agent.go`): resolve `cfg.Secrets.Op.BootstrapSource` via `Resolve(cfg.Secrets, source)` and force-append `BootstrapTarget=<value>` to `cmd.Env` when it resolves, alongside the existing forced TERM/COLORTERM/GOCACHE/PLAYWRIGHT_BROWSERS_PATH injection.
- [x] 2.2 Guard: no-op when `BootstrapSource == ""`; never log/persist the resolved value.

## 3. Tests

- [x] 3.1 `internal/agent/secret_test.go`: `TestBuildCmd_OpBootstrap_ForceExportedWhenConfigured` — bootstrap target lands in `cmd.Env` when configured and resolves.
- [x] 3.2 `internal/agent/secret_test.go`: `TestBuildCmd_OpBootstrap_AbsentWhenUnconfigured` — no env entry and no subprocess invocation when `[secrets.op]` is unconfigured.

## 4. Docs

- [x] 4.1 Gotcha bullet in `context/knowledge/gotchas/misc.md` (security-relevant: every spawned session now carries a resolved secrets-bootstrap token in its runtime env).
- [x] 4.2 Update `context/knowledge/index.md` coverage cell.

## 5. Verify

- [x] 5.1 `make pre-pr` clean.

## 6. Archive

- [x] 6.1 Merge the delta into `openspec/specs/agent-execution/spec.md` and move the change folder to `openspec/changes/archive/<date>-fix-agent-secret-bootstrap/`, committed on the change branch before merge.
