## Tasks

- [x] Add a per-backend start-timeout table (`startTimeoutOverrides`) and resolver (`startTimeoutFor`) to `internal/daemon/client/client.go`, keyed by the existing `Is<Backend>Backend` predicates.
- [x] Give pi a 20s override (`piStartTimeout`); everything else keeps the default `rpcTimeout`.
- [x] Wire `Client.Start` to call `startTimeoutFor(task, cfg)` instead of the flat `rpcTimeout`.
- [x] Unit test `startTimeoutFor` directly: pi backend, non-pi backend, unresolvable backend, empty config.
- [x] Document the daemon-side pi/ollama prelaunch + client-timeout interaction in `context/knowledge/gotchas/misc.md`.
- [x] Add a spec delta scenario under `daemon-client`'s existing "RPC calls are bounded by a timeout" requirement.
- [x] Archive this change into `openspec/specs/daemon-client/spec.md` before merge.
- [x] `make pre-pr` clean.
