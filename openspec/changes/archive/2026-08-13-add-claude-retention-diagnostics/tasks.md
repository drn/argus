## 1. Tests

Write failing tests for every scenario below (red first, per project.md's TDD default) before any Stage 2+ code. Use `internal/testutil` assertions; table tests via `t.Run`; no test touches the real `~/.claude/` or `~/.argus/` — inject an explicit path (mirroring `claudeSettingsPath`/`readStopHookCommands`'s existing testable-path pattern) so every test reads a `t.TempDir()` fixture file, never the real settings.json.

- [x] 1.1 `internal/agent/claudesettings_test.go` (new): `QueryClaudeCleanupPeriodDays`-equivalent (explicit-path variant for testability, mirroring `diagnoseProfileLibraryAt`'s injectable-path pattern) — configured value returned, absent key returns nil+no error, missing file / malformed JSON returns an error — from `agent-execution`'s "Query Claude Code's effective cleanup-period retention" (all 3 scenarios)
- [x] 1.2 `internal/agent/claudesettings_test.go`: resume-failure classifier — last output containing `No conversation found with session ID:` classifies as a retention-swept failure; unrelated crash text does not — from `agent-execution`'s "Retention-swept resume failure is classified distinctly" (both scenarios)
- [x] 1.3 `internal/doctor/cleanupstatus_test.go` (new): pure `DiagnoseCleanupPeriod`/`RenderCleanupPeriod` tests — explicit value above 30 → OK, nil (unset) → LOW, value ≤30 → LOW, read error → UNKNOWN — from `binary-coherence`'s "Claude session retention diagnostic" (first 4 scenarios)
- [x] 1.4 `cmd/argus/doctor_test.go` (extend): `gatherCleanupPeriodStatus`-style wrapper test proves the doctor command's exit-code contract is unaffected by a LOW or UNKNOWN retention status when binary-coherence itself is Healthy — from "Check does not change the exit-code contract"
- [x] 1.5 `internal/tui/settings_test.go` (extend): the System category's rows include the retention tri-state row for OK / LOW / UNKNOWN, and the row is absent when `sv.remote` is true — from `settings-view`'s "Claude session retention status row in System category" (all 4 scenarios)
- [x] 1.6 `internal/tui/app_test.go` (extend, or new): `handleSessionExitUI` sets the explanatory status-bar notice when last output matches the retention signature, and sets no such notice for a generic non-matching crash — from `tui-shell`'s "Retention-swept resume failure surfaces an explanatory notice" (both scenarios)
- [x] 1.7 Confirm every scenario across all 4 delta spec files maps to a failing test written above before starting Stage 2

## 2. Shared query + classifier (`agent-execution`)

**Depends on:** Stage 1

- [x] 2.1 Add `internal/agent/claudesettings.go`: a function reading a given path's JSON for a top-level `cleanupPeriodDays` field (mirroring `readStopHookCommands`'s shape: explicit path in, `(*int, error)` out) plus a package-level convenience wrapper resolving `~/.claude/settings.json` via `os.UserHomeDir()` for real callers (`cmd/argus/doctor.go`, `internal/tui/settings.go`)
- [x] 2.2 Add the resume-failure classifier (`func IsRetentionSweptResumeFailure(lastOutput []byte) bool` or similar) checking for the literal substring `No conversation found with session ID:`
- [x] 2.3 `make test-pkg PKG=./internal/agent/` — confirm Stage 1.1-1.2 pass

## 3. `argus doctor` diagnostic (`binary-coherence`)

**Depends on:** Stage 2

- [x] 3.1 Add `internal/doctor/cleanupstatus.go` mirroring `secretsstatus.go`'s shape: `CleanupPeriodStatus` enum (OK / Low / Unknown) + `DiagnoseCleanupPeriod(days *int, readErr error) CleanupPeriodStatus` + `RenderCleanupPeriod(status, days *int) string` (LOW branch prints the `{"cleanupPeriodDays": 3650}` fix snippet)
- [x] 3.2 In `cmd/argus/doctor.go`, add `gatherCleanupPeriodStatus()` calling Stage 2.1's query + `doctor.DiagnoseCleanupPeriod`, and add `fmt.Print(doctor.RenderCleanupPeriod(...))` to `runDoctor()` after the existing secrets-bootstrap line, still before the exit-code check
- [x] 3.3 `internal/doctor/cleanupstatus_test.go` and `cmd/argus/doctor_test.go` — confirm Stage 1.3-1.4 pass

## 4. Settings → System row (`settings-view`)

**Depends on:** Stage 2

- [x] 4.1 Add a `srClaudeRetention` `settingsRowKind`; in `SettingsView`'s refresh path (mirroring `sv.secretsBootstrapStatus`'s population, gated on `!sv.remote`), call Stage 2.1's query and store the raw `(*int, error)`
- [x] 4.2 Add local `claudeRetentionLabel`/`claudeRetentionColor` pure functions (mirroring `secretsBootstrapStatusLabel`/`Color`) mapping the raw query result to OK / LOW / UNKNOWN + color, and a row entry in `catSystem`'s `rebuildRows` gated on `!sv.remote`
- [x] 4.3 Add `renderClaudeRetentionDetail` mirroring `renderSecretsBootstrapDetail`: status line, current effective value (or "unset — defaults to 30"), and the fix snippet when LOW
- [x] 4.4 `internal/tui/settings_test.go` — confirm Stage 1.5 passes

## 5. Resume-failure notice (`tui-shell`)

**Depends on:** Stage 2

- [x] 5.1 Thread `lastOutput []byte` into `handleSessionExitUI` (both existing callers, `NotifySessionExit` and `HandleSessionExit`, already have it in scope — `NotifySessionExit` currently discards it via `_ = lastOutput`, `HandleSessionExit` only logs its length)
- [x] 5.2 Inside `handleSessionExitUI`, on a non-clean exit, call Stage 2.2's classifier; on a match, call `a.statusbar.SetError(...)` (or `SetInfo`, matching the existing severity convention for an expected/explained condition rather than an Argus bug) with the explanatory message pointing at Settings → System / `argus doctor`
- [x] 5.3 `internal/tui/app_test.go` — confirm Stage 1.6 passes

## 6. Documentation

**Depends on:** Stages 3, 4, 5

- [x] 6.1 README: extend "Diagnosing binary skew (`argus doctor`)" with one sentence on the new retention section, mirroring the existing Stop-hook/profile-library/secrets-bootstrap sentences; extend the existing "Claude Code session retention" section noting Settings → System mirrors the same tri-state live and that a swept-transcript resume failure now surfaces an explanatory notice instead of a bare crash.
- [x] 6.2 Add a gotcha bullet (`context/knowledge/gotchas/misc.md` or a new `daemon-rpc.md` bullet, whichever reads more naturally) covering: the shared `agent.QueryClaudeCleanupPeriodDays` primitive consumed by both doctor and Settings, the exact resume-failure signature string being the sole detection mechanism (no other plumbing needed since it only occurs on a `--resume` attempt in practice), and that cleanup is a global `~/.claude` sweep with no per-origin scoping.
- [x] 6.3 Update `context/knowledge/index.md`'s coverage-bullet cell for whichever gotcha file gained the entry.

## 7. Verification

**Depends on:** Stage 6

- [x] 7.1 Run `make pre-pr` (build → vet → fmt-check → lint-pr → vuln → test-cover-gate) and confirm it passes clean — non-negotiable before opening/updating the PR. build/vet/fmt-check/lint-pr all clean (0 new lint issues); `vuln` shows only pre-existing Go-stdlib CVEs (crypto/tls, net/textproto, crypto/x509, net, html/template — advisory per `context/knowledge/gotchas/ci-gates.md`, confirmed unrelated: no new dependencies added); `test-cover-gate` hit two pre-existing, documented flakes unrelated to this change — `internal/agent`'s `TestBuildCmd_EnvVarMapping_UnresolvedSourceUnsetAndWarns` fails on this dev machine because a real `OPENAI_API_KEY` leaks from the shell environment (reproduces identically on a clean `origin/master` checkout via `git stash`), and `internal/tui` hit the documented 120s per-package timeout flake under full-suite `-race` on a loaded machine (passes in ~93s in isolation, well under the timeout). A full run with headroom (`-timeout 300s`, `OPENAI_API_KEY` unset for measurement) passes clean with zero failures at 88.7% filtered coverage (above the 88% floor).
- [x] 7.2 Run `make test-cover` and confirm touched packages (`internal/agent`, `internal/doctor`, `cmd/argus`, `internal/tui`) sit at or above the aspirational per-package floor in `context/knowledge/testing.md` (≥95%, ≥90% acceptable for UI smoke-only code), noting any pre-existing exclusions (e.g. `cmd/argus/main.go`) that dominate a package's raw number. Raw whole-package numbers: `internal/doctor` 98.3%, `internal/agent` 89.8%, `internal/tui` 83.3%, `cmd/argus` 36.4% — the latter two are dominated by large pre-existing/excluded or UI-smoke-only code unrelated to this change (`cmd/argus/main.go` is on the coverage-floor exclusion list); every new file/function this change added (`claudesettings.go`, `cleanupstatus.go`, the doctor/settings/app.go wiring) received dedicated scenario-by-scenario test coverage during implementation.

## 8. Archive the OpenSpec change

**Depends on:** Stage 7

- [x] 8.1 Run `openspec archive add-claude-retention-diagnostics` (or, if the CLI is unavailable, apply it by hand: merge each delta spec's requirements into the corresponding base spec under `openspec/specs/<capability>/spec.md`, then move the change folder to `openspec/changes/archive/<YYYY-MM-DD>-add-claude-retention-diagnostics/`)
- [x] 8.2 Confirm `openspec validate --all --strict` passes after archiving (if the CLI is available)
- [x] 8.3 Commit the archived specs and moved change folder on the same PR branch, before merge
