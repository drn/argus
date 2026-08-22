## Why

`Session.WriteInput` (a real human keystroke) and `Session.WriteInputSystem` (argus-injected input — reliable-notify delivery, a hera bounce instruction, a live emulator's auto-answered terminal capability query) are both genuinely necessary: the needs-input heuristics in `internal/agent/needsinput.go` and `internal/tui/app.go` depend on telling human input apart from system-injected input (BUG-034). That is not the defect.

The defect is that nothing in the API **structurally** forces a caller to pick the right one — the distinction is naming-convention-only, and picking the wrong one has now happened **twice**: originally BUG-034 itself (reliable-notify delivery, fixed by inventing `WriteInputSystem` in commit `62040591`), and again in `fix-live-emulator-writeinput` (the live emulator's query-response forwarder used `WriteInput` where `WriteInputSystem` was needed, producing a focus-gated needs-input flicker — see `context/knowledge/gotchas/pty-terminal.md`).

Separately, an audit of every call site found a second, lower-severity but real gap: the daemon RPC (`WriteReq`) and the REST `POST /input` payload carry **no origin field at all**, so a system-origin write crossing either process boundary (TUI→daemon, daemon→supervisor, or the remote-TUI REST path) silently degrades to plain user-input semantics on the far side — defeating BUG-034's fix in exactly the topologies where it crosses a process boundary.

## What Changes

- **Unify `WriteInput` / `WriteInputSystem` into one method with a mandatory origin parameter**: `WriteInput(p []byte, origin agentview.InputOrigin) (int, error)`, where `InputOrigin` is a two-value type (`agentview.OriginUser` / `agentview.OriginSystem`). Go has no default arguments, so every call site must state an origin explicitly — the class of "picked the wrong overload" bug this proposal is fixing cannot recur via a forgotten default. This is a breaking, non-back-compat signature change (per this repo's Breaking Changes Policy) applied to `agent.SessionHandle`, `agentview.TerminalAdapter`, `notify.SessionHandleIface`, `daemon/client.RemoteSession`, `apiclient.Session`, and `agent.Session` itself, plus every call site and test fake.
- **Thread the origin across the daemon RPC and the REST `/input` path**, closing the cross-process gap:
  - `daemon.WriteReq` gains an `Origin agentview.InputOrigin` field. Its zero value is `agentview.OriginUser` — the only behavior any pre-existing peer ever had — so an old daemon/supervisor talking to a new one (field simply absent from its own struct, ignored by JSON decode) and a new one talking to an old-shaped request (field decodes as its zero value) both behave exactly as before. `ProtocolVersion` bumps 4→5 per this repo's existing "bump on any new optional field" convention (see `internal/daemon/types.go`'s v3 `BinaryHash`/`VCS` precedent) — a documentation/doctor-visible marker, not a hard compatibility gate (mismatches are already advisory-only via `SupervisorProtocolMatch`).
  - The REST `POST /input` endpoint reads an optional `X-Input-Origin: system` header (any other value, or its absence, resolves to `OriginUser` — the endpoint's only prior behavior). The apiclient's REST session (`internal/apiclient/session.go`, the `--remote` TUI path) now sends this header for `OriginSystem` writes, closing the same gap on that hop.
- No behavioral change for any existing caller: every prior `WriteInput` call becomes `WriteInput(p, agentview.OriginUser)`; every prior `WriteInputSystem` call becomes `WriteInput(p, agentview.OriginSystem)`.
- The daemon-client `RemoteSession`'s input-coalescing (`inputLoop`/`drainInput`) now stops coalescing at an origin boundary, in addition to the existing bracketed-paste boundary — merging a `System`-origin and a `User`-origin write into a single RPC would misattribute one write's origin to the other's bytes.

## Wire-protocol compatibility finding (requested verification)

The R/S session protocol (`internal/daemon/sessioncore.go`, mounted by both the daemon and the session-supervisor) runs over `net/rpc/jsonrpc` — plain JSON-encoded request/response structs, not gob or a custom binary framing. A new field on an existing request struct is safely additive in both directions under `encoding/json`: an old peer's struct simply lacks the field (ignored on encode/decode), and a new peer decoding an old-shaped payload gets the field's zero value. Since `agentview.OriginUser` (zero value) is the only behavior any pre-`v5` peer ever exhibited, this is genuinely a **moderate, additive** change exactly as anticipated — no daemon/supervisor restart is required for the mixed-version case to behave safely, though a `System`-origin write only take effect once **both** sides of a given RPC hop are rebuilt. `ProtocolVersion` is bumped per the repo's stated convention (any new optional field bumps it) purely as a version-history/doctor-visible marker — `SupervisorProtocolMatch`'s mismatch handling stays advisory-only, unchanged by this proposal.

## Capabilities

### Modified Capabilities

- `agent-execution`: "Input forwarding records activity only on success" now specifies the mandatory `InputOrigin` parameter and its two distinct timestamp effects, replacing the two-method design.
- `daemon-client`: "Forwarding terminal input" now specifies that origin is threaded through the RPC explicitly, with the wire-compatibility contract above.
- `rest-api`: "Terminal output, input, and resize" now specifies the `X-Input-Origin` header and its default.

## Impact

- **Modified code:** `internal/app/agentview/terminal.go` (new `InputOrigin` type), `internal/agent/{iface,session}.go`, `internal/notify/{types,service}.go`, `internal/tui/{app,heraactions}.go`, `internal/tui/terminal/terminalpane.go`, `internal/tui/hera/panes.go`, `internal/daemon/{types,sessioncore}.go`, `internal/daemon/client/handle.go`, `internal/apiclient/{session,terminal,client}.go`, `internal/api/handlers.go`, plus every test fake implementing one of the widened interfaces.
- **No new daemon RPC method** — `WriteReq` gains one additive field on the existing `WriteInput` method.
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make / Go-build wiring is added or changed. The quality gate stays `make pre-pr`.
