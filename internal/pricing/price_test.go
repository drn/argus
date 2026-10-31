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

func TestPriceCall(t *testing.T) {
	tiered := Rate{
		Input: 2, CacheRead: 0.1, CacheWrite5m: 2.5, Output: 10,
		LongContextThreshold: 272_000,
		LongContext:          &Rate{Input: 4, CacheRead: 0.2, CacheWrite5m: 5, Output: 15},
	}
	table := &Table{Models: map[string]Rate{
		"tiered": tiered,
		"flat":   {Input: 2, CacheRead: 0.1, Output: 10},
		"zero":   {Input: 2, LongContextThreshold: 0, LongContext: &Rate{Input: 99}},
	}}
	d := TokenDeltas{Input: 1_000_000, CacheRead: 1_000_000, CacheWrite5m: 1_000_000, Output: 1_000_000}
	for _, tc := range []struct {
		name   string
		model  string
		input  int64
		want   float64
		wantOK bool
	}{
		{"above threshold uses long-context rates", "tiered", 272_001, 4 + 0.2 + 5 + 15, true},
		{"at threshold uses base rates", "tiered", 272_000, 2 + 0.1 + 2.5 + 10, true},
		{"below threshold uses base rates", "tiered", 1_000, 2 + 0.1 + 2.5 + 10, true},
		{"no tier always base", "flat", 900_000, 2 + 0.1 + 10, true},
		{"zero threshold ignores tier", "zero", 900_000, 2, true},
		{"unknown model unpriced", "missing", 1, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usd, ok := table.PriceCall(tc.model, d, tc.input)
			testutil.Equal(t, ok, tc.wantOK)
			testutil.Equal(t, usd, tc.want)
		})
	}
}

func TestPriceDelta_IgnoresLongContextTier(t *testing.T) {
	table := &Table{Models: map[string]Rate{
		"tiered": {Input: 2, LongContextThreshold: 1, LongContext: &Rate{Input: 4}},
	}}
	usd, ok := table.PriceDelta("tiered", TokenDeltas{Input: 1_000_000})
	testutil.Equal(t, ok, true)
	testutil.Equal(t, usd, 2.0)
}
