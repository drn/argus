package backendtier

import (
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
		logUnrecognizedStrategy(cfg.BackendRouting.Strategy)
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
//
// Valid = backend present in cfg.Backends and a recognized probe kind. A
// none-probe tier counts as "no reading" so it never outranks a backend with
// known headroom (fix-backend-usage-routing D6).
func resolveHeadroom(cfg config.Config) string {
	var (
		best         string
		bestScore    float64
		haveBest     bool
		firstUnknown string
	)
	for _, tier := range cfg.BackendRouting.Tiers {
		if _, ok := cfg.Backends[tier.Backend]; !ok {
			continue
		}
		var cached func() (float64, bool)
		switch tier.Probe {
		case config.ProbeNone:
			if firstUnknown == "" {
				firstUnknown = tier.Backend
			}
			continue
		case config.ProbeClaudeUsage:
			cached = claudeCachedPct
		case config.ProbeCodexUsage:
			cached = codexCachedPct
		default:
			continue
		}
		pct, ok := cached()
		if !ok {
			if firstUnknown == "" {
				firstUnknown = tier.Backend
			}
			continue
		}
		threshold := float64(tier.ThresholdPct)
		if pct >= threshold {
			continue
		}
		// Strictly greater: an equal score keeps the earlier tier.
		if score := threshold - pct; !haveBest || score > bestScore {
			best, bestScore, haveBest = tier.Backend, score, true
		}
	}
	if haveBest {
		return best
	}
	return firstUnknown
}

// loggedStrategies memoizes which unrecognized strategy values have already
// been logged, so a misconfigured config.toml produces one line per distinct
// value rather than one per resolution.
var (
	loggedStrategiesMu sync.Mutex
	loggedStrategies   = map[string]bool{}
)

// logUnrecognizedStrategy logs an unrecognized [backend_routing].strategy
// once per distinct value. slog reaches daemon.log (where resolution mostly
// runs); uxlog covers the TUI process.
func logUnrecognizedStrategy(strategy string) {
	loggedStrategiesMu.Lock()
	seen := loggedStrategies[strategy]
	loggedStrategies[strategy] = true
	loggedStrategiesMu.Unlock()
	if seen {
		return
	}
	slog.Warn("[backendtier] unrecognized backend_routing strategy, treating as ordered", "strategy", strategy)
	uxlog.Log("[backendtier] unrecognized backend_routing strategy %q, treating as ordered", strategy)
}
