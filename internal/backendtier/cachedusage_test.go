package backendtier

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/usagebudget"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (f *fakeClock) Now() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.t }
func (f *fakeClock) Add(d time.Duration) {
	f.mu.Lock()
	f.t = f.t.Add(d)
	f.mu.Unlock()
}

func newTestCached(fetch func() (Usage, error)) (*CachedUsage, *fakeClock) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	c := NewCachedUsage(fetch, 5*time.Second, time.Minute)
	c.now = clk.Now
	return c, clk
}

func waitIdle(t *testing.T, c *CachedUsage) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		busy := c.inflight
		c.mu.Unlock()
		if !busy {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("refresh never finished")
}

// A blocking fetch must never block Read — the regression behind the UI freeze.
func TestCachedUsage_ReadNeverBlocksOnSlowFetch(t *testing.T) {
	release := make(chan struct{})
	c, _ := newTestCached(func() (Usage, error) { <-release; return Usage{}, nil })
	done := make(chan error, 1)
	go func() { _, err := c.Read(); done <- err }()
	select {
	case err := <-done:
		testutil.ErrorIs(t, err, ErrNoUsageSnapshot)
	case <-time.After(time.Second):
		t.Fatal("Read blocked on a slow fetch")
	}
	close(release)
	waitIdle(t, c)
}

func TestCachedUsage_ServesWarmSnapshotAndRefreshesSingleFlight(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	pct := 10.0
	c, clk := newTestCached(func() (Usage, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return Usage{Claude: map[string]usagebudget.Reading{"default": {Percentage: pct}}}, nil
	})
	c.Prime()
	u, err := c.Read()
	testutil.NoError(t, err)
	testutil.Equal(t, u.Claude["default"].Percentage, 10.0)

	// Fresh: no refetch.
	for i := 0; i < 5; i++ {
		_, _ = c.Read()
	}
	waitIdle(t, c)
	mu.Lock()
	testutil.Equal(t, calls, 1)
	pct = 20
	mu.Unlock()

	// Stale: the stale value is still served, and many reads trigger one fetch.
	clk.Add(6 * time.Second)
	u, err = c.Read()
	testutil.NoError(t, err)
	testutil.Equal(t, u.Claude["default"].Percentage, 10.0)
	for i := 0; i < 5; i++ {
		_, _ = c.Read()
	}
	waitIdle(t, c)
	mu.Lock()
	testutil.Equal(t, calls, 2)
	mu.Unlock()
	u, _ = c.Read()
	testutil.Equal(t, u.Claude["default"].Percentage, 20.0)
}

func TestCachedUsage_FailedRefreshKeepsSnapshotUntilMaxAge(t *testing.T) {
	fail := false
	c, clk := newTestCached(func() (Usage, error) {
		if fail {
			return Usage{}, errors.New("daemon down")
		}
		return Usage{Claude: map[string]usagebudget.Reading{"default": {Percentage: 5}}}, nil
	})
	c.Prime()
	fail = true
	clk.Add(10 * time.Second)
	_, err := c.Read()
	testutil.NoError(t, err) // stale but within maxAge
	waitIdle(t, c)
	clk.Add(2 * time.Minute)
	_, err = c.Read()
	testutil.ErrorIs(t, err, ErrNoUsageSnapshot) // too old: fail open
	waitIdle(t, c)
}

// An unwarmed cached source fails open to the local caches, like a failed RPC.
func TestResolveChoice_UnwarmedCachedSourceFailsOpenToLocal(t *testing.T) {
	withCachedPct(t, unknown(), unknown())
	release := make(chan struct{})
	c, _ := newTestCached(func() (Usage, error) { <-release; return Usage{}, nil })
	defer func() { close(release); waitIdle(t, c) }()
	restore := SetUsageSource(c.Read)
	defer restore()
	cfg := personalCfg(config.StrategyHeadroom, claudeAcct("", 90), claudeAcct("personal", 90))
	done := make(chan Choice, 1)
	go func() { done <- ResolveChoice(cfg, "", "") }()
	select {
	case got := <-done:
		testutil.Equal(t, got.Backend, "claude")
		// Local caches are empty, so the fail-open path must treat the reading as unknown.
		testutil.Contains(t, got.Reason, "known=false")
	case <-time.After(time.Second):
		t.Fatal("ResolveChoice blocked on the unwarmed source")
	}
}
