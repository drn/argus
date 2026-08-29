# Allow writes to ~/.argus/cache in the sandbox profile

## Why

`BuildCmd` forces `GOCACHE` and `PLAYWRIGHT_BROWSERS_PATH` to `~/.argus/cache/{go-build,ms-playwright}` for every spawned agent, redirecting them out from under `~/Library/Caches` (the TCC re-prompt fix). The sandbox profile's "Build tool caches" allow-list was never updated to match: it allows `~/Library/Caches`, `~/go`, `~/.npm`, `~/.cache`, etc., but never `~/.argus`. Under `(deny default)` this means a sandboxed agent's first `go build`/`go test` (or Playwright browser install) gets EPERM creating the very directory it was just redirected to — the redirect fix silently regressed for any sandboxed agent, and this was reproduced directly in this session (`mkdir /Users/darrencheng/.argus/cache: operation not permitted` when running `go build`/`go test`).

## What Changes

- Add `(allow file-write* (subpath (string-append (param "HOME") "/.argus/cache")))` to `sandboxProfileBase`, scoped to `~/.argus/cache` (not all of `~/.argus`, which also holds `data.sql` and the daemon socket).

## Impact

- Affected spec: `sandbox-execution` (Scoped tool, cache, and browser write access requirement)
- Affected code: `internal/agent/sandbox.go`
