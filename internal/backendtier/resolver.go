package backendtier

import (
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/usagebudget"
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
func ResolveBackend(cfg config.Config) string {
	for _, tier := range cfg.BackendRouting.Tiers {
		if _, ok := cfg.Backends[tier.Backend]; !ok {
			continue
		}
		if tierAvailable(tier) {
			return tier.Backend
		}
	}
	return ""
}

func tierAvailable(tier config.BackendTier) bool {
	switch tier.Probe {
	case config.ProbeNone:
		return true
	case config.ProbeClaudeUsage:
		return underThreshold(claudeCachedPct, tier.ThresholdPct)
	case config.ProbeCodexUsage:
		return underThreshold(codexCachedPct, tier.ThresholdPct)
	default:
		// Unknown probe kind: never available, but not an error — resolution
		// continues to the next tier (backend-tier-routing spec).
		return false
	}
}

// underThreshold reports whether a capped tier is available: below its
// threshold, or fail-open when the cached reading is missing/stale.
func underThreshold(cached func() (float64, bool), thresholdPct int) bool {
	pct, ok := cached()
	if !ok {
		return true
	}
	return pct < float64(thresholdPct)
}
