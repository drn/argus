# Knowledge Index

Non-obvious invariants and gotchas, split by topic. Read the relevant file when working in that area.

| File | Topic | Bullets |
|------|-------|---------|
| [gotchas/daemon-rpc.md](gotchas/daemon-rpc.md) | Daemon lifecycle, RPC timeouts, reconciliation races, session resume | 26 |
| [gotchas/pty-terminal.md](gotchas/pty-terminal.md) | PTY sizing, x/vt emulator, ring buffer, replay cache, paint cache, lazyScreen | 31 |
| [gotchas/ui-threading.md](gotchas/ui-threading.md) | tview thread safety, tick goroutine rules, paste/input batching | 10 |
| [gotchas/sandbox.md](gotchas/sandbox.md) | macOS sandbox-exec SBPL profiles, symlink resolution, allowed paths | 11 |
| [gotchas/worktree.md](gotchas/worktree.md) | Worktree creation ordering, cleanup, path validation, stale ref pruning | 8 |
| [gotchas/keybindings.md](gotchas/keybindings.md) | Key routing, ctrl sequences, tcell modifier quirks, agent view navigation | 11 |
| [gotchas/tasklist-ui.md](gotchas/tasklist-ui.md) | Task list cursor, modals, focus guards, filter, archive, spinner | 24 |
| [gotchas/misc.md](gotchas/misc.md) | DB patterns, Go idioms, Codex, MCP, todos, PRs, file explorer, vault, quick-add | 60 |

## Other Files

| File | Purpose |
|------|---------|
| [code-quality.md](code-quality.md) | Historical refactoring log (archive — not loaded by default) |
| [reference-bt-migration.md](reference-bt-migration.md) | Pre-tcell BT reference commit (5b8d560) |
