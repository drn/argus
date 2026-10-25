## ADDED Requirements

### Requirement: Read-only daemon API tool

The server SHALL expose an `argus_api_get` tool when the daemon has wired the REST API into the MCP server. The tool SHALL accept a single `path` argument and perform a GET against the daemon's REST API in-process through the same router and handlers that serve HTTP clients. It SHALL NOT accept a method argument and SHALL NOT dispatch any route that is not registered as a `GET /api/...` route. Streaming routes SHALL be refused. The caller SHALL be treated as a non-master authenticated principal, so master-only handlers refuse. A 2xx response SHALL return the response body as the tool result; any other status SHALL be a tool error carrying the status and body. The tool SHALL NOT read, send, or return the API token, and any occurrence of the master token in a response body SHALL be redacted. The tool description SHALL list the commonly useful routes.

#### Scenario: GET route returns the body

- **WHEN** an agent calls `argus_api_get` with `path` `/api/tasks`
- **THEN** the tool result is the JSON body the REST handler produced

#### Scenario: Unknown route is a clean error

- **WHEN** the path matches no `GET /api/` route
- **THEN** the tool returns an error naming HTTP 404 and does not return dashboard HTML

#### Scenario: Mutating routes are unreachable

- **WHEN** the path names a route that exists only for POST, PUT, or DELETE
- **THEN** the tool returns an error and no handler runs

#### Scenario: Path traversal and absolute URLs are rejected

- **WHEN** the path contains `..` (raw or percent-encoded), a scheme or host, a backslash, a fragment, or control characters
- **THEN** the tool returns a validation error before any routing

#### Scenario: Master-only routes refuse

- **WHEN** the path names a GET route guarded by the master-token check
- **THEN** the tool returns the handler's 403 as an error

#### Scenario: Token never appears in output

- **WHEN** a handler's response body contains the master token
- **THEN** the tool output contains `[redacted]` in its place

#### Scenario: Tool is absent until wired

- **WHEN** the daemon has not wired the REST API (API disabled)
- **THEN** `tools/list` does not include `argus_api_get`
