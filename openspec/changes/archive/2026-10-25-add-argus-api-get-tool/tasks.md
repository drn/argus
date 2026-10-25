## 1. Tests first

- [x] 1.1 `internal/api`: ReadOnlyGet tests (GET routes work, unknown route 404, POST-only route refused, dashboard catch-all not served, stream refused, traversal/scheme/host/control rejected, master-only handler refused, token redacted, body cap)
- [x] 1.2 `internal/mcp`: tool tests (disabled until wired, listed when wired, missing/blank path, 2xx returns body, non-2xx is a tool error, validation error surfaces)

## 2. Implementation

- [x] 2.1 `internal/api/readonly.go`: `ReadOnlyGet` + path normalization
- [x] 2.2 `internal/mcp/apiget.go`: `APIGetter`, `SetAPIGetter`, tool def, handler; wire into `tools/list` and `tools/call`
- [x] 2.3 `internal/daemon/daemon.go`: inject after the API server is created

## 3. Docs

- [x] 3.1 README MCP tool table
- [x] 3.2 `argus-schedule` builtin skill: use the tool for its read-only calls
- [x] 3.3 gotcha entry in `context/knowledge/gotchas/misc.md` + index count

## 4. Verify

- [x] 4.1 `make pre-pr`
- [ ] 4.2 Rebase on master after the bridge stack lands; confirm `/api/tasks/{id}/bridge`, `/bridge/ui`, `/api/bridge/shadow` resolve
- [x] 4.3 Archive the change in the PR (`openspec archive add-argus-api-get-tool`)
