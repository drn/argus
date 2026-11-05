## 1. Daemon-sourced readings
- [x] 1.1 `backendtier.SetUsageSource` + `LocalUsage`; ResolveChoice reads one snapshot per resolution
- [x] 1.2 `Daemon.RoutingUsage` RPC + `client.RoutingUsage`; TUI installs the source in `cmd/argus/main.go`
- [x] 1.3 Fallback to local caches + uxlog on RPC failure

## 2. Headroom fallback
- [x] 2.1 Least-used pick when all known tiers are over threshold; reason logged
- [x] 2.2 Regression tests for missing-reading ranking

## 3. Docs
- [x] 3.1 Gotcha in usage-budget-routing.md; spec archived
