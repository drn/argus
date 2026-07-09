## Why

A Hera-rail worker parked at a permission-confirmation dialog (a plain inline
scrollback selection prompt, not alt-screen) kept showing the "active" spinner
instead of the `(?)` needs-input icon for HOURS, only flipping the instant the
user manually focused that agent's row (BUG-029).

Root cause: the only route to needs-input for a session whose tail never goes
byte-idle is the content-stability pass, which requires TWO CONSECUTIVE ticks
with an IDENTICAL content fingerprint. Any line that legitimately varies
tick-to-tick but isn't one of the two recognized "chrome" shapes (spinner/box
decoration, `❯`-leading lines) permanently defeats the 2-tick match — the
fingerprint never converges, so the session is never flagged, and its spinner
(driven by the same fingerprint via `agent.ContentIdle`) never stops. This is
proven intentional-by-design elsewhere (new transcript content must NOT
converge), so the fix must not loosen what counts as chrome.

## What Changes

- Add a bounded escalation fallback to both the TUI's content-stability pass
  (`detectNeedsInputSticky` in `internal/tui/app.go`) and `agent.ContentIdle`
  (`internal/agent/needsinput.go`): when the RAW tail (or emulated screen)
  already matches the selection-prompt shape with the "working" affordance
  ABSENT, and that combination persists for N consecutive ticks — even though
  the full-tail content fingerprint never converges tick-to-tick — the session
  is flagged needs-input / classified content-idle anyway.
- N is a named, documented constant (not inline), chosen in the 5-10
  consecutive-tick range to bound the worst case from "forever" to "N seconds"
  without raising false-positive risk on a genuinely busy agent.
- No change to the shared chrome-recognition allowlist
  (`fingerprintVolatileLine` / `decorationLine`) or to `ForceResyncPTY`/resize
  behavior.

## Capabilities

### Modified Capabilities

- `idle-detection`: adds a new requirement — a bounded consecutive-tick
  escalation fallback for the content-stability pass and content-idle
  classification, for a session whose fingerprint never converges despite
  showing an unambiguous parked-selection-prompt signal. Existing requirements
  (fingerprint definition, 2-tick content-stability match, content-idle
  computation) are unchanged; this is additive.

## Impact

- **Code:** `internal/tui/app.go` (`detectNeedsInputSticky`),
  `internal/agent/needsinput.go` (`ContentIdle`), plus a small shared
  consecutive-tick counter helper if the two call sites can share one without
  an awkward `internal/tui` ↔ `internal/agent` coupling (duplication is
  acceptable otherwise).
- **Tests:** `internal/agent/needsinput_test.go`,
  `internal/tui/app_test.go` (or the relevant sticky-detection test file) —
  N-tick escalation fires; a transient (<N tick) match does not false-positive;
  all existing BUG-032/033/035/036 fixtures stay green.
- **Docs:** `context/knowledge/gotchas/events.md`.
- **No schema change, no new MCP tool, no TUI keybinding change, no daemon-side
  `computeNeedsInput` change.**
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make / Go-build
  wiring added or changed. The quality gate stays `make pre-pr`.
