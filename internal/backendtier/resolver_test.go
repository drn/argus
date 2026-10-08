package backendtier

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
)

// withCachedPct temporarily overrides both probe-kind cached-reading seams,
// restoring the real functions on test cleanup.
func withCachedPct(t *testing.T, claude, codex func() (float64, bool)) {
	t.Helper()
	origClaude, origCodex := claudeCachedPct, codexCachedPct
	claudeCachedPct, codexCachedPct = claude, codex
	t.Cleanup(func() { claudeCachedPct, codexCachedPct = origClaude, origCodex })
}

func known(pct float64) func() (float64, bool) {
	return func() (float64, bool) { return pct, true }
}

func unknown() func() (float64, bool) {
	return func() (float64, bool) { return 0, false }
}

func testBackends() map[string]config.Backend {
	return map[string]config.Backend{
		"claude": {Command: "claude"},
		"codex":  {Command: "codex"},
		"pi":     {Command: "pi"},
	}
}

func TestResolveBackend_FirstTierUnderThresholdWins(t *testing.T) {
	withCachedPct(t, known(10), unknown())
	cfg := config.Config{
		Backends: testBackends(),
		BackendRouting: config.BackendRoutingConfig{Tiers: []config.BackendTier{
			{Backend: "claude", Probe: config.ProbeClaudeUsage, ThresholdPct: 80},
			{Backend: "codex", Probe: config.ProbeCodexUsage, ThresholdPct: 80},
		}},
	}
	testutil.Equal(t, ResolveBackend(cfg), "claude")
}

func TestResolveBackend_FirstTierOverThresholdFallsThrough(t *testing.T) {
	withCachedPct(t, known(95), known(10))
	cfg := config.Config{
		Backends: testBackends(),
		BackendRouting: config.BackendRoutingConfig{Tiers: []config.BackendTier{
			{Backend: "claude", Probe: config.ProbeClaudeUsage, ThresholdPct: 80},
			{Backend: "codex", Probe: config.ProbeCodexUsage, ThresholdPct: 80},
		}},
	}
	testutil.Equal(t, ResolveBackend(cfg), "codex")
}

func TestResolveBackend_AllCappedExhaustedFallsThroughToUncapped(t *testing.T) {
	withCachedPct(t, known(95), known(95))
	cfg := config.Config{
		Backends: testBackends(),
		BackendRouting: config.BackendRoutingConfig{Tiers: []config.BackendTier{
			{Backend: "claude", Probe: config.ProbeClaudeUsage, ThresholdPct: 80},
			{Backend: "codex", Probe: config.ProbeCodexUsage, ThresholdPct: 80},
			{Backend: "pi", Probe: config.ProbeNone},
		}},
	}
	testutil.Equal(t, ResolveBackend(cfg), "pi")
}

func TestResolveBackend_AllCappedExhaustedNoUncappedReturnsEmpty(t *testing.T) {
	withCachedPct(t, known(95), known(95))
	cfg := config.Config{
		Backends: testBackends(),
		BackendRouting: config.BackendRoutingConfig{Tiers: []config.BackendTier{
			{Backend: "claude", Probe: config.ProbeClaudeUsage, ThresholdPct: 80},
			{Backend: "codex", Probe: config.ProbeCodexUsage, ThresholdPct: 80},
		}},
	}
	testutil.Equal(t, ResolveBackend(cfg), "")
}

func TestResolveBackend_NoTierListConfigured(t *testing.T) {
	cfg := config.Config{Backends: testBackends()}
	testutil.Equal(t, ResolveBackend(cfg), "")
}

func TestResolveBackend_StaleOrUnknownReadingTreatedAsAvailable(t *testing.T) {
	withCachedPct(t, unknown(), unknown())
	cfg := config.Config{
		Backends: testBackends(),
		BackendRouting: config.BackendRoutingConfig{Tiers: []config.BackendTier{
			{Backend: "claude", Probe: config.ProbeClaudeUsage, ThresholdPct: 80},
		}},
	}
	testutil.Equal(t, ResolveBackend(cfg), "claude")
}

func TestResolveBackend_UnknownProbeKindSkippedNotError(t *testing.T) {
	withCachedPct(t, unknown(), unknown())
	cfg := config.Config{
		Backends: testBackends(),
		BackendRouting: config.BackendRoutingConfig{Tiers: []config.BackendTier{
			{Backend: "claude", Probe: "gemini_usage", ThresholdPct: 80},
			{Backend: "pi", Probe: config.ProbeNone},
		}},
	}
	testutil.Equal(t, ResolveBackend(cfg), "pi")
}

func TestResolveBackend_TierNamesNonexistentBackendSkipped(t *testing.T) {
	cfg := config.Config{
		Backends: testBackends(),
		BackendRouting: config.BackendRoutingConfig{Tiers: []config.BackendTier{
			{Backend: "gemini", Probe: config.ProbeNone},
			{Backend: "pi", Probe: config.ProbeNone},
		}},
	}
	testutil.Equal(t, ResolveBackend(cfg), "pi")
}

func TestResolveBackend_EveryTierMisconfiguredDegradesToEmpty(t *testing.T) {
	cfg := config.Config{
		Backends: testBackends(),
		BackendRouting: config.BackendRoutingConfig{Tiers: []config.BackendTier{
			{Backend: "gemini", Probe: config.ProbeNone},
			{Backend: "grok", Probe: config.ProbeClaudeUsage, ThresholdPct: 80},
		}},
	}
	testutil.Equal(t, ResolveBackend(cfg), "")
}

func TestResolveBackend_CacheReadNeverBlocksResolution(t *testing.T) {
	calls := 0
	withCachedPct(t, func() (float64, bool) {
		calls++
		return 10, true
	}, unknown())
	cfg := config.Config{
		Backends: testBackends(),
		BackendRouting: config.BackendRoutingConfig{Tiers: []config.BackendTier{
			{Backend: "claude", Probe: config.ProbeClaudeUsage, ThresholdPct: 80},
		}},
	}
	testutil.Equal(t, ResolveBackend(cfg), "claude")
	testutil.Equal(t, calls, 1)
}

// --- fix-backend-usage-routing: routing strategy ---------------------------

func routingCfg(strategy string, tiers ...config.BackendTier) config.Config {
	return config.Config{
		Backends:       testBackends(),
		BackendRouting: config.BackendRoutingConfig{Strategy: strategy, Tiers: tiers},
	}
}

func claudeTier(threshold int) config.BackendTier {
	return config.BackendTier{Backend: "claude", Probe: config.ProbeClaudeUsage, ThresholdPct: threshold}
}

func codexTier(threshold int) config.BackendTier {
	return config.BackendTier{Backend: "codex", Probe: config.ProbeCodexUsage, ThresholdPct: threshold}
}

// Scenario: Absent strategy keeps ordered behavior.
func TestResolveBackend_AbsentStrategyIsOrdered(t *testing.T) {
	// Codex has more headroom, but ordered mode returns the first available tier.
	withCachedPct(t, known(40), known(20))
	testutil.Equal(t, ResolveBackend(routingCfg("", claudeTier(70), codexTier(80))), "claude")
}

// Scenario: Unrecognized strategy is treated as ordered (and logged, once per
// distinct value, through slog so it reaches daemon.log).
func TestResolveBackend_UnrecognizedStrategyIsOrderedAndLogged(t *testing.T) {
	readSlog := captureSlog(t)
	withCachedPct(t, known(40), known(20))
	// Unique values per run so a package-level log-once memo can't mask this.
	bogus := "bogus-" + t.Name()
	other := "other-" + t.Name()

	testutil.Equal(t, ResolveBackend(routingCfg(bogus, claudeTier(70), codexTier(80))), "claude")
	testutil.Equal(t, ResolveBackend(routingCfg(bogus, claudeTier(70), codexTier(80))), "claude")
	testutil.Equal(t, ResolveBackend(routingCfg(other, claudeTier(70), codexTier(80))), "claude")

	log := readSlog()
	testutil.Contains(t, log, bogus)
	testutil.Equal(t, strings.Count(log, bogus), 1)
	testutil.Contains(t, log, other)
}

func TestResolveBackend_Headroom(t *testing.T) {
	for _, tc := range []struct {
		name          string
		claude, codex func() (float64, bool)
		tiers         []config.BackendTier
		want          string
	}{
		{
			// Scenario: Most headroom wins (claude 70−40=30, codex 80−20=60).
			name:   "most headroom wins",
			claude: known(40), codex: known(20),
			tiers: []config.BackendTier{claudeTier(70), codexTier(80)},
			want:  "codex",
		},
		{
			// Scenario: Ties break by list order. The leading none-probe tier
			// has no reading, so it never outranks a known-headroom tier.
			name:   "ties break by list order",
			claude: known(30), codex: known(50),
			tiers: []config.BackendTier{{Backend: "pi", Probe: config.ProbeNone}, codexTier(80), claudeTier(60)},
			want:  "codex",
		},
		{
			// Scenario: Known reading beats unknown reading.
			name:   "known reading beats unknown",
			claude: known(10), codex: unknown(),
			tiers: []config.BackendTier{codexTier(80), claudeTier(80)},
			want:  "claude",
		},
		{
			// Scenario: All unknown falls back to list order (first VALID tier:
			// a tier naming an absent backend is skipped).
			name:   "all unknown falls back to list order",
			claude: unknown(), codex: unknown(),
			tiers: []config.BackendTier{{Backend: "gemini", Probe: config.ProbeNone}, codexTier(80), claudeTier(80)},
			want:  "codex",
		},
		{
			// Scenario: Over-threshold tier is excluded.
			name:   "over-threshold tier excluded",
			claude: known(75), codex: unknown(),
			tiers: []config.BackendTier{claudeTier(70), codexTier(80)},
			want:  "codex",
		},
		{
			// At exactly the threshold counts as over.
			name:   "at threshold excluded",
			claude: known(70), codex: unknown(),
			tiers: []config.BackendTier{claudeTier(70), codexTier(80)},
			want:  "codex",
		},
		{
			// Scenario: Every tier over threshold → empty (caller falls back to
			// the default-backend precedence).
			name:   "every tier over threshold",
			claude: known(95), codex: known(90),
			tiers: []config.BackendTier{claudeTier(70), codexTier(80)},
			want:  "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withCachedPct(t, tc.claude, tc.codex)
			testutil.Equal(t, ResolveBackend(routingCfg(config.StrategyHeadroom, tc.tiers...)), tc.want)
		})
	}
}
