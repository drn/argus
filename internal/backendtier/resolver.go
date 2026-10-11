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
func ResolveBackend(cfg config.Config) string {
	switch cfg.BackendRouting.Strategy {
	case config.StrategyHeadroom:
		return resolveHeadroom(cfg)
	case "", config.StrategyOrdered:
	default:
		warnUnknownStrategy(cfg.BackendRouting.Strategy)
	}
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

// resolveHeadroom picks, among valid capped tiers with a fresh reading below
// threshold, the one with the largest threshold−pct headroom (ties → list
// order); failing that, the first tier with no usable reading (stale/unknown
// capped tiers and none-probe tiers) in list order; failing that, "".
func resolveHeadroom(cfg config.Config) string {
	var (
		best        string
		bestScore   float64
		haveBest    bool
		firstUnread string
	)
	for _, tier := range cfg.BackendRouting.Tiers {
		if _, ok := cfg.Backends[tier.Backend]; !ok {
			continue
		}
		var cached func() (float64, bool)
		switch tier.Probe {
		case config.ProbeNone:
		case config.ProbeClaudeUsage:
			cached = claudeCachedPct
		case config.ProbeCodexUsage:
			cached = codexCachedPct
		default:
			continue
		}
		pct, known := 0.0, false
		if cached != nil {
			pct, known = cached()
		}
		if !known {
			if firstUnread == "" {
				firstUnread = tier.Backend
			}
			continue
		}
		score := float64(tier.ThresholdPct) - pct
		if score <= 0 {
			continue
		}
		if !haveBest || score > bestScore {
			best, bestScore, haveBest = tier.Backend, score, true
		}
	}
	if haveBest {
		return best
	}
	return firstUnread
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
