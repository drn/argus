## Why

Argus now delivers agent-facing skills and hera/argus-task routing content to every spawned Claude session automatically: skill bodies via `internal/skills` + `--add-dir` (PR #866), routing prose via `internal/routing` + `--append-system-prompt-file` (PR #871). Both landed on master. The manual distribution path they superseded — `install-claude-skills.sh`/`uninstall-claude-skills.sh` symlinking `.claude/skills/*` into `~/.claude/skills/` and appending `claude/snippets/*.md` into `~/.claude/CLAUDE.md` — is now dead weight: no session needs to run it, and its continued presence documents a mechanism that no longer matches how the repo actually works.

## What Changes

- Delete `install-claude-skills.sh` and `uninstall-claude-skills.sh` from the repo root.
- Delete `claude/snippets/hera.md` and `claude/snippets/argus-tasks.md` (confirmed byte-identical to their embedded counterparts in `internal/routing/builtin/` before removal — no content lost).
- **BREAKING** (docs/spec only, no runtime behavior change): rewrite the `routing-provisioning` capability's "Builtin routing content bundle" requirement, which previously required the embedded content to be verified byte-identical against `claude/snippets/*.md` read off disk. That drift-guard test (`TestBuiltinContent_MatchesRepoSnippets`) is removed along with its source files; `internal/routing/builtin/*` is now the sole copy, not one of two.
- Update `README.md`'s "Agent-facing skills" section to describe the automatic mechanism (no install step) instead of the manual script.
- Update `internal/routing/routing.go`'s package comment and `context/knowledge/gotchas/misc.md` / `context/knowledge/index.md` to stop citing the retired files as sources of truth.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `routing-provisioning`: the "Builtin routing content bundle" requirement no longer requires (or can require) byte-identity against `claude/snippets/*.md`, since that manual source no longer exists. `internal/routing/builtin/*` becomes the sole, authoritative source.

## Impact

- **Code:** `internal/routing/routing_test.go` (drift-guard test removed), `internal/routing/routing.go` (comment), `internal/agent` unaffected (materialization/injection logic untouched — same behavior, no runtime change).
- **Docs:** `README.md`, `context/knowledge/gotchas/misc.md`, `context/knowledge/index.md`.
- **Deleted:** `install-claude-skills.sh`, `uninstall-claude-skills.sh`, `claude/snippets/hera.md`, `claude/snippets/argus-tasks.md` (and the now-empty `claude/` directory).
- **Not touched:** the user's own `~/.claude/` (skills/CLAUDE.md) — any symlinks or compiled blocks a user previously installed there are left alone; this change only removes the repo-side tooling that created them.
- **Known pre-existing gap, not fixed here:** `internal/skills/builtin/hera/SKILL.md` has already drifted from `.claude/skills/hera/SKILL.md` (PR #863's wording update wasn't mirrored into the embedded copy). Out of scope for this change; flagged separately.
