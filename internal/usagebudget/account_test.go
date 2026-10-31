package usagebudget

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

func TestCachedReadingForAccount(t *testing.T) {
	base := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	fresh := func(pct float64) *Window {
		return &Window{Percentage: pct, ResetAt: base.Add(2 * time.Hour), LastProbedAt: base}
	}

	t.Run("probe cache keeps both windows", func(t *testing.T) {
		resetState(t)
		restore := SetBridgeReader(nil)
		defer restore()
		nowFunc = func() time.Time { return base }
		storeReading(Reading{FiveHour: fresh(81), SevenDay: fresh(40), LastProbedAt: base}, "personal")

		r, ok := CachedReadingForAccount("personal")
		testutil.True(t, ok)
		testutil.Equal(t, r.Percentage, 81.0)
		testutil.Equal(t, r.FiveHour.Percentage, 81.0)
		testutil.Equal(t, r.SevenDay.Percentage, 40.0)
	})

	t.Run("bridge wins over probe", func(t *testing.T) {
		resetState(t)
		nowFunc = func() time.Time { return base }
		storeReading(Reading{SevenDay: fresh(10), LastProbedAt: base}, "personal")
		restore := SetBridgeReader(func(a string) (Reading, bool) {
			if a == "personal" {
				return Reading{FiveHour: fresh(55), Percentage: 55}, true
			}
			return Reading{}, false
		})
		defer restore()

		r, ok := CachedReadingForAccount("personal")
		testutil.True(t, ok)
		testutil.Equal(t, r.Percentage, 55.0)
	})

	t.Run("default and blank share the default cache", func(t *testing.T) {
		resetState(t)
		restore := SetBridgeReader(nil)
		defer restore()
		nowFunc = func() time.Time { return base }
		storeReading(Reading{SevenDay: fresh(22), LastProbedAt: base})

		for _, name := range []string{"", "default"} {
			r, ok := CachedReadingForAccount(name)
			testutil.True(t, ok)
			testutil.Equal(t, r.Percentage, 22.0)
		}
		_, ok := CachedReadingForAccount("personal")
		testutil.False(t, ok)
	})
}

func TestLiveWindows(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	live := &Window{Percentage: 30, ResetAt: now.Add(time.Hour), LastProbedAt: now}
	expired := &Window{Percentage: 90, ResetAt: now.Add(-time.Minute), LastProbedAt: now.Add(-time.Hour)}
	stale := &Window{Percentage: 70, ResetAt: now.Add(48 * time.Hour), LastProbedAt: now.Add(-2 * CacheMaxAge)}

	tests := []struct {
		name         string
		r            Reading
		wantFive     *Window
		wantSevenDay *Window
	}{
		{"both live", Reading{FiveHour: live, SevenDay: live}, live, live},
		{"expired five hour dropped", Reading{FiveHour: expired, SevenDay: live}, nil, live},
		{"stale weekly dropped", Reading{FiveHour: live, SevenDay: stale}, live, nil},
		{"none", Reading{}, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			five, week := LiveWindows(tt.r, now)
			testutil.Equal(t, five, tt.wantFive)
			testutil.Equal(t, week, tt.wantSevenDay)
		})
	}
}
