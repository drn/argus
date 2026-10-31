package config

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestAutoAccount_IsSelectable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := testAccountsCfg()

	testutil.NoError(t, cfg.ValidateAccount(AutoAccountName))
	testutil.Equal(t, IsAutoAccount("auto"), true)
	testutil.Equal(t, IsAutoAccount("personal"), false)
	testutil.Equal(t, cfg.AccountSupports(AutoAccountName, "codex"), true)

	t.Run("resolves from global default", func(t *testing.T) {
		c := cfg
		c.DefaultAccount = AutoAccountName
		testutil.Equal(t, c.ResolveAccount("", "other"), AutoAccountName)
	})
	t.Run("resolves from project entry", func(t *testing.T) {
		c := cfg
		c.ProjectAccounts = map[string]string{"p": AutoAccountName}
		testutil.Equal(t, c.ResolveAccount("", "p"), AutoAccountName)
	})
	t.Run("project pin beats global auto", func(t *testing.T) {
		c := cfg
		c.DefaultAccount = AutoAccountName
		testutil.Equal(t, c.ResolveAccount("", "work-proj"), "abs")
	})
	t.Run("explicit auto kept", func(t *testing.T) {
		testutil.Equal(t, cfg.ResolveAccount(AutoAccountName, "work-proj"), AutoAccountName)
	})
	t.Run("never listed as a name", func(t *testing.T) {
		for _, n := range cfg.AccountNames() {
			testutil.Equal(t, n == AutoAccountName, false)
		}
	})
	t.Run("has no directories", func(t *testing.T) {
		_, _, err := cfg.ClaudeConfigDir(AutoAccountName)
		testutil.Error(t, err)
		_, _, err = cfg.CodexHome(AutoAccountName)
		testutil.Error(t, err)
	})
}

func TestAutoAccount_CannotBeDefined(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := Config{Accounts: map[string]Account{"auto": {ClaudeConfigDir: "/opt/auto"}}}
	err := cfg.ValidateAccount(AutoAccountName)
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "reserved")
	testutil.DeepEqual(t, cfg.AccountNames(), []string{DefaultAccountName})
}

func TestAccountRoutingThresholds(t *testing.T) {
	tests := []struct {
		name               string
		five, weekly       int
		wantFive, wantWeek int
	}{
		{"unset defaults", 0, 0, 90, 90},
		{"custom", 80, 95, 80, 95},
		{"bounds inclusive", 1, 100, 1, 100},
		{"out of range ignored", -5, 101, 90, 90},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := AccountRoutingConfig{FiveHourThresholdPct: tc.five, WeeklyThresholdPct: tc.weekly}
			testutil.Equal(t, r.FiveHourThreshold(), tc.wantFive)
			testutil.Equal(t, r.WeeklyThreshold(), tc.wantWeek)
		})
	}
}

func TestAccountRouting_ParseFromTOML(t *testing.T) {
	path := writeFile(t, `
default_account = "auto"

[account_routing]
five_hour_threshold_pct = 85
weekly_threshold_pct = 95
`)
	l := NewFileLoader(path)
	got := l.Apply(DefaultConfig())
	testutil.NoError(t, l.Err())
	testutil.Equal(t, got.DefaultAccount, AutoAccountName)
	testutil.Equal(t, got.AccountRouting.FiveHourThreshold(), 85)
	testutil.Equal(t, got.AccountRouting.WeeklyThreshold(), 95)
}
