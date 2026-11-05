package backendtier

import (
	"errors"
	"sync"
	"time"

	"github.com/drn/argus/internal/uxlog"
)

// ErrNoUsageSnapshot is returned by CachedUsage.Read when no usable snapshot
// exists yet (never fetched, or the last good one is older than maxAge).
// currentReadings treats any source error as fail-open, so callers fall back
// to the process's local caches exactly as they do for a failed RPC.
var ErrNoUsageSnapshot = errors.New("backendtier: no fresh usage snapshot")

// CachedUsage wraps a slow usage source (a daemon RPC) so reading it never
// blocks the caller. Read returns the last good snapshot immediately and, when
// that snapshot is older than ttl, kicks ONE background refresh
// (single-flight). It exists because tier resolution runs on the TUI's UI
// goroutine in per-tick Hera refresh paths, where a synchronous daemon RPC
// (~30ms each, once per role per tick, plus RPC-connection queueing) froze typing for ~20s at a time.
//
// A snapshot older than maxAge is not served (ErrNoUsageSnapshot) so a dead
// daemon cannot pin routing to arbitrarily old readings.
type CachedUsage struct {
	fetch  func() (Usage, error)
	ttl    time.Duration
	maxAge time.Duration
	now    func() time.Time

	mu       sync.Mutex
	snap     Usage
	fetched  time.Time
	have     bool
	inflight bool
}

// NewCachedUsage builds a CachedUsage over fetch.
func NewCachedUsage(fetch func() (Usage, error), ttl, maxAge time.Duration) *CachedUsage {
	return &CachedUsage{fetch: fetch, ttl: ttl, maxAge: maxAge, now: time.Now}
}

// Prime performs one synchronous fetch. Call it only before the UI loop runs
// (process startup) so the first resolutions see real readings.
func (c *CachedUsage) Prime() {
	c.mu.Lock()
	c.inflight = true
	c.mu.Unlock()
	c.refresh()
}

// Read returns the cached snapshot without ever performing I/O on the calling
// goroutine.
func (c *CachedUsage) Read() (Usage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	age := c.now().Sub(c.fetched)
	if (!c.have || age > c.ttl) && !c.inflight {
		c.inflight = true
		go c.refresh()
	}
	if !c.have || age > c.maxAge {
		return Usage{}, ErrNoUsageSnapshot
	}
	return c.snap, nil
}

// refresh runs one fetch and publishes the result. Caller must have set
// inflight; a failed fetch keeps the previous snapshot (served until maxAge).
func (c *CachedUsage) refresh() {
	var (
		u   Usage
		err error
	)
	// Always clear inflight, even if fetch panics, or refreshes stop forever.
	defer func() {
		c.mu.Lock()
		c.inflight = false
		c.mu.Unlock()
	}()
	u, err = c.fetch()
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		uxlog.Log("[backendtier] background usage refresh failed: %v", err)
		return
	}
	c.snap, c.fetched, c.have = u, c.now(), true
}
