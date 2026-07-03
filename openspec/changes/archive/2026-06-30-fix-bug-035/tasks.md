# Tasks

## 1. Selection + footer coverage (internal/agent/needsinput.go) — GAP B

- [x] 1.1 Broaden `needsInputSelectionRe` from `❯[ \t]*1\.` to `❯[ \t]*\d+\.` (cursor on any numbered option).
- [x] 1.2 Widen `needsInputChooserFooterRe` to tolerate the real AskUserQuestion footer: an Enter-action affordance (`select`/`confirm`/`choose`) and an Esc-action affordance on the SAME footer line, case-insensitive, with navigation separators between them.
- [x] 1.3 Tests: navigated cursor on option 2/3 fires; cursor on option 1 still fires (no regression); chooser footer with the widened wording fires; the existing negative footer cases (split lines, lone phrase) still do NOT fire.

## 2. Free-text question on a never-idle session (internal/agent/needsinput.go) — GAP A

- [x] 2.1 Add `needsInputWorkingRe` — Claude's "working" affordance ("esc to interrupt" / "ctrl+c to interrupt"), present WHILE generating and absent at the idle input prompt.
- [x] 2.2 Add `awaitingInputText`: the unambiguous selection widget OR (`endsInQuestion` AND working-affordance ABSENT).
- [x] 2.3 Replace `SelectionPromptFingerprint` with `AwaitingInputFingerprint` (raw fast path + single emulated render) keyed on `awaitingInputText`; update both never-idle call sites.
- [x] 2.4 Tests (BOTH directions): parked free-text question (stable, no working affordance) → flagged; busy agent ("?"-ending line + "esc to interrupt" frame, stable 2 ticks) → NOT flagged (BUG-032 guard); fingerprint stable across animation-only ticks.

## 3. Never-idle call sites (push.go + app.go)

- [x] 3.1 `internal/api/push.go` `computeNeedsInput`: switch the content-stability pass to `AwaitingInputFingerprint`.
- [x] 3.2 `internal/tui/app.go` `detectNeedsInputSticky`: switch the content-stability pass to `AwaitingInputFingerprint`.
- [x] 3.3 Update the daemon/TUI tests: the existing "trailing-question working agent not flagged" fixture gains a real "esc to interrupt" working frame (it was indistinguishable from an awaiting agent); add an awaiting-question fixture that IS flagged.

## 4. Spec + docs

- [x] 4.1 Delta under `specs/idle-detection/spec.md`; `openspec validate fix-bug-035 --strict` passes.
- [x] 4.2 Document the gotcha in `context/knowledge/gotchas/events.md`; bump `index.md` count.

## 5. Verify

- [x] 5.1 `make test-pkg` green for `./internal/agent/`, `./internal/api/`, `./internal/tui/`.
- [x] 5.2 `make pre-pr` clean (fmt via local goimports, lint via pinned golangci-lint).
