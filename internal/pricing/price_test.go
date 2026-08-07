package pricing

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestPriceDelta_NoRateEntry_Unpriced(t *testing.T) {
	table := &Table{Models: map[string]Rate{}}
	usd, ok := table.PriceDelta("sonnet", TokenDeltas{Input: 1000})
	testutil.Equal(t, ok, false)
	testutil.Equal(t, usd, 0.0)
}

func TestPriceDelta_PerClassCorrectness(t *testing.T) {
	table := &Table{Models: map[string]Rate{
		"sonnet": {Input: 3.0, CacheWrite1h: 6.0, CacheWrite5m: 3.75, CacheRead: 0.3, Output: 15.0},
	}}
	usd, ok := table.PriceDelta("sonnet", TokenDeltas{
		Input:        1_000_000,
		CacheWrite1h: 1_000_000,
		CacheWrite5m: 1_000_000,
		CacheRead:    1_000_000,
		Output:       1_000_000,
	})
	testutil.Equal(t, ok, true)
	testutil.Equal(t, usd, 3.0+6.0+3.75+0.3+15.0)
}

func TestPriceDelta_ZeroDeltaPricesToZero(t *testing.T) {
	table := &Table{Models: map[string]Rate{"sonnet": {Input: 3.0}}}
	usd, ok := table.PriceDelta("sonnet", TokenDeltas{})
	testutil.Equal(t, ok, true)
	testutil.Equal(t, usd, 0.0)
}
