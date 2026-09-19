## 1. Composer snapshot tests

- [x] 1.1 Add VT-rendered composer extraction tests for empty, typed, wrapped, fullscreen, and unknown prompt states.
- [x] 1.2 Add notifier tests for empty, notice-only, changing, and stable-abandoned composer decisions.

## 2. Content-aware delivery

- [x] 2.1 Expose a bounded composer snapshot from `agent.ScreenRenderer`.
- [x] 2.2 Track per-delivery draft stability and replace the primary idle/focus gate with content classification.
- [x] 2.3 Preserve and annotate stable abandoned content while clearing empty or notice-only composers.

## 3. Diagnostics and documentation

- [x] 3.1 Log content decisions without logging composer text.
- [x] 3.2 Update messaging gotchas and their index summary.

## 4. Verification and delivery

- [x] 4.1 Run targeted race tests and strict OpenSpec validation.
- [x] 4.2 Archive the change into the base spec.
- [x] 4.3 Run `make pre-pr`, commit, push, and open the follow-up PR through Iris.
