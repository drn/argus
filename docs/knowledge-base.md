# Knowledge Base setup

The Argus knowledge base (KB) indexes a folder of Obsidian-style markdown files into a SQLite FTS5 store, serves it over MCP, and auto-injects that MCP server into every agent Argus spawns. Agents get your notes — design docs, meeting captures, durable preferences — as a first-class `kb_search` / `kb_read` lookup, and can write learnings back with `kb_ingest`.

This document is the setup/install guide. For the conceptual overview see the **Knowledge Base** pillar in the [README](../README.md#-knowledge-base); for the MCP tool surface see [MCP Tools](../README.md#mcp-tools).

## TL;DR

```bash
# 1. Enable the KB and point it at a vault — pick ONE:
#    (a) TUI:  Settings → Knowledge Base → Enter on "KB: Disabled", set Metis path
#    (b) config: edit ~/.argus/config.toml  ([kb] block below)

# 2. Restart the daemon so it picks up the change (starts the MCP server +
#    indexer, injects MCP config). Relaunching the TUI spawns a fresh daemon:
argus daemon stop          # then relaunch `argus` — the TUI auto-starts a new daemon

# 3. Verify — with the daemon running (these CLI calls talk to it over its socket):
argus kb status            # → document count, vault path, MCP port
argus kb search "onboarding"
```

The KB is **disabled by default** — nothing runs until you turn it on.

## What "enabling" actually does

When `kb.enabled` is true, the daemon (`argus daemon`) does three things on startup:

1. **Starts the MCP HTTP server** on `kb.http_port` (default `7742`; auto-probes upward if taken — `argus kb status` reports the actual port).
2. **Starts the vault indexer** against `kb.metis_vault_path`, doing a full scan then watching for changes (files dropped into the vault are searchable within seconds).
3. **Injects the MCP server into Claude Code and Codex globally** — writes the `argus` entry into `~/.claude.json` (`mcpServers.argus`) and `~/.codex/config.toml` (`[mcp_servers.argus]`), and trusts project MCP servers in `~/.claude/settings.json`. Because injection is global, every newly-spawned worktree agent inherits the toolset with no per-project config.

The FTS5 index lives in the same SQLite database as the rest of Argus state: `~/.argus/data.sql`. The vault on disk stays the source of truth; the index is rebuilt from it.

> The MCP server exposes both the **KB tools** and the **task/schedule/messaging tools**. It used to be named `argus-kb`; it's now just `argus`. Injection removes any stale `argus-kb` entry automatically, so an older setup upgrades cleanly.

## Installing Obsidian (optional)

Argus only reads markdown files off disk, so **Obsidian itself is not required** — `metis_vault_path` can point at any directory of `.md` files. Obsidian is the recommended way to author and organize that directory, and installing it is the simplest path to the default vault location.

1. Install Obsidian from [obsidian.md](https://obsidian.md) (or `brew install --cask obsidian` on macOS).
2. Create (or open) a vault. To use the Argus default path, enable **iCloud** sync and name the vault `Metis` — Obsidian stores iCloud vaults under `~/Library/Mobile Documents/iCloud~md~obsidian/Documents/`, which is exactly where Argus auto-discovers them. Any other location works too; just point `metis_vault_path` at it.
3. Add some notes (each a `.md` file with the [frontmatter schema](#document-schema) below). Obsidian writes the `.obsidian/` folder that Argus's vault discovery looks for.

If you'd rather not install Obsidian, just create a plain directory of markdown files and set `metis_vault_path` to it — everything else works the same. The vault won't show up in the Settings autocomplete (which only scans iCloud Obsidian vaults), but a typed absolute path is honored.

## Option A — enable in the TUI (recommended)

1. Open Argus and press `3` to reach the **Settings** tab (or cycle tabs with `Tab` / `Shift+Tab`), then select the **Knowledge Base** category.
2. The first row reads `KB: Disabled`. Press `Enter` to toggle it to `KB: Enabled`.
3. The second row is `Metis: <path>`. Press `Enter` to edit it. As you type, the field autocompletes from iCloud Obsidian vaults discovered on this machine (any directory containing a `.obsidian/` folder under `~/Library/Mobile Documents/iCloud~md~obsidian/Documents`). You can also type any absolute path — the vault does **not** have to be an iCloud/Obsidian vault; any directory of `.md` files works.
4. Both rows will show **`(restart required)`** until the daemon restarts with the new values.
5. Restart the daemon: `argus daemon stop`, then relaunch the TUI (it auto-starts a fresh daemon). Self-update from Settings also restarts the daemon.

## Option B — enable via `~/.argus/config.toml`

`config.toml` is an override layer applied on top of the database settings, so you can set the KB up entirely from the file:

```toml
[kb]
enabled          = true
http_port        = 7742
metis_vault_path = "/Users/you/Library/Mobile Documents/iCloud~md~obsidian/Documents/Metis"
```

| Key                | Type   | Default                              | Description                                  |
| ------------------ | ------ | ------------------------------------ | -------------------------------------------- |
| `enabled`          | bool   | `false`                              | Run the knowledge-base MCP server + indexer. |
| `http_port`        | int    | `7742`                               | KB / MCP server port (auto-probes upward).   |
| `metis_vault_path` | string | iCloud Obsidian `…/Documents/Metis`  | Directory of markdown files to index.        |

Restart the daemon after editing (`argus daemon stop`).

## Document schema

Every document is a markdown file with a leading YAML frontmatter block. `title` and `tags` drive retrieval and clustering:

```markdown
---
title: "Onboarding checklist"
tags: [process, onboarding, hr]
---

Lead with the key insight. Use ## H2 sections for subtopics.
Cross-reference related docs with [[wikilinks]].
```

- **Title** resolves by precedence: frontmatter `title` → first `# H1` in the body → filename stem.
- **Tags** are a flat lowercase, kebab-case list (inline `[a, b]` or comma-separated). Use them for thematic clustering; use the folder path for hierarchy.
- Keep entries focused (50–500 words retrieves best). One topic per file.

Missing frontmatter degrades gracefully (title falls back to the H1 or filename), but adding it sharpens search results.

## CLI reference

These talk to the running daemon over its Unix socket:

| Command                          | What it does                                                      |
| -------------------------------- | ----------------------------------------------------------------- |
| `argus kb status`                | Document count, configured vault path, and live MCP port.         |
| `argus kb search <query> [limit]`| Ranked full-text search with snippets (default limit 10).         |
| `argus kb list [--prefix P] [--limit N]` | Path-aware document listing.                              |
| `argus kb ingest <file>`         | Add or update a document from a local markdown file.              |

## MCP tools (what agents see)

Once enabled, every agent Argus spawns can call:

| Tool        | Description                                              |
| ----------- | ------------------------------------------------------- |
| `kb_search` | Full-text search with ranked results and snippets.      |
| `kb_read`   | Read a full document by vault-relative path.            |
| `kb_list`   | List documents with an optional path-prefix filter.     |
| `kb_ingest` | Add or update a document in the vault.                  |
| `kb_delete` | Remove a document by vault-relative path.               |

Agents are instructed to `kb_search` before writing so they update existing documents rather than creating duplicates.

## Troubleshooting

- **`argus kb status` says `MCP port: (not running)`** — the daemon isn't running with the KB enabled. Confirm `kb.enabled` is set (TUI Settings or `config.toml`) and that you restarted the daemon after the change.
- **`Vault: (not configured)`** — `metis_vault_path` is empty. Set it in Settings or `config.toml`.
- **Settings rows show `(restart required)`** — your changes are saved but not live yet. Run `argus daemon stop` and relaunch.
- **Document count is 0 but the vault has files** — confirm the path points at the directory containing the `.md` files (not its parent), and that the daemon process can read it (iCloud paths must be downloaded locally, not evicted placeholders).
- **An agent doesn't see the `kb_*` tools** — injection writes `~/.claude.json` / `~/.codex/config.toml` when the daemon starts with the KB enabled; restart the agent session so it re-reads its MCP config.
