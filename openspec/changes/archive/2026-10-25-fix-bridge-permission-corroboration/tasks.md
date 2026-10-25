## 1. Probe and contract

- [x] 1.1 Verify base 1b6ce5eb and probe live telemetry admission.
- [x] 1.2 Define per-field corroboration specs and document telemetry policy failure.

## 2. Implementation

- [x] 2.1 Add regression tests for shared resolution and all daemon verdict consumers.
- [x] 2.2 Preserve bridge source, resolve uncertain input using raw scraper state, and align shadow/TUI.
- [x] 2.3 Update gotchas and run mod validation/tests unchanged.

## 3. Delivery

- [x] 3.1 Review behavior and tests, archive this change and validate specs.
- [x] 3.2 Complete pre-PR validation and prepare the stacked PR against argus/argus-bridge/3b-band for iris delivery. All full-gate checks/packages passed except the known TUI 200s aggregate budget; the coordinator-approved isolated race rerun passed with a 300s budget (239.021s).
