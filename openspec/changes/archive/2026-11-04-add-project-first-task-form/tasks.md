## 1. Spec approval

- [x] 1.1 Commit proposal, design, deltas, and tasks; send the change path to the coordinator and wait for approval before implementation (approved with three changes in Hera message 20929).
- [x] 1.2 Update the spec for empty-filter typing, Escape backtracking with prompt/name retention, and recency ordering from existing task data.

## 2. Preference persistence with TDD

- [x] 2.1 Add failing tests for default false, persisted true/false reload, and existing configuration precedence.
- [x] 2.2 Add the UI config field and DB loading using existing UI preference conventions.
- [x] 2.3 Add failing Settings tests, then implement the Appearance Project first row, detail, toggle, persistence error feedback, and uxlog calls; preserve Left-to-rail navigation.

## 3. Project phase with TDD

- [x] 3.1 Add failing form tests for typeahead and paste, Enter accepting initial/changed projects, invalid input, immediate Escape cancellation, full-form project editing, and unchanged disabled navigation.
- [x] 3.2 Implement the project-only phase by reusing existing project selection and project-change resolution, returning to prompt focus on acceptance.
- [x] 3.3 Add failing tests comparing branch/backend/model/profile/account/sandbox/skills behavior against selecting the same project in the full form, then ensure both routes share the defaults path.
- [x] 3.4 Wire Tasks new task and Hera new coordinator, including command-palette routes; test single/zero-project bypass and explicit worker-spawn exclusion.
- [x] 3.5 Log phase opening, acceptance, cancellation, bypass, and stale async results; guard async results against canceled/replaced forms and changed projects where necessary.
- [x] 3.6 Test and implement empty-filter/open-list initialization, default highlight, first-match reset, recent-project ordering without new storage, and Escape backtracking with prompt/name retention and Ctrl+Q cancellation.
- [x] 3.7 Keep remote creation on the existing flow and test its config-enabled bypass; match sibling remote Appearance toggle behavior and document the named follow-up.

## 4. Real-event-loop verification

- [x] 4.1 Add SimulationScreen smoke tests for setting ON: only project step visible, one Enter reveals the full form with prompt focus, chosen project and dependent defaults applied, paste works, and Escape restores the origin without residual state.
- [x] 4.2 Add SimulationScreen smoke tests for setting OFF: full form opens directly with unchanged prompt focus/defaults and existing focus traversal; cover both task and coordinator openings.
- [x] 4.3 Test canceled/reopened lifecycle and no-choice bypass; run targeted tests with the race detector.
- [x] 4.4 Document non-obvious lifecycle/default-resolution gotchas; update keymap/help only if keys actually change and README Reference only if a factual table changes.
- [x] 4.5 Exercise typing another project without backspacing, Escape/re-pick preserving prompt/name, and recency ordering through the real event loop.

## 5. Validate and deliver

- [x] 5.1 Run relevant unit/smoke suites, `make test-cover`, and full `make pre-pr`; report ambient ARGUS_* profile-test contamination, signing workarounds, and advisory stdlib vulnerability results explicitly.
- [x] 5.2 Archive this change into base specs on the same branch before merge, then run `openspec validate --all --strict` and inspect the final diff.
- [ ] 5.3 Commit, push through iris only after `make pre-pr` passes, and open a PR against master through iris; do not merge. No attribution line is required (coordinator message 20934).
- [ ] 5.4 Send the coordinator the PR link, setting key and Settings location, local versus CI-only checks, and deliberate exclusions.

## Validation evidence

- Targeted unit/SimulationScreen tests and broad TUI/DB/config race suites passed locally on macOS.
- `make test-cover` passed with 89.0% filtered coverage. Full `make pre-pr` passed with `GOTOOLCHAIN=go1.26.10`, `GOFLAGS=-p=3`, and `CODEX_HOME`/`CODEX_SQLITE_HOME` unset; the unchanged coverage gate is 88%.
- Repo-default Go 1.26.8 stops at 11 reachable stdlib vulnerability findings (advisory in CI); the repo toolchain was not changed. Inherited Codex directory variables caused the daemon recapture fixture to fail; it passed with those unset. One full attempt hit the unrelated supervisor-client socket-startup/cleanup flake, which passed isolated and in the reduced-concurrency full gate. No ARGUS profile-env or commit-signing failures occurred.
- Strict OpenSpec validation passed for all 75 items after archival. Ubuntu execution remains CI-only; web/macOS assets and REST contracts were not changed.
