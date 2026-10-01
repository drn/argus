## Why

On 2026-10-01 the daemon ran protocol v7 against a v6 session-supervisor. Both surface components were behind, but `argus doctor` printed only the stream message (stream outranked spawn), so the operator concluded the skew was a cosmetic rendering concern. The real breakage was spawn-side: a pre-v7 supervisor ignores `MCPPort`, so every newly started session came up without argus MCP tools. The spawn message also never listed argus MCP wiring among its consequences.

## What Changes

- Add a combined both-stale verdict so doctor reports the stream AND spawn consequences when both surface components differ, with one remediation line.
- Add argus MCP wiring to the enumerated spawn-surface consequences.
- Diagnostic/reporting layer only: no surface constant or `ProtocolVersion` changes, so surface coherence (and the supervisor's exemption from the hash loop) is unaffected.

## Capabilities

### New Capabilities

- None.

### Modified Capabilities

- `binary-coherence`: report every stale supervisor surface component, not just the highest-ranked one.

## Impact

- `internal/doctor/doctor.go` and `surface_test.go`; daemon-rpc gotchas.
