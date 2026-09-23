package db

import (
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
)

func TestDB_BackendTiers_EmptyByDefault(t *testing.T) {
	d := testDB(t)
	tiers, err := d.BackendTiers()
	testutil.NoError(t, err)
	testutil.Equal(t, len(tiers), 0)
}

func TestDB_BackendTiers_RoundTripPreservesOrder(t *testing.T) {
	d := testDB(t)
	want := []config.BackendTier{
		{Backend: "claude", Probe: config.ProbeClaudeUsage, ThresholdPct: 80},
		{Backend: "codex", Probe: config.ProbeCodexUsage, ThresholdPct: 70},
		{Backend: "pi", Probe: config.ProbeNone},
	}
	testutil.NoError(t, d.SetBackendTiers(want))

	got, err := d.BackendTiers()
	testutil.NoError(t, err)
	testutil.DeepEqual(t, got, want)
}

func TestDB_BackendTiers_SetReplacesWholeList(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.SetBackendTiers([]config.BackendTier{
		{Backend: "claude", Probe: config.ProbeClaudeUsage, ThresholdPct: 80},
		{Backend: "codex", Probe: config.ProbeCodexUsage, ThresholdPct: 70},
	}))

	replacement := []config.BackendTier{
		{Backend: "pi", Probe: config.ProbeNone},
	}
	testutil.NoError(t, d.SetBackendTiers(replacement))

	got, err := d.BackendTiers()
	testutil.NoError(t, err)
	testutil.DeepEqual(t, got, replacement)
}

func TestDB_BackendTiers_SetEmptyClearsList(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.SetBackendTiers([]config.BackendTier{
		{Backend: "claude", Probe: config.ProbeClaudeUsage, ThresholdPct: 80},
	}))

	testutil.NoError(t, d.SetBackendTiers(nil))

	got, err := d.BackendTiers()
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 0)
}

// TestDB_Config_BackendTiersLoadedFromDB verifies db.Config surfaces the
// Settings-UI-edited tier list when config.toml defines none.
func TestDB_Config_BackendTiersLoadedFromDB(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.SetBackendTiers([]config.BackendTier{
		{Backend: "claude", Probe: config.ProbeClaudeUsage, ThresholdPct: 80},
	}))

	cfg := d.Config()
	testutil.Equal(t, len(cfg.BackendRouting.Tiers), 1)
	testutil.Equal(t, cfg.BackendRouting.Tiers[0].Backend, "claude")
}
