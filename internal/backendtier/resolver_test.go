package backendtier

import (
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
