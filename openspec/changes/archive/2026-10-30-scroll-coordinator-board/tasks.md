## 1. Regression tests

- [x] 1.1 Add a failing SimulationScreen smoke test for wheel scrolling an expanded tall item without stealing focus.
- [x] 1.2 Add unit coverage for top/bottom clamp, content resize, and cursor follow.

## 2. Implementation

- [x] 2.1 Record board bounds and route wheel events through the Hera page with three-line scrolling.
- [x] 2.2 Preserve keyboard scrolling, selection and expansion; document routing gotcha and frontend parity.

## 3. Validation and delivery

- [x] 3.1 Run targeted tests and make pre-pr, archive the change, validate specs.
- [x] 3.2 Prepare the reviewed change for iris push and a PR against master, including the smoke test name.
