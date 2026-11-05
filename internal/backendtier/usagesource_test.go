package backendtier

import (
	"errors"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/usagebudget"
)

func personalCfg(strategy string, tiers ...config.BackendTier) config.Config {
	cfg := routingCfg(strategy, tiers...)
	cfg.Accounts = map[string]config.Account{"personal": {ClaudeConfigDir: "/tmp/argus-personal-test"}}
	return cfg
}

func claudeAcct(account string, threshold int) config.BackendTier {
	return config.BackendTier{Backend: "claude", Account: account, Probe: config.ProbeClaudeUsage, ThresholdPct: threshold}
}

func installSource(t *testing.T, u Usage, err error) *int {
	t.Helper()
	calls := 0
	restore := SetUsageSource(func() (Usage, error) { calls++; return u, err })
	t.Cleanup(restore)
	return &calls
}

// The reported bug: the TUI's local caches are empty (every tier unread), so
// headroom picked the first tier (default account at 98%). With the daemon's
// readings it must pick the personal account.
func TestResolveChoice_UsesInstalledSourceNotLocalCaches(t *testing.T) {
	withCachedPct(t, unknown(), unknown())
	calls := installSource(t, Usage{Claude: map[string]usagebudget.Reading{
		"default":  {Percentage: 98},
		"personal": {Percentage: 70},
	}}, nil)
	cfg := personalCfg(config.StrategyHeadroom, claudeAcct("", 90), codexTier(80), claudeAcct("personal", 90))
	got := ResolveChoice(cfg, "", "")
	testutil.Equal(t, got.Backend, "claude")
	testutil.Equal(t, got.Account, "personal")
	testutil.Equal(t, *calls, 1) // one RPC per resolution, however many tiers
}

func TestResolveChoice_SourceCodexReading(t *testing.T) {
	withCachedPct(t, unknown(), unknown())
	installSource(t, Usage{
		Claude: map[string]usagebudget.Reading{"default": {Percentage: 95}},
		Codex:  &usagebudget.Reading{Percentage: 10},
	}, nil)
	got := ResolveChoice(routingCfg(config.StrategyHeadroom, claudeTier(90), codexTier(80)), "", "")
	testutil.Equal(t, got.Backend, "codex")
}

func TestResolveChoice_SourceFailureFallsBackToLocalCaches(t *testing.T) {
	withCachedPct(t, known(10), unknown())
	installSource(t, Usage{}, errors.New("daemon gone"))
	got := ResolveChoice(routingCfg(config.StrategyHeadroom, codexTier(80), claudeTier(90)), "", "")
	testutil.Equal(t, got.Backend, "claude") // local claude reading beats unknown codex
}

func TestSetUsageSource_RestoreAndNil(t *testing.T) {
	withCachedPct(t, known(10), unknown())
	restore := SetUsageSource(func() (Usage, error) { return Usage{}, nil })
	restore()
	got := ResolveChoice(routingCfg(config.StrategyHeadroom, codexTier(80), claudeTier(90)), "", "")
	testutil.Equal(t, got.Backend, "claude")
	SetUsageSource(nil)()
}

// A missing reading ranks below any real reading with headroom, but above a
// real reading that is over threshold.
func TestHeadroom_MissingReadingRanking(t *testing.T) {
	tiers := []config.BackendTier{codexTier(80), claudeTier(90)}
	t.Run("known with headroom beats unknown", func(t *testing.T) {
		withCachedPct(t, known(85), unknown()) // claude 5 headroom
		testutil.Equal(t, ResolveBackend(routingCfg(config.StrategyHeadroom, tiers...)), "claude")
	})
	t.Run("unknown beats known over threshold", func(t *testing.T) {
		withCachedPct(t, known(99), unknown())
		testutil.Equal(t, ResolveBackend(routingCfg(config.StrategyHeadroom, tiers...)), "codex")
	})
}

func TestHeadroom_AllOverThresholdLeastUsed(t *testing.T) {
	withCachedPct(t, known(98), unknown())
	installSource(t, Usage{Claude: map[string]usagebudget.Reading{
		"default": {Percentage: 98}, "personal": {Percentage: 93},
	}}, nil)
	cfg := personalCfg(config.StrategyHeadroom, claudeAcct("", 90), claudeAcct("personal", 90))
	got := ResolveChoice(cfg, "", "")
	testutil.Equal(t, got.Account, "personal")
	testutil.Contains(t, got.Reason, "all tiers over threshold; least-used")

	// A pin or backend restriction still constrains the candidate set.
	pinned := ResolveChoice(cfg, "", "default")
	testutil.Equal(t, pinned.Account, "default")
	testutil.Equal(t, pinned.Backend, "claude")
}

// Ordered strategy keeps its documented "all exhausted → empty" contract.
func TestOrdered_AllOverThresholdStillEmpty(t *testing.T) {
	withCachedPct(t, known(99), known(99))
	testutil.Equal(t, ResolveBackend(routingCfg(config.StrategyOrdered, claudeTier(90), codexTier(80))), "")
}

func TestLocalUsage(t *testing.T) {
	restore := usagebudget.SetBridgeReader(func(account string) (usagebudget.Reading, bool) {
		return usagebudget.Reading{Percentage: 42}, account == "personal"
	})
	defer restore()
	cfg := personalCfg(config.StrategyHeadroom)
	u := LocalUsage(cfg)
	testutil.Equal(t, u.Claude["personal"].Percentage, 42.0)
	_, hasDefault := u.Claude["default"]
	testutil.Equal(t, hasDefault, false)
}
