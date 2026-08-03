## Why

A coordinator that notices a bound role has gone quiet must currently make two calls to reach it: `hera_revive` to wake the session, then `hera_send` to actually deliver the message. The two-step dance is easy to forget — a coordinator sends first, gets no response, and only later thinks to check whether the recipient's session is even alive. `hera_revive` already encodes the exact safety gate needed (dead → restart, stuck-but-idle → kick, busy/blocked/live-coordinator → leave alone) via the shared `internal/hera.ReviveRole` primitive. `hera_send` should just call it automatically so a coordinator never has to remember the separate step.

## What Changes

- `hera_send` (`internal/mcp/hera.go`, `toolHeraSend`), when the caller is a coordinator sending to an explicitly named `to` recipient (not the worker/freelance default-to-coordinator path, and not itself), now attempts a revive of that recipient BEFORE delivering the message — reusing `s.heraRevive`/`internal/hera.ReviveRole` verbatim, the exact same primitive `hera_revive` already calls. No new gating logic is introduced.
- The attempt is soft-fail and best-effort: a recipient with no live binding (a planned node, never spawned, or ended role), a lookup error, a revive error, or `heraRevive` not being wired (daemon didn't configure a reviver) all skip the auto-revive step silently (Info/Debug log at most) and the message send proceeds regardless.
- On a successful revive attempt, the `hera_send` tool response gains a `- **revive**: <outcome>` line (rendered via the existing `heraReviveOutcomeMessage`) alongside the existing `message_id`/`to`/`delivery_mode` lines, so the coordinator learns in one round-trip whether the recipient was dead, stuck, or already fine. The line is omitted entirely when no revive attempt was made.
- A `slog.Info("[hera] revive", ...)` line is emitted matching `toolHeraRevive`'s existing one, so an auto-triggered revive is indistinguishable in logs from a manual `hera_revive` call.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-messaging`: the "hera_send recipient resolution and defaults" requirement's sibling gains a new requirement, "hera_send auto-revives a dead or stuck recipient," describing the coordinator-only, explicit-`to`-only auto-revive attempt and its soft-fail semantics.

## Impact

- `internal/mcp/hera.go` (`toolHeraSend`: auto-revive attempt wired between recipient resolution and `s.heraSvc.Send`)
- `internal/mcp/hera_test.go` (new test cases)
- `context/knowledge/gotchas/messaging.md`, `context/knowledge/index.md`
- No schema/data migration, no new dependencies, no new MCP tool, no REST/API surface change, no TUI behavior change. Reuses `internal/hera.ReviveRole` and `heraReviveOutcomeMessage` exactly as `hera_revive` already does — no changes to `internal/hera/revive.go`.
