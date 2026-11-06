## Why

`omp` (Oh My Pi) probes every provider's credentials at startup, including `stat('~/.aws/credentials')` for Bedrock, and only tolerates ENOENT. The sandbox profile's `(deny file-read* ... ~/.aws)` also blocks `file-read-metadata`, so an existing `~/.aws` makes that stat fail with EPERM and omp crashes on launch inside a sandboxed task.

## What Changes

- Add `(allow file-read-metadata (subpath ~/.aws))` immediately after the `~/.aws` read deny. Existence, size and mtime become visible; file contents and directory listings stay denied.
- The profile is shared by all backends, so every sandboxed agent gains metadata-only visibility of `~/.aws`.

## Capabilities

### Modified Capabilities

- `sandbox-execution`: credential read protection permits metadata (stat) on `~/.aws` while content reads stay blocked.

## Impact

- Code: `internal/agent/sandbox.go` profile text; tests in `internal/agent/sandbox_test.go`.
- No wire/REST/UI change; no frontend parity impact.
