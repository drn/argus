## 1. Delivery

- [x] 1.1 Frame web compose text with bracketed-paste start/end markers in one
      ordered input write, preserving the existing separate delayed CR submit.
- [x] 1.2 Keep input-history recording human-readable (no paste markers) and
      prevent a failed text write from submitting stale agent input. Include
      framing bytes in the input size check.
- [x] 1.3 Bump the service-worker shell version for the SPA change.
- [x] 1.4 Reject embedded bracketed-paste delimiters so text cannot escape
      the frame and become keystrokes.

## 2. Verification

- [x] 2.1 Update Playwright compose tests for exact paste and CR write ordering,
      `$pr`, ordinary text, and failure handling.
- [x] 2.2 Run the relevant Playwright suite and race Go suite (`make test-cover`,
      which uses the same race test command as `make test`).
- [x] 2.3 Smoke-test a real Codex session with `pr` and another `p...` skill,
      and a real Claude `/skill` submission. Revise this change if paste
      semantics fail either test.
- [x] 2.4 Document any non-obvious paste/submit invariant in
      `context/knowledge/gotchas/web-remote.md`.

## 3. Ship

- [ ] 3.1 Run `make test-cover`, then `make pre-pr` before any PR push/update.
- [ ] 3.2 Archive this change on the change branch before merge.
