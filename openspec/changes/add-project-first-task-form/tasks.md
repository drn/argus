## 1. Spec approval

- [ ] 1.1 Commit proposal, design, deltas, and tasks; send the change path to the coordinator and wait for approval before implementation.

## 2. Preference persistence with TDD

- [ ] 2.1 Add failing tests for default false, persisted true/false reload, and existing configuration precedence.
- [ ] 2.2 Add the UI config field and DB loading using existing UI preference conventions.
- [ ] 2.3 Add failing Settings tests, then implement the Appearance Project first row, detail, toggle, persistence error feedback, and uxlog calls; preserve Left-to-rail navigation.

## 3. Project phase with TDD

- [ ] 3.1 Add failing form tests for typeahead and paste, Enter accepting initial/changed projects, invalid input, immediate Escape cancellation, full-form project editing, and unchanged disabled navigation.
- [ ] 3.2 Implement the project-only phase by reusing existing project selection and project-change resolution, returning to prompt focus on acceptance.
- [ ] 3.3 Add failing tests comparing branch/backend/model/profile/account/sandbox/skills behavior against selecting the same project in the full form, then ensure both routes share the defaults path.
- [ ] 3.4 Wire Tasks new task and Hera new coordinator, including command-palette routes; test single/zero-project bypass and explicit worker-spawn exclusion.
- [ ] 3.5 Log phase opening, acceptance, cancellation, bypass, and stale async results; guard async results against canceled/replaced forms and changed projects where necessary.

## 4. Real-event-loop verification

- [ ] 4.1 Add SimulationScreen smoke tests for setting ON: only project step visible, one Enter reveals the full form with prompt focus, chosen project and dependent defaults applied, paste works, and Escape restores the origin without residual state.
- [ ] 4.2 Add SimulationScreen smoke tests for setting OFF: full form opens directly with unchanged prompt focus/defaults and existing focus traversal; cover both task and coordinator openings.
- [ ] 4.3 Test canceled/reopened lifecycle and no-choice bypass; run targeted tests with the race detector.
- [ ] 4.4 Document non-obvious lifecycle/default-resolution gotchas; update keymap/help only if keys actually change and README Reference only if a factual table changes.

## 5. Validate and deliver

- [ ] 5.1 Run relevant unit/smoke suites, `make test-cover`, and full `make pre-pr`; report ambient ARGUS_* profile-test contamination, signing workarounds, and advisory stdlib vulnerability results explicitly.
- [ ] 5.2 Archive this change into base specs on the same branch before merge, then run `openspec validate --all --strict` and inspect the final diff.
- [ ] 5.3 Commit, push through iris only after `make pre-pr` passes, and open a PR against master through iris with the session attribution line; do not merge.
- [ ] 5.4 Send the coordinator the PR link, setting key and Settings location, local versus CI-only checks, and deliberate exclusions.
