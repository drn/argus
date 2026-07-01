# Detect binary skew across TUI, daemon, and supervisor

## Why

`go install ./cmd/argus/` can leave the TUI, daemon, and session-supervisor running different builds (the `go install` target, the `PATH` `argus`, and the `~/.argus/argusd` symlink can resolve to different files). The skew silently breaks every key that needs the TUI↔daemon round-trip (Enter to attach, Ctrl+Q to detach) while local keys keep working — a state users cannot self-diagnose, and one that survives a daemon restart when the binaries diverge by path. Today's staleness check covers only the daemon, is skipped on the auto-start path, and never names the paths.

## What Changes

- Relay the **supervisor's** binary identity (hash + resolved path + VCS revision/dirty flag) to the TUI through the daemon's `BootInfo`; add `BinaryHash` + VCS to `HelloResp` and bump the additive R/S `ProtocolVersion` 2→3.
- Add commit-SHA + dirty-flag + path to the identity model, read from `debug.ReadBuildInfo` (no `-ldflags` wiring). Keep the SHA-256 content hash as the authoritative stale/not-stale signal; VCS + path are display-only.
- Add **`argus doctor`**: a read-only command that enumerates every argus binary on disk, what each process runs, and a green/red verdict — distinguishing *restart needed* (same path, different hash) from *path divergence* (different files — the real footgun) — with the exact fix command for each.
- Extend the startup detection + modal: compute **supervisor** staleness even on the auto-start path (daemon staleness stays gated on connecting to a pre-existing daemon); show rich identity for whichever process is stale; offer a **double-confirmed** supervisor restart that names the count of agents it will interrupt. Surface stays the blocking modal only (no persistent banner).

## Capabilities

### New Capabilities

- **binary-coherence** — detecting and diagnosing TUI/daemon/supervisor binary skew: the identity model, the cross-process identity relay, the `argus doctor` diagnostic, and the startup skew prompt.

### Modified Capabilities

None. No existing requirement text changes; the staleness modal and supervisor handshake were previously unspecced, so this change formalizes them as new requirements.

## Impact

- **Code:** `internal/daemon/types.go` (`HelloResp`, `BootInfoResp`, `ProtocolVersion`), `internal/daemon/supervisor.go` + `internal/daemon/rpc.go` + `internal/daemon/daemon.go` (identity capture + relay), `internal/daemon/client/client.go` (`Hello`/`BootInfo` plumbing), `cmd/argus/main.go` (`isDaemonStale` split, supervisor branch, new `doctor` subcommand), `internal/tui/app.go` + `internal/tui/modal/` (rich-identity modal + supervisor double-confirm restart).
- **Protocol:** additive `ProtocolVersion` 2→3; forward/backward compatible with the existing additive-only contract.
- **No data migration, no new external dependency, no flag.** Remote (`--remote`) mode is unaffected (out of scope).
- **Docs:** `argus doctor` added to the README Reference; a gotcha entry on the skew failure mode and the path-divergence verdict.
