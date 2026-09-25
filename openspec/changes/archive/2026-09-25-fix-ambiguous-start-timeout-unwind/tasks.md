## Tasks

- [x] Add `agent.ErrStartAmbiguous` sentinel to `internal/agent/iface.go`.
- [x] `Client.Start` wraps a `Daemon.StartSession` RPC timeout specifically (via `errors.Is(err, ErrRPCTimeout)`) in `agent.ErrStartAmbiguous`; other RPC errors pass through unwrapped.
- [x] `CreateAndStart`'s Step-5 error branch checks `errors.Is(err, agent.ErrStartAmbiguous)` and skips `unwind`, returning a distinguishing error instead of deleting the worktree/task row.
- [x] Update the `daemon-rpc.md` gotcha to replace the now-superseded flat-7-minute description with the ambiguous-timeout mechanism, and note the per-backend timeout (from `fix-pi-backend-start-timeout`) is safe now that a timeout doesn't destroy state.
- [x] Spec delta: redefine `agent-execution`'s "Slow prelaunch does not time out task startup" requirement around the ambiguous-timeout distinction.
- [x] Archive this change into `openspec/specs/agent-execution/spec.md` before merge.
- [x] `make pre-pr` clean.
