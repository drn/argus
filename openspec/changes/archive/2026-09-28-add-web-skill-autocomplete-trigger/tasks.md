## 1. Trigger-conditional autocomplete

- [x] 1.1 Add an `isCodexBackend(command)` JS helper in
  `internal/api/static/index.html` mirroring
  `internal/agent/agent.go`'s `IsCodexBackend` (basename of the first
  whitespace-delimited word of `command` equals `codex`).
- [x] 1.2 `createSkillAutocomplete({ input, dropdown, getProject, getTrigger })`:
  add `getTrigger` (default `() => '/'`); replace the hardcoded `'/'` in
  `tokenAtCursor`'s caller (`update`'s `token.startsWith('/')` check) and in
  `select`'s `replacement` string with `getTrigger()`.
- [x] 1.3 Wire the New Task prompt's instantiation (line ~6444) with a
  `getTrigger` that reads `#create-backend`'s value, looks it up in
  `createBackendsCache`, and returns `$` when `isCodexBackend(b.command)` else
  `/`.
- [x] 1.4 Wire `composeAC`'s instantiation (line ~6450) with a `getTrigger`
  that reads `currentTask.backend`, looks it up in `createBackendsCache`, and
  returns `$`/`/` the same way.
- [x] 1.5 On the `#create-backend` `change` handler, call `close()` (via the
  handle already returned by `createSkillAutocomplete`) so a dropdown open for
  the previous backend's trigger doesn't linger stale.

## 2. Tests

- [x] 2.1 `cmd/argus-test-server` / Playwright (or existing JS test harness,
  whichever this repo's static assets use) coverage: typing `$p` opens the
  dropdown for a Codex-backed compose bar and not for a Claude-backed one;
  `/re` still opens for Claude; switching the New Task backend select closes
  an open dropdown.
- [x] 2.2 If `isCodexBackend` logic is added as a small pure JS helper with no
  existing JS unit-test harness in this repo, confirm via manual `run`
  verification in the browser (start the dev server, exercise both New Task
  and an active Codex task's compose bar) instead, and note that in the PR.

## 3. Docs

- [x] 3.1 Add a bullet to `context/knowledge/gotchas/web-remote.md` noting the
  compose-bar/new-task autocomplete trigger is now backend-conditional
  (mirrors the TUI's `acTrigger`), alongside the existing skill-autocomplete
  bullets there.
