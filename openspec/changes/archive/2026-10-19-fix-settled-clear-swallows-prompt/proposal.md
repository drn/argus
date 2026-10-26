## Why

A worker parked on a Claude Code permission prompt (`❯ 1. Yes / 2. No`) showed no needs-input indicator. Replaying the worker's real session log through the production detector showed the prompt itself was detected correctly; the flag was suppressed afterwards. An earlier prompt had been cleared by the settlement path, which records a cleared marker with no content fingerprint. With no fingerprint the stale-re-detection guard falls back to timestamp-only comparison, and because nobody typed (the last-input timestamp never advanced) every later prompt was swallowed as a "stale re-detection".

## What Changes

- A cleared marker produced by the settlement path is flagged as settled. When such a marker has no fingerprint and a later candidacy has one, the candidacy re-arms instead of being suppressed.
- A marker without a fingerprint that did not come from settlement keeps the existing timestamp-only suppression.
- The change lives in the shared clear function, so the TUI, the daemon push watcher, and the web/macOS clients (which consume the daemon's set) all inherit it.

## Impact

- Affects the needs-input clear logic in `internal/agent`; no wire or schema changes.
