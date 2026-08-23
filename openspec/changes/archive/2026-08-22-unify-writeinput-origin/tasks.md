## 1. Unify the Go API

- [x] 1.1 Add `InputOrigin` type + `OriginUser`/`OriginSystem` constants to `internal/app/agentview/terminal.go`; widen `TerminalAdapter.WriteInput` to take it, remove `WriteInputSystem`.
- [x] 1.2 Widen `agent.SessionHandle.WriteInput` (`internal/agent/iface.go`) identically; remove `WriteInputSystem` from the interface.
- [x] 1.3 Unify `agent.Session.WriteInput`/`WriteInputSystem` (`internal/agent/session.go`) into one method; update `lastInput`/`lastUserInput` doc comments.
- [x] 1.4 Widen `notify.SessionHandleIface` (`internal/notify/types.go`); update the 3 reliable-notify delivery calls in `internal/notify/service.go` to pass `agentview.OriginSystem`.
- [x] 1.5 Update TUI call sites: `internal/tui/app.go` (Ctrl+C, Escape, generic key forward — all `OriginUser`), `internal/tui/heraactions.go` (`heraDoBounceWorker` — `OriginSystem`), `internal/tui/terminal/terminalpane.go` (wheel-forward + paste — `OriginUser`; `forwardEmulatorResponse` — `OriginSystem`), `internal/tui/hera/panes.go` (pane key forward — `OriginUser`).
- [x] 1.6 Unify `daemon/client.RemoteSession.WriteInput`/`WriteInputSystem` (`internal/daemon/client/handle.go`); rework `inputLoop`/`drainInput` so origin-mismatched queued items are never coalesced into the same RPC (carried to the next call instead).
- [x] 1.7 Unify `apiclient.Session.WriteInput`/`WriteInputSystem` (`internal/apiclient/session.go`); thread origin through `apiclient.Client.WriteInput` (`internal/apiclient/terminal.go`) as the `X-Input-Origin` header.
- [x] 1.8 Update every test fake implementing one of the widened interfaces (agent, notify, tui, tui/terminal, tui/hera, daemon, daemon/client, api, apiclient packages).

## 2. Wire protocol

- [x] 2.1 Add `WriteReq.Origin agentview.InputOrigin` (`internal/daemon/types.go`); bump `ProtocolVersion` 4→5 with a version-history bullet documenting the additive-safety rationale.
- [x] 2.2 `sessionCore.WriteInput` (`internal/daemon/sessioncore.go`) passes `req.Origin` through to the real session unchanged.
- [x] 2.3 REST `handleWriteInput` (`internal/api/handlers.go`) reads `X-Input-Origin`, defaulting to `OriginUser` when absent or unrecognized.
- [x] 2.4 `apiclient.Client.WriteInput` sends `X-Input-Origin: system` only for `OriginSystem` (omitted — not `"user"` — for `OriginUser`, matching the pre-existing wire shape).

## 3. Tests

- [x] 3.1 `internal/agent/session_test.go`: rename/adapt the BUG-034 pinning test to the unified signature; add explicit origin args to all other `WriteInput` calls in the file.
- [x] 3.2 `internal/notify/service_test.go`: extend `fakeSession` to record origins; add `TestNotifier_DeliveryUsesOriginSystem` asserting every reliable-notify write uses `OriginSystem`.
- [x] 3.3 `internal/tui/heraactions_test.go`: extend `recordingWriteSession` to record origins; assert `heraDoBounceWorker`'s write uses `OriginSystem`.
- [x] 3.4 `internal/daemon/client/handle_test.go`: rewrite `TestDrainInput` for the `inputItem`/`(batch, carry)` signature; add a subtest proving an origin boundary stops coalescing and returns the item as `carry`.
- [x] 3.5 `internal/apiclient/client_test.go`: assert `OriginUser` sends no `X-Input-Origin` header and `OriginSystem` sends `X-Input-Origin: system`.
- [x] 3.6 `internal/api/handlers_test.go`: end-to-end REST test asserting `X-Input-Origin: system` advances `LastInput` but not `LastUserInput` on the real session, and that an absent header still advances both (unchanged default).

## 4. Docs

- [x] 4.1 Document the "two systems, not tech debt, but needed structural enforcement" resolution, the unified API, and the wire-protocol finding in `context/knowledge/gotchas/pty-terminal.md` and `context/knowledge/gotchas/daemon-rpc.md`.

## 5. Archive

- [x] 5.1 Merge this change's delta specs into the base specs and archive the change folder in this same PR (`context/knowledge` / `CLAUDE.md` archive-atomically requirement).
