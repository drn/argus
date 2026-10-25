## Context

`internal/mcp` cannot import `internal/api` (api already imports mcp for the plugin registry). The daemon starts the MCP server before the API server, and either can be disabled independently (`cfg.KB.Enabled`, `cfg.API.Enabled`).

## Decisions

1. **In-process dispatch, not loopback HTTP.** `api.Server.ReadOnlyGet(ctx, path)` builds the request against the server's own mux and records the response. No token is read, no auth header is sent, no port is needed (the API port can be auto-bumped), and the Tailscale/loopback binding rules are untouched. Rejected: an HTTP client to `127.0.0.1` with the token file — it would hold the master token inside the MCP process for no benefit.
2. **Dependency inversion via a function.** `mcp.APIGetter func(ctx, path) (APIGetResult, error)` is injected with `SetAPIGetter` after the API server exists (daemon passes `apiSrv.ReadOnlyGet`). The tool is advertised only when set. The setter is mutex-guarded because it is called after the MCP listener is already serving.
3. **Route allowlist by registered pattern, not by string checks.** The mux is asked which pattern matches; only patterns beginning `GET /api/` run. This is needed because the mux's `GET /` dashboard catch-all would otherwise answer unknown paths with 200 HTML. Streaming patterns (`/api/events/stream`, `/api/tasks/{id}/stream`) are refused since an in-process recorder would block until timeout.
4. **Least-privilege caller identity.** Requests are stamped `X-Argus-Auth: device`, so `requireMaster` handlers (token list, backends CRUD, update) refuse (e.g. the `GET /api/tokens` roster). A caller that needs a master-only read still has to use the REST API directly.
5. **Defense in depth on the token.** The tool never touches the token, but the body is scanned and any master-token substring replaced with `[redacted]` in case a handler ever echoes it.
6. **Bounds.** 15s context timeout, 1 MiB body cap with an explicit truncation marker, panic recovery around the handler.
7. **Path normalization.** `tasks`, `/tasks`, `api/tasks`, `/api/tasks` all mean `/api/tasks`. `..` (raw or percent-decoded), backslash, `://`, a leading `//`, `#`, and control characters are rejected before routing. The query string is passed through unchanged.

## Risks

- A future GET handler with side effects would be reachable. Mitigation: the existing convention (GET = read); the device-tier identity limits blast radius; reviewers adding a GET route should keep it side-effect free.
- Device-tier identity means `/api/bridge/...` routes must not be master-gated if they should be readable; revisit when the bridge stack lands.
