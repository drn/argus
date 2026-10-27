## 1. Regression coverage

- [x] 1.1 Unit-test that a new unread item on the selected coordinator selects the board, while an unchanged unread set, a read acknowledgment, the disabled setting and a worker selection do not.
- [x] 1.2 Update the refresh/toggle test that asserted mail arrival keeps the plan.

## 2. Implementation and verification

- [x] 2.1 Track the selected coordinator's unread item IDs across refreshes and select the board on a newly unread ID; add uxlog.
- [x] 2.2 Update the Hera gotcha; archive the modified requirement in this change.
