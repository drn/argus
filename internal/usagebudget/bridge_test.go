package usagebudget

import (
	"context"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestBridgeReaderAccountAndProbePreference(t *testing.T) {
	restore := SetBridgeReader(func(account string) (Reading, bool) {
		if account == "" || account == "work" {
			return Reading{Percentage: 73}, true
		}
		return Reading{}, false
	})
	defer restore()
	pct, ok := CachedClaudePct()
	testutil.True(t, ok)
	testutil.Equal(t, pct, 73.0)
	pct, ok = CachedClaudePctForAccount("work")
	testutil.True(t, ok)
	testutil.Equal(t, pct, 73.0)
	_, ok = CachedClaudePctForAccount("personal")
	testutil.False(t, ok)
	previous := probeDirFunc
	probeDirFunc = func() (string, error) { t.Fatal("hidden probe launched despite live bridge"); return "", nil }
	defer func() { probeDirFunc = previous }()
	testutil.NoError(t, Probe(context.Background()))
	reading, ok := CachedReading()
	testutil.True(t, ok)
	testutil.Equal(t, reading.Percentage, 73.0)
}

func TestNamedAccountNeverBorrowsDefaultProbe(t *testing.T) {
	restore := SetBridgeReader(nil)
	defer restore()
	usageCache.mu.Lock()
	previous, previousOK := usageCache.reading, usageCache.ok
	usageCache.reading = Reading{Percentage: 99, LastProbedAt: nowFunc()}
	usageCache.ok = true
	usageCache.mu.Unlock()
	defer func() {
		usageCache.mu.Lock()
		usageCache.reading, usageCache.ok = previous, previousOK
		usageCache.mu.Unlock()
	}()
	_, ok := CachedClaudePctForAccount("work")
	testutil.False(t, ok)
	pct, ok := CachedClaudePct()
	testutil.True(t, ok)
	testutil.Equal(t, pct, 99.0)
}
