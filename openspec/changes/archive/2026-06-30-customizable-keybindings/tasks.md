# Tasks

## 1. keymap package
- [x] 1.1 `actions.go`: `Context`/`Action` consts (context-prefixed), `ActionLabel`, per-context ordered inventory for help.
- [x] 1.2 `parse.go`: `Parse(spec) (Binding, error)` + `Binding.String()` + `Binding.Matches`; named-key + ctrl-letter tables; reject ctrl+non-letter, ctrl+left/right, shift+letter.
- [x] 1.3 `keymap.go`: `Binding`, `Keymap`, `DefaultKeymap()`, `Build(config.Keybindings) (*Keymap, []Warning)`, `Resolve`, `Bindings`.
- [x] 1.4 Tests: parse valid/invalid, String round-trip, Matches/Resolve incl. loose cmd-arrow, Build validation warnings, DefaultKeymap completeness.

## 2. config + db
- [x] 2.1 Replace `config.Keybindings` with context-scoped maps; `DefaultKeybindings()` → empty.
- [x] 2.2 Drop the 9 keybinding rows from `db/config.go` + `db/migrate.go`; add idempotent `DELETE FROM config WHERE key LIKE 'keybindings.%'`.
- [x] 2.3 Update config/db tests.

## 3. dispatch refactor (pure contexts first)
- [x] 3.1 App owns keymap + `activeKeymap()` cache + accessor wiring to widgets.
- [x] 3.2 `handleGlobalKey` (rune + tcell.Key switches) → Resolve; structural keys stay literal.
- [x] 3.3 `tasklist.go`, `handleFilePanelKey`, `handleDiffKey`, `settings.go` → Resolve.
- [x] 3.4 `hera/page.go` handleRailMutation + regenerate `isRailMutationKey` from keymap; `rail.go`.

## 4. agent context (last) — regression-first
- [x] 4.1 Write red regression smoke tests (Ctrl+Z no PTY leak, Ctrl+Y conditional, Cmd-arrow no leak).
- [x] 4.2 Refactor `handleAgentKey` to Resolve; predicates stay in switch; Esc/Ctrl+Q/Ctrl+C literal.

## 5. help + docs
- [x] 5.1 Generate `HelpSections` from keymap; update `help_test.go` golden.
- [x] 5.2 README §Keybindings note + replace §`[keybindings]` config section.
- [x] 5.3 gotchas/keybindings.md invariants.

## 6. gate + archive
- [x] 6.1 `make pre-pr` green.
- [x] 6.2 Archive change: fold delta into `openspec/specs/keybindings/`, move folder to `changes/archive/<date>-customizable-keybindings/`.
