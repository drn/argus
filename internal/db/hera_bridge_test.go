package db

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestAccrueHeraBridgeCost(t *testing.T) {
	d := heraTestDB(t)
	o := mkOrch(t, d, "o")
	r := mkRole(t, d, o.ID, "r", HeraKindWorker)
	b := mkBinding(t, d, r.ID, "t", "/tmp/t")
	for _, tc := range []struct {
		name, session string
		cost, want    float64
	}{
		{"first", "s", 1, 1}, {"increase", "s", 1.5, 1.5}, {"duplicate", "s", 1.5, 1.5}, {"clear", "new", 0.2, 1.7}, {"counter reset", "new", 0.1, 1.8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testutil.NoError(t, d.AccrueHeraBridgeCost(b.ID, tc.session, tc.cost))
			got, err := d.GetHeraBindingCostTotals(b.ID)
			testutil.NoError(t, err)
			testutil.Equal(t, got.CostUSDAccrued, tc.want)
		})
	}
}

func TestHeraBridgeCostSurvivesDatabaseReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	d, err := Open(path)
	testutil.NoError(t, err)
	o := mkOrch(t, d, "o")
	r := mkRole(t, d, o.ID, "r", HeraKindWorker)
	b := mkBinding(t, d, r.ID, "t", "/tmp/t")
	testutil.NoError(t, d.AccrueHeraBridgeCost(b.ID, "s", 2))
	testutil.NoError(t, d.Close())
	d, err = Open(path)
	testutil.NoError(t, err)
	t.Cleanup(func() { testutil.NoError(t, d.Close()) })
	testutil.NoError(t, d.AccrueHeraBridgeCost(b.ID, "s", 2))
	got, err := d.GetHeraBindingCostTotals(b.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.CostUSDAccrued, 2.0)
}

func TestHeraBridgeCostLateSampleVersusRestart(t *testing.T) {
	d := heraTestDB(t)
	o := mkOrch(t, d, "o")
	r := mkRole(t, d, o.ID, "r", HeraKindWorker)
	b := mkBinding(t, d, r.ID, "t", "/tmp/t")
	testutil.NoError(t, d.AccrueHeraBridgeCost(b.ID, "s", 2, "launch"))
	testutil.NoError(t, d.AccrueHeraBridgeCost(b.ID, "s", 1, "launch")) // late old-boot sample
	testutil.NoError(t, d.AccrueHeraBridgeCost(b.ID, "s", 2.5, "launch"))
	got, err := d.GetHeraBindingCostTotals(b.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.CostUSDAccrued, 2.5)
	testutil.NoError(t, d.AccrueHeraBridgeCost(b.ID, "s", 0.5, "restarted"))
	got, err = d.GetHeraBindingCostTotals(b.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.CostUSDAccrued, 3.0)
}

func TestHeraBridgeCostRejectsInvalidAndMissingBinding(t *testing.T) {
	d := heraTestDB(t)
	for _, tc := range []struct {
		name, session string
		cost          float64
	}{
		{"empty session", "", 1}, {"negative", "s", -1}, {"nan", "s", math.NaN()}, {"infinity", "s", math.Inf(1)},
	} {
		t.Run(tc.name, func(t *testing.T) { testutil.True(t, d.AccrueHeraBridgeCost(999, tc.session, tc.cost) != nil) })
	}
	testutil.ErrorIs(t, d.AccrueHeraBridgeCost(999, "s", 1), ErrHeraNotFound)
	_, err := d.UpdateHeraTranscriptCost(999, HeraBindingCostTotals{})
	testutil.ErrorIs(t, err, ErrHeraNotFound)
}

func TestHeraCostSourceHandoff(t *testing.T) {
	d := heraTestDB(t)
	o := mkOrch(t, d, "o")
	r := mkRole(t, d, o.ID, "r", HeraKindWorker)
	b := mkBinding(t, d, r.ID, "t", "/tmp/t")
	testutil.NoError(t, d.AccrueHeraBridgeCost(b.ID, "s", 1))
	value, err := d.UpdateHeraTranscriptCost(b.ID, HeraBindingCostTotals{TokensInput: 100, CostUSDAccrued: 9})
	testutil.NoError(t, err)
	testutil.Equal(t, value, 1.0)
	value, err = d.UpdateHeraTranscriptCost(b.ID, HeraBindingCostTotals{TokensInput: 200, CostUSDAccrued: 1.2})
	testutil.NoError(t, err)
	testutil.Equal(t, value, 1.2)
	// A recovered bridge baselines its first cumulative report, then charges
	// only the next increment, preserving already-accrued dollars.
	testutil.NoError(t, d.AccrueHeraBridgeCost(b.ID, "s", 2))
	got, err := d.GetHeraBindingCostTotals(b.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.CostUSDAccrued, 1.2)
	testutil.NoError(t, d.AccrueHeraBridgeCost(b.ID, "s", 2.5))
	got, err = d.GetHeraBindingCostTotals(b.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.CostUSDAccrued, 1.7)
}
