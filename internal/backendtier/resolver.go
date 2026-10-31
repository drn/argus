package backendtier

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/usagebudget"
	"github.com/drn/argus/internal/uxlog"
)

// claudeCachedPct / codexCachedPct are indirected through package-level vars
// (rather than called directly) purely as a test seam: overriding them lets
// resolver tests exercise every dispatch branch without mutating either
// probe's real, shared, package-level cache.
var (
	claudeCachedPct = usagebudget.CachedClaudePct
	codexCachedPct  = CachedCodexPct
)

// ResolveBackend walks cfg.BackendRouting.Tiers in order and returns the name
// of the first tier that is available: an uncapped ("none") tier is always
// available, a capped tier is available when its probe's cached usage is
// below its threshold OR the cached reading is stale/unknown (fail open — an
// unprobeable tier never blocks resolution). A tier naming a backend absent
// from cfg.Backends, or carrying an unrecognized probe kind, is skipped.
//
// Returns "" when no tier list is configured, every tier is skipped, or every
// capped tier is at/over threshold with no uncapped tier in the list —
// callers fall back to their own existing single-default-backend precedence.
func ResolveBackend(cfg config.Config, accounts ...string) string {
	account := ""
	if len(accounts) > 0 {
		account = accounts[0]
	}
	return ResolveChoice(cfg, "", account).Backend
}

func claudeUsageForAccount(accounts []string) func() (float64, bool) {
	if len(accounts) == 0 || accounts[0] == "" || accounts[0] == "default" {
		return claudeCachedPct
	}
	return func() (float64, bool) { return usagebudget.CachedClaudePctForAccount(accounts[0]) }
}

var warnedStrategies sync.Map

// warnUnknownStrategy logs an unrecognized strategy once per distinct value;
// slog so it reaches daemon.log, where resolution usually runs.
func warnUnknownStrategy(strategy string) {
	if _, seen := warnedStrategies.LoadOrStore(strategy, struct{}{}); seen {
		return
	}
	msg := fmt.Sprintf("[backendtier] unrecognized backend_routing strategy %q; treating as %q", strategy, config.StrategyOrdered)
	uxlog.Log("%s", msg)
	slog.Warn(msg)
}
