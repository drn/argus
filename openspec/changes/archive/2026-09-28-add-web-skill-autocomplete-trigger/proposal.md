## Why

The TUI's New Task form already picks the skill-autocomplete trigger character
per backend: `$` for Codex (`agent.IsCodexBackend`), `/` for everything else
(`internal/tui/newtaskform.go` `acTrigger()`, codified in
`openspec/specs/forms-and-modals/spec.md` under "Skill autocomplete in the
prompt"). This matches each CLI's own native convention — Codex's interactive
picker is bound to `$`, Claude Code's (and Pi's slash-prompt templates) to `/`.

The web SPA's equivalent dropdown (`createSkillAutocomplete` in
`internal/api/static/index.html`, shared by the New Task prompt and the
agent-view compose bar) hardcodes `/` unconditionally. For a Codex task, typing
`$pr` in the web compose bar still sends correctly (per
`openspec/specs/web-compose-input/spec.md`), but Argus's own suggestion
dropdown never opens, so the user gets no skill-name assist — a frontend
parity gap the project's AGENTS.md calls out explicitly ("Any user-facing
feature or behavior change must be evaluated against all three surfaces").

## What Changes

- `createSkillAutocomplete` accepts a `getTrigger` thunk (defaults to `'/'`
  when omitted) instead of hardcoding `/`. `tokenAtCursor`/`update`/`select`
  use the resolved trigger character instead of the literal `/`.
- A shared `isCodexBackend(command)` JS helper mirrors
  `agent.IsCodexBackend`'s basename-of-first-word check, so the logic isn't
  duplicated ad hoc at each call site.
- The New Task prompt's autocomplete instance resolves its trigger from the
  currently-selected `#create-backend` option (looked up in
  `createBackendsCache` for its `command`), and re-evaluates when the backend
  select changes (an open dropdown for the old trigger is closed rather than
  left stale).
- The agent-view compose bar's autocomplete (`composeAC`) resolves its
  trigger from `currentTask.backend` (looked up in `createBackendsCache`).
- No change to send-time behavior (already spec'd in `web-compose-input`) or
  to the skill list/filter/fetch logic — this only changes which character
  opens the dropdown and which character `select()` re-inserts.

## Capabilities Affected

- `mobile-pwa` (ADDED): backend-conditional skill-autocomplete trigger in the
  web New Task prompt and the agent-view compose bar.
