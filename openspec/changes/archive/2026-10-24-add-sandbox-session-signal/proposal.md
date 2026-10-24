## Why

Permission hooks currently guess that an agent is sandboxed from its worktree path. Unsandboxed sessions can use the same paths, so hooks need an explicit signal from the launcher.

## What changes

- Export `ARGUS_SANDBOXED=1` only for commands actually wrapped in `sandbox-exec`, across all backends and fresh, resumed, and recycled launches.
- Remove inherited or configured values of that reserved variable before determining the launch result.
- Update supervisor spawn compatibility metadata and regression coverage.

## Capabilities

### New capabilities

None.

### Modified capabilities

- `sandbox-execution`: Add an authoritative launch environment signal.

## Impact

Shared agent command construction and supervisor spawn-surface metadata change. The companion ai-ron permission-hook PR depends on this contract and must merge afterward. REST, TUI, web, and macOS interfaces do not change; every frontend's launches use the same command builder.
