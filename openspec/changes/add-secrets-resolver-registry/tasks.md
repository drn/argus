**Design doc:** `openspec/changes/add-secrets-resolver-registry/design.md`

**Migration note (not a task below):** design.md's Migration Plan steps 2-5 (Aaron adding a `[secrets]` block to his real `~/.argus/config.toml`, re-running `argus daemon install`/toggling auto-start to revert the plist, and deleting `~/.argus/argusd-launcher.sh`) are manual, host-machine operator steps performed by Aaron *after* this change ships — they touch files outside this repo/worktree and are out of scope for an implementation agent to execute. Tracked here so they aren't silently dropped; not checkboxed because they aren't part of this PR.

## 1. Tests

Write failing tests for every scenario below (Prove-It Pattern — each must fail for the right reason, i.e. compile-fail against not-yet-written registry/resolver code or fail against today's `os.LookupEnv`-only behavior, before any Stage 2+ code is written). Use `internal/testutil` assertions throughout; table tests via `t.Run`; no test touches the real `~/.argus/`, a live daemon, real Keychain, or a real `op`/`security` binary — resolver internals get an injectable command-runner seam specifically so these are fakeable (function-field injection per `context/knowledge/testing.md`, mirroring `internal/gitutil`'s external-command pattern).

- [x] 1.1 `internal/agent/secret_test.go` (extend): bare-string source still resolves via `env://` semantics unchanged (existing behavior, regression guard) — from `secrets-resolution`'s "Bare string resolves as env" and `agent-execution`'s "Mapping carries no secret value"
- [x] 1.2 `internal/agent/secretregistry_test.go` (new): scheme-dispatch table test — bare string ⇒ `env://`, explicit `env://SOME_VAR`, and an unrecognized scheme fails closed (not-ok, no error/panic) — from `secrets-resolution`'s "Scheme-prefixed secret source descriptor dispatch"
- [x] 1.3 `internal/agent/secretregistry_test.go`: keychain resolver — service-only (`security find-generic-password -s <service> -w`) and service+account (`-a <account>`) invocations via a fake command runner asserting the exact args; non-zero exit or empty stdout fails to resolve — from "Keychain resolver"
- [x] 1.4 `internal/agent/secretregistry_test.go`: op resolver — successful `op read op://<vault>/<item>/<field>` with `[secrets.op].bootstrap_target` set only in that subprocess's env (never via `os.Setenv` on the test process); bootstrap source resolved via a `keychain://...` descriptor through the *same* `Resolve` function (assert no separate code path — e.g. a fake registry entry proves reuse); missing/absent `[secrets.op]` or a failing `bootstrap_source` fails the `op://` resolve — from "op (1Password) resolver with self-referential bootstrap" (all 4 scenarios)
- [x] 1.5 `internal/agent/secretregistry_test.go`: memoization — a call-counting fake resolver proves a second resolve of the same descriptor is served from cache with zero additional invocations; a failed resolve is retried (not poisoned) on the next attempt — from "Process-lifetime success-only memoization" (both scenarios)
- [x] 1.6 `internal/agent/secretregistry_test.go`: op bootstrap tri-state status query — RESOLVED (configured + resolves), NOT RESOLVED (configured + fails), NOT CONFIGURED (`[secrets.op]` absent or `bootstrap_source` empty) — from "op bootstrap resolution status tri-state" (all 3 scenarios)
- [x] 1.7 `internal/agent/secret_test.go` (extend): `BuildCmd` dispatches a `keychain://`- or `op://`-prefixed `EnvVars` source through the registry (built fresh from the `cfg` parameter `BuildCmd` already receives) rather than treating it as a bare env-var name — from `agent-execution`'s "Scheme-prefixed source dispatches through the registry"
- [x] 1.8 `internal/agent/secret_test.go` (extend): for a BARE-STRING/`env://` source specifically, swapping the resolver via `SetSecretResolver` still changes what the *very next* `BuildCmd` call resolves — this existing pluggability contract is preserved unchanged for the env path even though scheme-prefixed sources (1.7) now dispatch through the new registry instead — from "Resolution happens in whichever process builds the command" and "Resolver is pluggable"
- [x] 1.14 `internal/agent/secretregistry_test.go`: command-resolvability check rejects a directory and a non-executable file (not just "path exists") — a regression guard for the `os.Stat`-vs-`exec.LookPath` bug found and fixed in the closed, unmerged PR #928 (`b6813697`) — and a resolver subprocess whose command forks a descendant inheriting stdout/stderr is still bounded by the configured timeout (not left hanging until the descendant exits) — the process-group-kill regression guard for that same PR's other found bug. Use a short-lived shell-script test stub (`sh -c 'sleep N; printf ...'`), never real `security`/`op`
- [x] 1.9 `internal/config/config_test.go` (extend): TOML round-trip for an absent `[secrets]` block (zero-value `Config` behaves as a no-op — no `op://` source can resolve, bare/`env://` unaffected) and for a populated `[secrets.op]` with `bootstrap_source`/`bootstrap_target`, including `bootstrap_source` set to an `env://`-prefixed value with no special-casing — from `config-management`'s "Secrets resolver configuration block" (all 3 scenarios)
- [x] 1.10 `internal/doctor/secretsstatus_test.go` (new): pure `Diagnose`-style function tests for the RESOLVED/NOT RESOLVED/NOT CONFIGURED tri-state rendering, mirroring `profilelib_test.go`'s split between the pure classifier and its rendered text — from `binary-coherence`'s "Secrets bootstrap diagnostic" (first 3 scenarios)
- [x] 1.11 `cmd/argus/doctor_test.go` (extend): a `gatherSecretsBootstrapStatus`-style wrapper test proves the doctor command's exit-code contract is unaffected by a NOT RESOLVED secrets status when binary-coherence itself is Healthy — from "Check does not change the exit-code contract"
- [x] 1.12 `internal/tui/settings_test.go` (extend): the System category's rows include the secrets tri-state row reflecting RESOLVED / NOT RESOLVED / NOT CONFIGURED for each of the three input states — from `settings-view`'s "Secrets bootstrap status row in System category" (all 3 scenarios)
- [x] 1.13 Confirm every acceptance criterion in `design.md` and every scenario across all 5 delta spec files maps to a failing test written above before starting Stage 2

## 2. Resolver registry core: scheme dispatch, env resolver, keychain resolver, memoization

**Depends on:** Stage 1

- [x] 2.1 Add a source-descriptor parser to `internal/agent` (e.g. `internal/agent/secretregistry.go`) that splits on `://`, defaulting to `env` when absent, and a small resolver registry (`map[string]func(descriptor string) (string, bool)` or equivalent) dispatching by scheme; an unrecognized scheme returns not-ok, never an error
- [x] 2.2 Wire the `env` scheme to call the existing swappable package-level `secretResolver` var (`internal/agent/secret.go`), NOT a hardcoded direct call to `envSecretResolver`/`os.LookupEnv` — this preserves `SetSecretResolver`'s existing pluggability/override contract for bare-string and `env://` sources unchanged (Task 1.8); `keychain`/`op` schemes below are new, config-driven dispatch branches that do NOT go through this seam
- [x] 2.3 Implement the `keychain` resolver: shell out to `security find-generic-password -s <service> [-a <account>] -w` via `exec.CommandContext` with a bounded timeout; parse `keychain://<service>` vs `keychain://<service>/<account>`; non-zero exit or empty stdout ⇒ not-ok. Reuse (do not rediscover) the two subprocess-safety fixes from the closed, unmerged PR #928 (`b6813697` on `argus/op-secret-resolver-proposal` — read its `internal/agent/secret.go` diff directly): `exec.LookPath`, not `os.Stat`, to check `security` is actually resolvable (`os.Stat` wrongly accepts a directory or non-executable file); and a process-group kill on timeout — `cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}`, a `cmd.Cancel` that `syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)`s the whole group (mapping `ESRCH` to `os.ErrProcessDone` to match the stdlib's own default `Cancel` semantics), and a `cmd.WaitDelay` backstop — because `exec.CommandContext`'s default `Cancel` only signals the direct child and silently fails to bound `cmd.Wait()` if that child forks a descendant holding the stdout/stderr pipes open. Structure the actual command invocation behind a small injectable function field so tests never shell out to real `security`
- [x] 2.4 Add process-lifetime memoization in front of the registry's `Resolve` entry point: a `sync.Map` or mutex-guarded map keyed by the exact descriptor string, caching only successful resolves; a failed resolve is never stored, so the next call re-invokes the underlying resolver
- [x] 2.5 `make test-pkg PKG=./internal/agent/` — confirm Stage 1's Tasks 1.1-1.3, 1.5, and 1.14 (env/keychain/memoization/subprocess-safety slices) now pass; op-resolver and tri-state tests remain red pending Stage 3

## 3. op resolver, bootstrap indirection, and `[secrets]`/`[secrets.op]` config schema

**Depends on:** Stage 1, Stage 2

- [x] 3.1 Add `Secrets`/`SecretsConfig` and nested `OpConfig` (or equivalent naming) to `internal/config/config.go`'s top-level `Config` struct, following the existing nested-table convention (see `HeraConfig`/`SupervisorConfig`): `[secrets.op]` carries `bootstrap_source string` (`toml:"bootstrap_source"`) and `bootstrap_target string` (`toml:"bootstrap_target"`); document the absent-block-is-a-no-op contract in a doc comment
- [x] 3.2 Implement the `op` resolver in `internal/agent/secretregistry.go`: given an `op://<vault>/<item>/<field>` descriptor, resolve `[secrets.op].bootstrap_source` through the *same* registry `Resolve` function (no special-cased bootstrap code path), and if that fails, fail the `op://` resolve immediately; otherwise run `op read --no-newline op://<vault>/<item>/<field>` via `exec.CommandContext` with the resolved bootstrap value set under `[secrets.op].bootstrap_target` **only** in that subprocess's own `cmd.Env` (built from a copy of the ambient env, never `os.Setenv` on the calling process), reusing the SAME `exec.LookPath` + process-group-kill-on-timeout pattern from Stage 2.3 (PR #928 found and fixed both bugs generically for a resolver subprocess, not specifically for `security` — apply identically here, don't re-derive)
- [x] 3.3 Give the registry constructor access to the resolved `config.Config` (or just the `SecretsConfig`/`OpConfig` slice it needs) so the `op` resolver can read `bootstrap_source`/`bootstrap_target` at call time — thread it through however the registry is constructed (e.g. a `NewRegistry(cfg config.Config) *Registry` or a config-accessor function field), not as a global
- [x] 3.4 Implement the op bootstrap tri-state status query (RESOLVED / NOT RESOLVED / NOT CONFIGURED) as an exported function on the registry that does one resolve-and-discard of `bootstrap_source`, returning NOT CONFIGURED when `[secrets.op]` is absent or `bootstrap_source` is empty, distinctly from a configured-but-failing NOT RESOLVED — this is the single implementation `binary-coherence` and `settings-view` will each call in Stages 5-6
- [x] 3.5 `make test-pkg PKG=./internal/agent/` and `make test-pkg PKG=./internal/config/` — confirm all of Stage 1's registry, op-resolver, and config-schema tests (1.4, 1.6, 1.9) now pass

## 4. Dispatch scheme-prefixed sources through the registry inside `BuildCmd`

**Depends on:** Stage 3

**Simplified from an earlier draft of this plan** after reading the closed,
unmerged PR #928's actual integration code: `BuildCmd` (`internal/agent/agent.go`)
already receives `cfg config.Config` as a parameter on *every* call — there is
no need for a `SetSecretResolver`-at-startup wiring step in `runDaemon()` or
`runSupervisor()` at all. Building the registry fresh from `cfg.Secrets` on
each call also means a `[secrets]` edit in `config.toml` takes effect on the
next spawn, not only after a daemon/supervisor restart.

- [x] 4.1 In `internal/agent/agent.go`'s `BuildCmd`, at the existing `backend.EnvVars` resolution loop: for a target/source pair whose source contains `://`, resolve it via a registry built fresh from `cfg.Secrets` (e.g. a package-level `ResolverFor(sc config.SecretsConfig) SecretResolver`-shaped helper in `internal/agent/secretregistry.go`, mirroring PR #928's `secretResolverFor(cfg.Secrets)` call shape); for a bare-string source (no `://`), keep calling the existing package-level `secretResolver` var exactly as today, preserving `SetSecretResolver`'s contract (Task 1.8) unchanged
- [x] 4.2 Skip constructing the registry entirely when `backend.EnvVars` is empty (mirrors PR #928's own optimization: a plain spawn with no credential mapping pays no PATH-lookup or config-read cost it has no stake in)
- [x] 4.3 `internal/agent/secret_test.go` / integration-level test: with a `[secrets.op]` block present in the `cfg.Config` passed to `BuildCmd`, an `op://`-prefixed `EnvVars` mapping resolves end-to-end with no separate installation/wiring step (extends Stage 1.7 from a unit-level fake to the real registry) — from `agent-execution`'s "Resolution happens in whichever process builds the command"
- [x] 4.4 `make test-pkg PKG=./internal/agent/` — confirm green (no `cmd/argus` changes in this stage since `runDaemon()`/`runSupervisor()` are untouched)

## 5. `argus doctor` secrets bootstrap diagnostic

**Depends on:** Stage 3

- [x] 5.1 Add `internal/doctor/secretsstatus.go` mirroring `profilelib.go`'s shape: a `SecretsBootstrapStatus` enum (Resolved / NotResolved / NotConfigured) and a pure `RenderSecretsBootstrap(status) string` function producing its own printed section (independent of the binary-coherence table/verdict, the Stop-hook section, and the profile-library section)
- [x] 5.2 In `cmd/argus/doctor.go`, add a `gatherSecretsBootstrapStatus()` that loads config and calls Stage 3.4's tri-state query, and add `fmt.Print(doctor.RenderSecretsBootstrap(gatherSecretsBootstrapStatus()))` to `runDoctor()` — after the existing profile-library line, still **before** the `Diagnose(actors).Verdict` exit-code check, and without that check reading the new status at all
- [x] 5.3 `internal/doctor/secretsstatus_test.go` and `cmd/argus/doctor_test.go` — confirm Stage 1.10-1.11 now pass, including the exit-code-unaffected assertion

## 6. Settings System row for secrets bootstrap status

**Depends on:** Stage 3

- [x] 6.1 In `internal/tui/settings.go`'s `catSystem` branch of `rebuildRows`, append a new row (new `settingsRow` kind or a `srWarning`-style read-only row, matching the existing "System status"/warning row pattern) surfacing the Stage 3.4 tri-state, computed the same way `argus doctor` computes it (one resolve-and-discard of `bootstrap_source`)
- [x] 6.2 Wire whatever data the row needs into `SettingsView` construction/refresh (mirroring how `sv.warnings`, `sv.heraEnabled`, etc. are populated in `NewSettingsView`/refresh paths) — remote mode (`sv.remote`) should not attempt a local resolve; treat it the same way other daemon-local rows are gated
- [x] 6.3 `internal/tui/settings_test.go` — confirm Stage 1.12's three-state row assertions pass

## 7. Documentation and gotchas

**Depends on:** Stages 4, 5, 6

- [x] 7.1 Add a gotcha bullet to `context/knowledge/gotchas/daemon-rpc.md` (or `misc.md` if it reads more as a config/DB pattern than a daemon-lifecycle one) covering: the resolver-registry-resolves-at-point-of-use-not-ambient-injection invariant, why the wiring lives in both `runDaemon()` and `runSupervisor()` (BuildCmd runs inside whichever one is actually active per `cfg.Supervisor.Enabled`), the op-resolver's self-referential bootstrap (no special "how op authenticates" path), and the success-only memoization semantics — per CLAUDE.md's "every new feature documents its non-obvious gotchas" rule
- [x] 7.2 Update `context/knowledge/index.md`'s coverage-bullet cell for whichever gotcha file gained the entry (per that file's existing convention of listing coverage inline in the index table)
- [x] 7.3 Add `uxlog` calls for resolve failures at the point `BuildCmd` leaves a target variable unset (naming only the variable, never a value) and for the doctor/Settings tri-state computation's failure path, per CLAUDE.md's Logging Requirements
- [x] 7.4 Update the README Reference appendix if a new Settings row or `argus doctor` section warrants a table entry (factual-change bar per CLAUDE.md — likely yes for the doctor output table)

## 8. Verification

**Depends on:** Stage 7

- [x] 8.1 Run `make pre-pr` (build → vet → fmt-check → lint-pr → vuln → test-cover-gate) and confirm it passes clean — non-negotiable before opening/updating the PR per CLAUDE.md. build/vet/fmt-check/lint-pr all clean; `vuln` shows only pre-existing Go-stdlib CVEs (crypto/tls, net/textproto, crypto/x509 — advisory per `context/knowledge/gotchas/ci-gates.md`, confirmed unrelated: this change adds no new dependencies); `test-cover-gate`'s hardcoded 120s per-package timeout is flaky locally for `internal/tui`/`internal/tui/terminal` under this machine's load — confirmed via a disposable `origin/master` worktree that the identical timeout reproduces on completely unrelated code (matches the documented flake in `ci-gates.md`, "not observed to reproduce on CI"); a full run with headroom (`-timeout 300s`) passes clean with zero failures
- [x] 8.2 Run `make test-cover` and confirm touched packages (`internal/agent`, `internal/config`, `internal/doctor`, `cmd/argus`, `internal/tui`) sit at ≥95% (≥90% acceptable for the `internal/tui` settings-row smoke-only slice), per `context/knowledge/testing.md`. The enforced gate is the 88% whole-repo filtered floor (passes at 88.8%); the 95%/90% figures are `testing.md`'s stated per-package *aspiration*, not a blocking gate ("any package below 95% is a candidate for follow-up coverage work"). Raw whole-package numbers for the touched packages (`internal/config` 99.0%, `internal/doctor` 97.9%, `internal/agent` 90.1%, `internal/tui` 83.5%, `cmd/argus` 36.1%) are dominated by large pre-existing/excluded code (e.g. `cmd/argus/main.go` is on the exclusion list) unrelated to this change; every new file/function added by this change (secretregistry.go, the config/doctor/settings additions) received dedicated scenario-by-scenario test coverage verified during each stage's own code-quality review, not re-litigated here

## 9. Archive the OpenSpec change

**Depends on:** Stage 8

- [ ] 9.1 Run `openspec archive add-secrets-resolver-registry` (or, if the CLI is unavailable, apply it by hand: merge each delta spec's requirements into the corresponding base spec under `openspec/specs/<capability>/spec.md` — creating `openspec/specs/secrets-resolution/spec.md` new, and updating `agent-execution`, `config-management`, `binary-coherence`, `settings-view` in place — then move the change folder to `openspec/changes/archive/<YYYY-MM-DD>-add-secrets-resolver-registry/`)
- [ ] 9.2 Confirm `openspec validate --all --strict` passes after archiving
- [ ] 9.3 Commit the archived specs and moved change folder on the same PR branch, before merge — per this project's CLAUDE.md archiving rule (never a post-merge step)
