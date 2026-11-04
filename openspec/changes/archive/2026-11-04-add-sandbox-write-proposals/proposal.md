## Why

A sandboxed agent that hits `Operation not permitted` on a legitimate directory (CocoaPods, Xcode DerivedData, `~/.expo`) has no way to ask for access. Today it can only tell the user to hand-edit config; the MCP exposes no sandbox write path and `argus_api_get` is GET-only. Letting agents widen their own sandbox would defeat it, so the missing piece is a *request* channel with a human-only approval step.

## What Changes

- New MCP tool `sandbox_propose_write_paths` (task resolved via `id`/`cwd`, batch of `{path, reason}`) that only records a pending proposal.
- New `sandbox_proposals` table; proposals readable via `GET /api/sandbox/proposals[...]` (so `argus_api_get` can show an agent its status).
- Human-only decision routes `POST /api/sandbox/proposals/{id}/approve|reject`, master-token only. No MCP tool approves, rejects, edits or applies a proposal.
- Approval (the ONLY code path that applies a proposal) writes the existing `extra_write` field: per-project (default) or global, approver's choice; approver may edit the path list.
- Shared validation on propose and approve: existing `agent.ValidateWritePath` plus a new sensitive/overbroad-root denylist (`~/.ssh`, `~/.aws`, `~/.gnupg`, `~/.config/gcloud`, `~/.netrc`, `~/Library/Keychains`, `~/.argus`, `~/Library`, `~/.claude`, `~/.codex`, and their parents/children). Malformed paths are rejected; sensitive/overbroad paths are ACCEPTED but flagged with a warning shown on the proposal and at approval time.
- Approval UI: macOS app panel (pending list with approve/edit/reject), TUI modal reached from the Settings Sandbox category (+ status-bar pending count), a web Settings panel. Both state that an approved path only applies to NEW sessions; running tasks must be restarted.
- Agent-facing guidance (builtin routing/skill text) tells agents to call the proposal tool instead of asking the user to edit config.

## Non-Goals

- Approval for manual edits: changes made directly in TUI Settings or web Settings stay immediate and need no proposal/approval. Only MCP-originated requests are gated.
- Hot-applying a grant to a running session (sandbox profile is fixed at spawn).
- Proposing read-deny, `enabled`, AppleEvents or cache-dir changes.
- Any auto-approve/allowlist policy.

## Impact

`internal/mcp`, `internal/api`, `internal/db` (new table), `internal/agent/sandbox.go` (validator), `internal/tui` (settings + modal), `internal/api/static` (+ `SW_VERSION` bump), `internal/apiclient`/`apistore` (remote TUI), docs/README Reference, `context/knowledge/gotchas/sandbox.md`.
