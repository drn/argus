## Why

The web compose bar sends its entire text as one raw PTY write and then sends
Enter in a second write. Codex's `$` skill picker can remain open after that
text arrives. Its Enter handler accepts the highlighted suggestion, so sending
`$pr` can select the first skill (for example, `presentations`) instead of
submitting the intended `pr` request. The web app currently has no paste
framing for compose text.

## What Changes

- Deliver compose text through bracketed paste framing, then send Enter as a
  separate write after the existing pause. This makes the CLI receive the
  entire composed message as one paste event rather than interpreting its
  characters as interactive picker input.
- Preserve accepted message text exactly, including `$skill` references;
  reject embedded paste delimiters, which could make the rest of a message
  act as terminal keystrokes. Keep the existing failure rule: if writing the
  paste fails, do not send Enter.
- Verify the behavior with a real Codex session using `$pr` alongside another
  `$p...` skill, as well as a Claude session for existing `/skill` behavior.

## Capabilities

### New Capabilities

- `web-compose-input`: Define paste-framed submission for the web compose bar.

### Modified Capabilities

- None.

## Impact

The web SPA's compose sender and Playwright coverage change. The REST input
endpoint and terminal clients in the TUI and macOS keep their existing byte
contract; no wire field or daemon behavior changes. This is a web compose-bar
fix, so no frontend follow-up is needed for the other two surfaces.

## Risk

Some CLI versions may treat pasted `$skill` text differently from a skill
selected in their picker. The live Codex check must confirm the submitted
request resolves to `pr` before this is shipped; if it does not, revise the
delivery method and this spec before implementation proceeds.
