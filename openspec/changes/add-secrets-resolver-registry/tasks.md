**Design doc:** `openspec/changes/add-secrets-resolver-registry/design.md`

**Migration note (not a task below):** design.md's Migration Plan steps 2-5 (Aaron adding a `[secrets]` block to his real `~/.argus/config.toml`, re-running `argus daemon install`/toggling auto-start to revert the plist, and deleting `~/.argus/argusd-launcher.sh`) are manual, host-machine operator steps performed by Aaron *after* this change ships — they touch files outside this repo/worktree and are out of scope for an implementation agent to execute. Tracked here so they aren't silently dropped; not checkboxed because they aren't part of this PR.

## 1. Tests

Write failing tests for every scenario below (Prove-It Pattern — each must fail for the right reason, i.e. compile-fail against not-yet-written registry/resolver code or fail against today's `os.LookupEnv`-only behavior, before any Stage 2+ code is written). Use `internal/testutil` assertions throughout; table tests via `t.Run`; no test touches the real `~/.argus/`, a live daemon, real Keychain, or a real `op`/`security` binary — resolver internals get an injectable command-runner seam specifically so these are fakeable (function-field injection per `context/knowledge/testing.md`, mirroring `internal/gitutil`'s external-command pattern).

- [ ] 1.1 `internal/agent/secret_test.go` (extend): bare-string source still resolves via `env://` semantics unchanged (existing behavior, regression guard) — from `secrets-resolution`'s "Bare string resolves as env" and `agent-execution`'s "Mapping carries no secret value"
- [ ] 1.2 `internal/agent/secretregistry_test.go` (new): scheme-dispatch table test — bare string ⇒ `env://`, explicit `env://SOME_VAR`, and an unrecognized scheme fails closed (not-ok, no error/panic) — from `secrets-resolution`'s "Scheme-prefixed secret source descriptor dispatch"
- [ ] 1.3 `internal/agent/secretregistry_test.go`: keychain resolver — service-only (`security find-generic-password -s <service> -w`) and service+account (`-a <account>`) invocations via a fake command runner asserting the exact args; non-zero exit or empty stdout fails to resolve — from "Keychain resolver"
- [ ] 1.4 `internal/agent/secretregistry_test.go`: op resolver — successful `op read op://<vault>/<item>/<field>` with `[secrets.op].bootstrap_target` set only in that subprocess's env (never via `os.Setenv` on the test process); bootstrap source resolved via a `keychain://...` descriptor through the *same* `Resolve` function (assert no separate code path — e.g. a fake registry entry proves reuse); missing/absent `[secrets.op]` or a failing `bootstrap_source` fails the `op://` resolve — from "op (1Password) resolver with self-referential bootstrap" (all 4 scenarios)
- [ ] 1.5 `internal/agent/secretregistry_test.go`: memoization — a call-counting fake resolver proves a second resolve of the same descriptor is served from cache with zero additional invocations; a failed resolve is retried (not poisoned) on the next attempt — from "Process-lifetime success-only memoization" (both scenarios)
- [ ] 1.6 `internal/agent/secretregistry_test.go`: op bootstrap tri-state status query — RESOLVED (configured + resolves), NOT RESOLVED (configured + fails), NOT CONFIGURED (`[secrets.op]` absent or `bootstrap_source` empty) — from "op bootstrap resolution status tri-state" (all 3 scenarios)
- [ ] 1.7 `internal/agent/secret_test.go` (extend): `BuildCmd` dispatches a `keychain://`- or `op://`-prefixed `EnvVars` source through the registry rather than treating it as a bare env-var name — from `agent-execution`'s "Scheme-prefixed source dispatches through the registry"
- [ ] 1.8 `internal/agent/secret_test.go` (extend): swapping the installed resolver via `SetSecretResolver` changes what the *very next* `BuildCmd` call resolves, with no process-identity assumption baked in — proves resolution is fresh-at-point-of-use, not cached at the `BuildCmd` layer — from "Resolution happens in whichever process builds the command" and "Resolver is pluggable"
- [ ] 1.9 `internal/config/config_test.go` (extend): TOML round-trip for an absent `[secrets]` block (zero-value `Config` behaves as a no-op — no `op://` source can resolve, bare/`env://` unaffected) and for a populated `[secrets.op]` with `bootstrap_source`/`bootstrap_target`, including `bootstrap_source` set to an `env://`-prefixed value with no special-casing — from `config-management`'s "Secrets resolver configuration block" (all 3 scenarios)
- [ ] 1.10 `internal/doctor/secretsstatus_test.go` (new): pure `Diagnose`-style function tests for the RESOLVED/NOT RESOLVED/NOT CONFIGURED tri-state rendering, mirroring `profilelib_test.go`'s split between the pure classifier and its rendered text — from `binary-coherence`'s "Secrets bootstrap diagnostic" (first 3 scenarios)
- [ ] 1.11 `cmd/argus/doctor_test.go` (extend): a `gatherSecretsBootstrapStatus`-style wrapper test proves the doctor command's exit-code contract is unaffected by a NOT RESOLVED secrets status when binary-coherence itself is Healthy — from "Check does not change the exit-code contract"
- [ ] 1.12 `internal/tui/settings_test.go` (extend): the System category's rows include the secrets tri-state row reflecting RESOLVED / NOT RESOLVED / NOT CONFIGURED for each of the three input states — from `settings-view`'s "Secrets bootstrap status row in System category" (all 3 scenarios)
- [ ] 1.13 Confirm every acceptance criterion in `design.md` and every scenario across all 5 delta spec files maps to a failing test written above before starting Stage 2

## 2. Resolver registry core: scheme dispatch, env resolver, keychain resolver, memoization

**Depends on:** Stage 1

- [ ] 2.1 Add a source-descriptor parser to `internal/agent` (e.g. `internal/agent/secretregistry.go`) that splits on `://`, defaulting to `env` when absent, and a small resolver registry (`map[string]func(descriptor string) (string, bool)` or equivalent) dispatching by scheme; an unrecognized scheme returns not-ok, never an error
- [ ] 2.2 Wire the existing `envSecretResolver` (`internal/agent/secret.go`) into the registry under the `env` scheme unchanged
- [ ] 2.3 Implement the `keychain` resolver: shell out to `security find-generic-password -s <service> [-a <account>] -w` via `exec.CommandContext` with a bounded timeout (mirroring `internal/gitutil`'s pattern for external commands with a deadline); parse `keychain://<service>` vs `keychain://<service>/<account>`; non-zero exit or empty stdout ⇒ not-ok. Structure the actual command invocation behind a small injectable function field so tests never shell out to real `security`
- [ ] 2.4 Add process-lifetime memoization in front of the registry's `Resolve` entry point: a `sync.Map` or mutex-guarded map keyed by the exact descriptor string, caching only successful resolves; a failed resolve is never stored, so the next call re-invokes the underlying resolver
- [ ] 2.5 `make test-pkg PKG=./internal/agent/` — confirm Stage 1's Tasks 1.1-1.3 and 1.5 (env/keychain/memoization slices) now pass; op-resolver and tri-state tests remain red pending Stage 3

## 3. op resolver, bootstrap indirection, and `[secrets]`/`[secrets.op]` config schema

**Depends on:** Stage 1, Stage 2

- [ ] 3.1 Add `Secrets`/`SecretsConfig` and nested `OpConfig` (or equivalent naming) to `internal/config/config.go`'s top-level `Config` struct, following the existing nested-table convention (see `HeraConfig`/`SupervisorConfig`): `[secrets.op]` carries `bootstrap_source string` (`toml:"bootstrap_source"`) and `bootstrap_target string` (`toml:"bootstrap_target"`); document the absent-block-is-a-no-op contract in a doc comment
- [ ] 3.2 Implement the `op` resolver in `internal/agent/secretregistry.go`: given an `op://<vault>/<item>/<field>` descriptor, resolve `[secrets.op].bootstrap_source` through the *same* registry `Resolve` function (no special-cased bootstrap code path), and if that fails, fail the `op://` resolve immediately; otherwise run `op read op://<vault>/<item>/<field>` via `exec.CommandContext` with the resolved bootstrap value set under `[secrets.op].bootstrap_target` **only** in that subprocess's own `cmd.Env` (built from a copy of the ambient env, never `os.Setenv` on the calling process), with the same bounded-timeout pattern as the keychain resolver
- [ ] 3.3 Give the registry constructor access to the resolved `config.Config` (or just the `SecretsConfig`/`OpConfig` slice it needs) so the `op` resolver can read `bootstrap_source`/`bootstrap_target` at call time — thread it through however the registry is constructed (e.g. a `NewRegistry(cfg config.Config) *Registry` or a config-accessor function field), not as a global
- [ ] 3.4 Implement the op bootstrap tri-state status query (RESOLVED / NOT RESOLVED / NOT CONFIGURED) as an exported function on the registry that does one resolve-and-discard of `bootstrap_source`, returning NOT CONFIGURED when `[secrets.op]` is absent or `bootstrap_source` is empty, distinctly from a configured-but-failing NOT RESOLVED — this is the single implementation `binary-coherence` and `settings-view` will each call in Stages 5-6
- [ ] 3.5 `make test-pkg PKG=./internal/agent/` and `make test-pkg PKG=./internal/config/` — confirm all of Stage 1's registry, op-resolver, and config-schema tests (1.4, 1.6, 1.9) now pass

## 4. Wire `agent.SetSecretResolver` at daemon and supervisor startup

**Depends on:** Stage 3

- [ ] 4.1 In `cmd/argus/main.go`'s `runDaemon()` (in-process/legacy path, `cfg.Supervisor.Enabled == false`) and `runSupervisor()` (the default, out-of-process session-supervisor entrypoint — the process where `BuildCmd`/`op read` actually runs per design.md's key finding), construct the Stage 2/3 registry from the loaded `config.Config` and call `agent.SetSecretResolver` with its `Resolve` method before the runner starts accepting sessions
- [ ] 4.2 Confirm (by reading, not by re-deriving) that `BuildCmd`'s only caller (`internal/agent/runner.go`'s `Start`) needs no change — the pluggable `secretResolver` var it already calls now does real scheme-dispatch work once Stage 4.1 installs the registry
- [ ] 4.3 `internal/agent/secret_test.go` / integration-level test: with the registry installed via `SetSecretResolver`, a `keychain://`- or `op://`-prefixed `EnvVars` mapping resolves end-to-end through `BuildCmd` (extends Stage 1.7 from a unit-level fake to the real registry wiring) — from `agent-execution`'s "Resolution happens in whichever process builds the command"
- [ ] 4.4 `make test-pkg PKG=./internal/agent/` and `make test-pkg PKG=./cmd/argus/` — confirm green

## 5. `argus doctor` secrets bootstrap diagnostic

**Depends on:** Stage 3

- [ ] 5.1 Add `internal/doctor/secretsstatus.go` mirroring `profilelib.go`'s shape: a `SecretsBootstrapStatus` enum (Resolved / NotResolved / NotConfigured) and a pure `RenderSecretsBootstrap(status) string` function producing its own printed section (independent of the binary-coherence table/verdict, the Stop-hook section, and the profile-library section)
- [ ] 5.2 In `cmd/argus/doctor.go`, add a `gatherSecretsBootstrapStatus()` that loads config and calls Stage 3.4's tri-state query, and add `fmt.Print(doctor.RenderSecretsBootstrap(gatherSecretsBootstrapStatus()))` to `runDoctor()` — after the existing profile-library line, still **before** the `Diagnose(actors).Verdict` exit-code check, and without that check reading the new status at all
- [ ] 5.3 `internal/doctor/secretsstatus_test.go` and `cmd/argus/doctor_test.go` — confirm Stage 1.10-1.11 now pass, including the exit-code-unaffected assertion

## 6. Settings System row for secrets bootstrap status

**Depends on:** Stage 3

- [ ] 6.1 In `internal/tui/settings.go`'s `catSystem` branch of `rebuildRows`, append a new row (new `settingsRow` kind or a `srWarning`-style read-only row, matching the existing "System status"/warning row pattern) surfacing the Stage 3.4 tri-state, computed the same way `argus doctor` computes it (one resolve-and-discard of `bootstrap_source`)
- [ ] 6.2 Wire whatever data the row needs into `SettingsView` construction/refresh (mirroring how `sv.warnings`, `sv.heraEnabled`, etc. are populated in `NewSettingsView`/refresh paths) — remote mode (`sv.remote`) should not attempt a local resolve; treat it the same way other daemon-local rows are gated
- [ ] 6.3 `internal/tui/settings_test.go` — confirm Stage 1.12's three-state row assertions pass

## 7. Documentation and gotchas

**Depends on:** Stages 4, 5, 6

- [ ] 7.1 Add a gotcha bullet to `context/knowledge/gotchas/daemon-rpc.md` (or `misc.md` if it reads more as a config/DB pattern than a daemon-lifecycle one) covering: the resolver-registry-resolves-at-point-of-use-not-ambient-injection invariant, why the wiring lives in both `runDaemon()` and `runSupervisor()` (BuildCmd runs inside whichever one is actually active per `cfg.Supervisor.Enabled`), the op-resolver's self-referential bootstrap (no special "how op authenticates" path), and the success-only memoization semantics — per CLAUDE.md's "every new feature documents its non-obvious gotchas" rule
- [ ] 7.2 Update `context/knowledge/index.md`'s coverage-bullet cell for whichever gotcha file gained the entry (per that file's existing convention of listing coverage inline in the index table)
- [ ] 7.3 Add `uxlog` calls for resolve failures at the point `BuildCmd` leaves a target variable unset (naming only the variable, never a value) and for the doctor/Settings tri-state computation's failure path, per CLAUDE.md's Logging Requirements
- [ ] 7.4 Update the README Reference appendix if a new Settings row or `argus doctor` section warrants a table entry (factual-change bar per CLAUDE.md — likely yes for the doctor output table)

## 8. Verification

**Depends on:** Stage 7

- [ ] 8.1 Run `make pre-pr` (build → vet → fmt-check → lint-pr → vuln → test-cover-gate) and confirm it passes clean — non-negotiable before opening/updating the PR per CLAUDE.md
- [ ] 8.2 Run `make test-cover` and confirm touched packages (`internal/agent`, `internal/config`, `internal/doctor`, `cmd/argus`, `internal/tui`) sit at ≥95% (≥90% acceptable for the `internal/tui` settings-row smoke-only slice), per `context/knowledge/testing.md`

## 9. Archive the OpenSpec change

**Depends on:** Stage 8

- [ ] 9.1 Run `openspec archive add-secrets-resolver-registry` (or, if the CLI is unavailable, apply it by hand: merge each delta spec's requirements into the corresponding base spec under `openspec/specs/<capability>/spec.md` — creating `openspec/specs/secrets-resolution/spec.md` new, and updating `agent-execution`, `config-management`, `binary-coherence`, `settings-view` in place — then move the change folder to `openspec/changes/archive/<YYYY-MM-DD>-add-secrets-resolver-registry/`)
- [ ] 9.2 Confirm `openspec validate --all --strict` passes after archiving
- [ ] 9.3 Commit the archived specs and moved change folder on the same PR branch, before merge — per this project's CLAUDE.md archiving rule (never a post-merge step)
