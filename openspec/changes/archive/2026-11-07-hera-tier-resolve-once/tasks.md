## 1. Failing tests and benchmark

- [x] 1.1 Keep `BenchmarkResolveHeraTier_PerBuild` (200 roles) and the env-gated `BenchmarkConfig_RealCopy`; record the baseline
- [x] 1.2 Write failing tests for each scenario in `specs/hera-view/spec.md` (one Config call per build, one profile decode per distinct profile per build, output identical to the per-role path, per-key separation, edits visible on the next build, in-repo profile precedence, factory used once per `doRefresh`)
- [x] 1.3 Confirm each new test fails for the intended reason

## 2. Implementation

- [x] 2.1 Add `HeraPage.SetTierResolverFactory` and use it in `doRefresh`
- [x] 2.2 Add `App.heraTierResolverForBuild` with the config snapshot and build-scoped memos; make `resolveHeraTier` delegate to a one-shot build closure
- [x] 2.3 Wire the factory in `app.go`; add the per-build `uxlog` line

## 3. Verification and docs

- [x] 3.1 Run the benchmark after the change; run `-race` on `./internal/tui/...`
- [x] 3.2 Add gotchas to `context/knowledge/gotchas/hera-view.md` and update the index
- [x] 3.3 `make pre-pr`; `/hera-review` and `/hera-review-test-adversary`; archive this change in the same PR
