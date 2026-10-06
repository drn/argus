## Why

Sandboxed agents can only write to a fixed set of paths. The global `sandbox.extra_write` list already exists (config, REST, SBPL emission) but the TUI Settings tab only displays it, so allowing something like `~/Downloads` for every process meant editing the DB or hitting the API by hand.

## What Changes

- The Settings → Sandbox category lists each global extra-write path as a row and supports add (`n`), edit (`e` / Enter), and delete (`d`) inline, persisting to `sandbox.extra_write` through the Store (local and `--remote`).
- Changes apply to tasks launched afterward; running sessions keep their existing profile.

## Non-Goals

- Web SPA editing of the global list (REST already accepts `extra_write`; macOS ArgusKit already models it). Named follow-up: web and macOS Settings editors.
- Per-project path editing (unchanged).
