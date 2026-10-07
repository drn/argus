## Why

The web Settings tab already edits the global `sandbox.extra_write` list, but `PUT /api/settings` accepted any string, including `/` (which grants writes everywhere) or paths that can never match. The TUI editor validates; the web did not. This corrects the earlier proposal's note that web editing was missing — only validation parity was.

## What Changes

- `agent.ValidateWritePath` is the single validator, used by the TUI editor and `PUT /api/settings`.
- `PUT /api/settings` returns 400 for an invalid `sandbox.extra_write` entry; the web UI surfaces it as a toast.
- `POST/PUT /api/projects` apply the same check to a project's `extra_write`.
- Web hint text clarifies accepted forms. `SW_VERSION` bumped.

## Non-Goals

- TUI project form and `config.toml` paths remain unvalidated; macOS Settings editor is a follow-up.
