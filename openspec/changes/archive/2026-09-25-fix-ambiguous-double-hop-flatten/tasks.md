## Tasks

- [x] Add `Ambiguous bool` to `daemon.StartResp`.
- [x] `sessionCore.StartSession` sets `resp.Ambiguous` via `errors.Is(err, agent.ErrStartAmbiguous)`.
- [x] Outer `Client.Start` re-wraps `agent.ErrStartAmbiguous` when `resp.Ambiguous` is set, even when its own `callWithTimeout` didn't time out.
- [x] Bump `SupervisorStreamSurface` (2 → 3) with a history line; re-record `StreamSurfaceDigest`.
- [x] Add `TestSupInnerAmbiguous` (deterministic double-hop e2e test); verify it fails without the fix.
- [x] Fix wording in `CreateAndStart`'s ambiguous-error message (no longer implies automatic re-attach).
- [x] Tighten `ErrStartAmbiguous`'s doc comment on which `SessionProvider` implementations actually wrap it today.
- [x] Document the retry-races-original-completion gap explicitly in `daemon-rpc.md`.
- [x] Spec delta: add a double-hop scenario to `agent-execution`'s existing requirement.
- [x] Archive into `openspec/specs/agent-execution/spec.md` before merge.
- [x] `make pre-pr` clean.
