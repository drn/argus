# Fix OpenCode startup and slow backend prelaunch

## Problem

Argus's default OpenCode command is `opencode --prompt <task prompt>`. With the installed OpenCode v2.0.16, the full-screen TUI leaves that text in its composer without submitting it. A fresh Argus task therefore shows a running OpenCode process but never starts a conversation. Reproducing the same command outside Argus produced no session until Enter was pressed; `opencode mini --prompt` submitted the prompt and remained interactive.

The screenshot's `daemon RPC call timed out` error came from a separate Pi task created seconds later. Pi's Ollama prelaunch took about 34 seconds, while both TUI-to-daemon and daemon-to-supervisor `StartSession` calls use the generic two-second RPC deadline. Argus unwound the task and deleted its worktree while the supervisor was still preparing the process.

## Proposed change

- Launch the built-in OpenCode backend with `opencode mini` so a new task's `--prompt` is submitted on startup. Keep model selection, session capture, `--session` resume, sandbox choice, and user-defined backend commands working through the existing paths. The user's custom `opencode` backend command remains authoritative.
- Give `StartSession` a deadline longer than Pi's bounded prelaunch budget at both RPC hops. Keep the short deadline for ordinary RPCs. A prelaunch failure still propagates as an error and triggers transactional cleanup after the server responds.
- Add tests for the generated OpenCode command and the long-running start path. Document the startup and timeout invariants in the relevant gotcha files.
- Read OpenCode v2's `session_v2` table during post-exit session capture, while retaining the v1 SQLite and legacy JSON fallbacks.

## Frontend parity

The backend default and session RPC are shared by the TUI, web, and macOS clients. This change adds no REST field or endpoint. All three surfaces continue to create tasks through the same daemon-side start path where applicable.

## Non-goals

- Changing the default OpenCode permission posture or the user's OpenCode configuration.
- Reworking OpenCode's full-screen TUI or attempting to synthesize an Enter key at startup.
- Changing the Pi/Ollama prelaunch process itself.
- **Named follow-up: `slow-prelaunch-rest-client-deadlines`** — align the remote TUI's 30-second HTTP client, the web task-create timeout, and macOS URLSession timeouts with the six-minute Pi prelaunch budget. These are independent HTTP client deadlines beyond the two session RPC hops fixed here.
