## 1. Approval

- [x] 1.1 Review the proposal, design, and deltas with Aaron in Plannotator; apply annotations and obtain approval before implementation.

## 2. Preference

- [x] 2.1 Write failing tests for the off default, TOML import, DB persistence, and live Appearance toggle.
- [x] 2.2 Add `UIConfig.TasksRail`, `ui.tasks_rail` persistence/import, and the “Persistent task rail” Appearance toggle with a live update callback and uxlog events.

## 3. Project rail

- [x] 3.1 Write failing rail-mode tests for header navigation/folding, focus-fold and all-open project modes, pinned/archive behavior, filter/H reconciliation, status aggregation, and legacy-mode preservation.
- [x] 3.2 Add opt-in rail behavior to the existing task list, reusing its partitioning, rendering, status, and filtering machinery; share small Projects helpers where appropriate.

## 4. Layout and session binding

- [x] 4.1 Write failing tests for rail width, narrow-window recovery, selected task binding without auto-start, unchanged-selection refreshes, and focus tracking.
- [x] 4.2 Compose the existing task list beside the existing agent content, retaining tab chrome and rail during zoom; seed/publish valid PTY dimensions from the actual pane.
- [x] 4.3 Reconcile selection, deletion, filters, task/session replacement, tab switching, modal return, and live preference changes without restarting sessions or leaking redraw loops.

## 5. Input and smoke coverage

- [x] 5.1 Write failing SimulationScreen tests for rail-to-terminal entry, Ctrl+Q return, terminal Tab/paste, modified-arrow traversal, zoom, narrow resize, mouse focus, and live toggle transitions.
- [x] 5.2 Implement focus/key/mouse routing with filter/modal precedence, existing lifecycle checks, and cross-tab arrow behavior; log selection, layout, skips, failures, and focus transitions.
- [x] 5.3 Update the keymap/help assertions and README Reference tables for the opt-in key contexts and setting.

## 6. Verification and delivery

- [x] 6.1 Document non-obvious invariants in the relevant gotcha files and run the targeted race tests and touched-package coverage checks.
- [x] 6.2 Review the implementation using hera-review and the test-adversary lens; resolve findings.
- [x] 6.3 Archive the change into base specs on this branch, run strict spec validation, and run the full `make pre-pr` gate clean.
- [ ] 6.4 Commit, push through iris, open the PR through `iris_gh_pr_create`, and report its URL and validation evidence to coord.
