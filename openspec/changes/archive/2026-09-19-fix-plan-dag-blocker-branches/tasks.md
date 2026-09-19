## 1. Specify fan-in branch context

- [x] 1.1 Trace blocker gating, binding fallback, base selection, prompt construction, and coordinator notification.
- [x] 1.2 Define the worker prompt behavior and non-goals in proposal, design, and delta spec.
- [x] 1.3 Validate the OpenSpec change strictly before implementation.

## 2. Implement with tests

- [x] 2.1 Add failing gater tests for complete fan-in branch context, unresolved branches, and unchanged root/single-blocker prompts.
- [x] 2.2 Resolve blocker branch metadata once and append the fan-in section to ordinary worker prompts.
- [x] 2.3 Keep base selection and coordinator fan-in notices behaviorally unchanged.

## 3. Update guidance and verify

- [x] 3.1 Update the bundled `hera-plan` skill to use the automatically supplied blocker branch map.
- [x] 3.2 Add the new orchestration gotcha.
- [x] 3.3 Run focused tests, strict OpenSpec validation, and `make pre-pr`.
- [x] 3.4 Archive the OpenSpec change and re-run strict validation.
