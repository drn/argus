## ADDED Requirements

### Requirement: Authenticated bridge delivery endpoints

The daemon SHALL expose POST /api/tasks/{id}/bridge/deliveries, /deliveries/authorize and /deliveries/ack under the existing authenticated task API. Pull and authorization SHALL require the current generation and boot; acknowledgment SHALL require its unguessable claim token. The daemon SHALL expose POST /api/tasks/{id}/bridge/budget for the coordinator Stop hook to enqueue an eligible nudge after its existing gates, returning enabled=false when the route is unavailable. Transport acknowledgment SHALL not mark durable mail read. These endpoints SHALL remain available to all frontends; web/macOS Bridge settings controls are a named follow-up.

#### Scenario: Disabled bridge delivery cannot authorize

- **WHEN** an authenticated mod requests authorization while bridge.delivery is false
- **THEN** the daemon returns authorized=false and the existing PTY path handles new deliveries

### Requirement: Bridge settings API round-trip

GET and PUT /api/settings SHALL expose a bridge section with enabled and delivery booleans. Partial updates SHALL leave absent keys unchanged and return effective configuration after the standard config.toml overlay. Remote TUI persistence SHALL use this section; web and macOS controls remain a named follow-up.

#### Scenario: Delivery-only update preserves master switch

- **WHEN** an authenticated client updates bridge.delivery alone
- **THEN** the API persists that value and returns the unchanged effective bridge.enabled value alongside it
