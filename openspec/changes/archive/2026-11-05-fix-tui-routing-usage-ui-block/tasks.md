## 1. Cached usage source
- [x] 1.1 `backendtier.CachedUsage` (non-blocking Read, single-flight background refresh, maxAge fail-open)
- [x] 1.2 `cmd/argus/main.go` installs it, primed before the UI loop
- [x] 1.3 Tests: slow fetch never blocks Read/ResolveChoice/resolveHeraTier

- [x] 1.4 `resolveDefaultBackendName` short-circuits an explicit/project backend (name-only callers never read usage)

## 2. Docs
- [x] 2.1 ui-threading gotcha; spec archived
