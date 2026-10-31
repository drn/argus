package backendtier

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/usagebudget"
	"github.com/drn/argus/internal/uxlog"
)

func TestAccountTierChoiceAndPins(t *testing.T) {
	withCachedPct(t, known(96), known(21))
	restore := usagebudget.SetBridgeReader(func(account string) (usagebudget.Reading, bool) {
		return usagebudget.Reading{Percentage: 0}, account == "personal"
	})
	defer restore()
	cfg := config.Config{Backends: testBackends(), Accounts: map[string]config.Account{"personal": {ClaudeConfigDir: t.TempDir()}}, BackendRouting: config.BackendRoutingConfig{Strategy: config.StrategyHeadroom, Tiers: []config.BackendTier{
		{Backend: "claude", Probe: config.ProbeClaudeUsage, ThresholdPct: 90},
		{Backend: "claude", Account: "personal", Probe: config.ProbeClaudeUsage, ThresholdPct: 90},
		{Backend: "codex", Probe: config.ProbeCodexUsage, ThresholdPct: 80},
	}}}
	for _, tc := range []struct{ name, backend, account, wantBackend, wantAccount string }{
		{"automatic", "", "", "claude", "personal"},
		{"explicit backend", "claude", "", "claude", "personal"},
		{"pinned default", "", "default", "codex", "default"},
		{"pinned personal", "claude", "personal", "claude", "personal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			choice := ResolveChoice(cfg, tc.backend, tc.account)
			testutil.Equal(t, choice.Backend, tc.wantBackend)
			testutil.Equal(t, choice.Account, tc.wantAccount)
		})
	}
	cfg.BackendRouting.Tiers[1].Account = "missing"
	testutil.Equal(t, ResolveChoice(cfg, "", "").Backend, "codex")
}

func TestInvalidTierAccountWarnsOnBothChannels(t *testing.T) {
	for _, tc := range []struct {
		name, backend string
		account       config.Account
	}{
		{"unsupported", "codex", config.Account{ClaudeConfigDir: t.TempDir()}},
		{"invalid path", "claude", config.Account{ClaudeConfigDir: "relative"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uxlog.Close()
			path := filepath.Join(t.TempDir(), "ux.log")
			testutil.NoError(t, uxlog.Init(path))
			defer uxlog.Close()
			var logs bytes.Buffer
			old := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			defer slog.SetDefault(old)
			cfg := config.Config{Backends: testBackends(), Accounts: map[string]config.Account{"bad": tc.account}, BackendRouting: config.BackendRoutingConfig{Tiers: []config.BackendTier{{Backend: tc.backend, Account: "bad", Probe: config.ProbeNone}, {Backend: "pi", Probe: config.ProbeNone}}}}
			testutil.Equal(t, ResolveChoice(cfg, "", "").Backend, "pi")
			contents, err := os.ReadFile(path)
			testutil.NoError(t, err)
			testutil.Contains(t, string(contents), "skipping backend=")
			testutil.Contains(t, logs.String(), "skipping backend=")
		})
	}
}

func TestNamedCodexNeverBorrowsDefaultReading(t *testing.T) {
	withCachedPct(t, unknown(), known(0))
	cfg := config.Config{Backends: testBackends(), Accounts: map[string]config.Account{"other": {CodexHome: t.TempDir()}}, BackendRouting: config.BackendRoutingConfig{Strategy: config.StrategyHeadroom, Tiers: []config.BackendTier{{Backend: "codex", Account: "other", Probe: config.ProbeCodexUsage, ThresholdPct: 80}, {Backend: "claude", Probe: config.ProbeClaudeUsage, ThresholdPct: 90}}}}
	withCachedPct(t, known(20), known(0))
	testutil.Equal(t, ResolveChoice(cfg, "", "").Backend, "claude")
}
