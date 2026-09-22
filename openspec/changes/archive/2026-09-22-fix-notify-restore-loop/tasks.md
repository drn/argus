## 1. Delivery safety

- [x] 1.1 Specify a per-task, one-shot circuit breaker for self-restored draft content.
- [x] 1.2 Track the last-restored draft on `Notifier` and consume it on the next stable-draft observation for that task.
- [x] 1.3 Add regression coverage, document the invariant, validate, and run checks.
