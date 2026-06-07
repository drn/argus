# Project context

## What argus is

Argus is a terminal-native orchestrator for LLM coding agents. It runs a swarm of Claude Code and Codex sessions side by side, each isolated in its own git worktree, all under a single keyboard-driven TUI — and mirrors that swarm to a phone (PWA), another laptop, another agent (MCP server), or external scripts (REST API).

A persistent daemon keeps PTY sessions alive across TUI restarts and reboots. An idle detector promotes any agent waiting for input to "in review" so a glance at the task list shows who needs attention next.

## Tech stack

- **Language:** Go (module `github.com/drn/argus`, Go `1.26.0`).
- **UI:** tcell/tview, with direct cell painting for the agent terminal pane via the `charmbracelet/x/vt` emulator.
- **Persistence:** SQLite via `modernc.org/sqlite` (pure Go, no CGO) at `~/.argus/data.sql`.
- **PTY:** `creack/pty`.
- **Surfaces:** in-process runner, Unix-socket daemon, HTTP REST API + installable PWA (xterm.js) on port 7743, and an MCP server.

## Build, test, and quality gates

- **`make pre-pr` is the non-negotiable gate before opening OR updating any PR.** It mirrors `.github/workflows/ci.yml` step-for-step: build → vet → fmt-check → lint-pr → vuln → test-cover-gate. A green `make pre-pr` means CI will be green.
- **Coverage floor:** filtered coverage must stay at or above the **88%** floor (`make test-cover-gate`, `-min 88`). The floor ratchets up, never down — do not lower `-min`. Target platform-agnostic code for the most reliable margin (filtered coverage can drift ~0.2% between darwin and linux).
- **`make fmt`** runs goimports over the tree; **`make fmt-check`** fails CI if anything is unformatted (the most common CI miss).
- **`make lint-pr`** uses `--new-from-rev=origin/master`, so it only flags issues your diff introduces. Fix them; do not add blanket `//nolint`.

Common targets: `make build`, `make vet`, `make test` (`-race -count=1 ./...`), `make test-pkg PKG=...`, `make test-cover`, `make test-watch`.

## Testing norms

- **TDD red-green-refactor is the default workflow.** Write a failing test, write the minimum code to pass, then refactor while staying green.
- **Assertions go through `internal/testutil`**, not raw `if got != want`: `Equal`, `DeepEqual`, `NoError`, `ErrorIs`, `Nil`, `Contains`.
- **Every change must include tests.** No new function, branch, or error path without coverage. Table-driven tests use `t.Run` subtests; guard slow tests with `testing.Short()`.
- **Tests must never touch real `~/.argus/` paths or the live daemon.** Use `t.TempDir()`, `t.Setenv("HOME", t.TempDir())` for anything resolving through `$HOME`, and `agent.NewRunner(nil)` instead of a real daemon client.

## Conventions

- **Data dir:** `~/.argus/` (SQLite `data.sql`, daemon socket, worktrees, logs).
- **Breaking changes are fine.** Single user (the author) — no backwards-compatibility burden.
- **No legacy migration code.** A schema change that needs data migration gets a one-off script, not in-tree migration logic. Legacy JSON persistence and `config.toml` support have been removed.
- **Non-obvious invariants live in `context/knowledge/gotchas/`** (indexed by `context/knowledge/index.md`), read on demand rather than loaded automatically.

## Load-bearing note on OpenSpec in this repo

**OpenSpec specs in this repo are LOCAL DOCS only — nothing in CI, the Makefile, or the Go build depends on them.** The `openspec/` artifacts are authoring aids that get injected into PRs for the maintainer's workflow; no code reads them and they are strippable. Future change authors **MUST NOT** wire spec-validation (e.g. `openspec validate`) into the Go CI or any `make` target. The quality gate is and stays `make pre-pr` — build, vet, fmt-check, lint-pr, vuln, and the coverage gate. Keep spec tooling out of it.
