# Fix BUG-035: fullscreen agents miss free-text questions and navigated/AskUserQuestion choosers

## Why

On a FULLSCREEN (alt-screen) Claude agent, the session's raw-output clock never
quiesces (continuous cursor-addressed repaint), so `Session.IsIdle()` is never
true and the idle-gated detection pass — the only one that honors the
trailing-question heuristic (`endsInQuestion`) — never runs for it. The
never-idle content-stability pass was deliberately restricted (BUG-032) to the
UNAMBIGUOUS selection widget, EXCLUDING `endsInQuestion`. Two coverage gaps
follow, both LIVE-confirmed and pre-existing (NOT regressions):

- **GAP A:** a fullscreen worker parked at a FREE-TEXT question ("Should I go
  ahead? (yes/no)") is caught by NEITHER pass, so it never surfaces `?`.
- **GAP B:** the selection signature `❯[ \t]*1\.` is anchored to option 1, so a
  navigated permission cursor (`❯` on option 2/3) or a Claude AskUserQuestion
  chooser (plain options, cursor not on "1.") misses; the chooser-footer matcher
  was also too narrow to survive the real AskUserQuestion footer wording.

## What Changes

- **GAP B (selection coverage):**
  - The numbered-selection signature matches the cursor on ANY numbered option
    (`❯` + any digits + `.`), not only option 1 — so a navigated permission
    cursor is caught.
  - The AskUserQuestion chooser-footer matcher is widened to survive the real
    footer: an Enter-action affordance and an Esc-action affordance on the same
    footer line, tolerant of wording (`select`/`confirm`/`choose`), case, and
    the `·` / `↑/↓` navigation separators between them. The footer is the robust
    matcher because it is present regardless of which option is highlighted.
- **GAP A (free-text question on a never-idle session):**
  - The never-idle content-stability pass additionally flags a FREE-TEXT
    question (`endsInQuestion` on the EMULATED screen) — BUT only when the
    EMULATED screen shows the agent is GENUINELY AWAITING INPUT and NOT actively
    working. The discriminator is the ABSENCE of Claude's "working" affordance
    (the "esc to interrupt" / "ctrl+c to interrupt" hint Claude renders WHILE
    generating and removes at the idle input prompt), combined with content
    stability. Content stability ALONE is NOT sufficient (a busy agent that
    narrates a "?"-ending line and stalls on a spinner frame for a tick is
    content-stable) — the working-affordance-absent gate is the load-bearing
    guard that keeps this from re-breaking BUG-032.
- `Session.IsIdle()` / `RoleView.IsActive` / spinner precedence / idle-push are
  UNCHANGED — this is a local discriminator on the emulated screen inside the
  needs-input never-idle pass only.

## Impact

- Affected specs: `idle-detection`
- Affected code: `internal/agent/needsinput.go` (broadened selection + footer
  regexes; new working-affordance signal; `AwaitingInputFingerprint` replaces
  `SelectionPromptFingerprint` to power the never-idle pass with the gated
  free-text case), `internal/api/push.go` (never-idle pass call site),
  `internal/tui/app.go` (never-idle pass call site).
