// Package sysmetrics samples host-system load (CPU, memory, swap, disk, process
// RSS, uptime) on a background ticker and caches the latest snapshot. The REST
// API serves the cache so requests return promptly and CPU percentages reflect a
// stable sampling interval rather than the gap between arbitrary client polls.
//
// Collection is backed by gopsutil/v4 (pure-Go, no CGO — matching the rest of the
// Argus build). Each metric group carries an availability flag so a metric the
// host platform cannot supply degrades to "unavailable" instead of failing the
// whole snapshot.
package sysmetrics

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/drn/argus/internal/uxlog"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/process"
)

// defaultInterval is how often the collector resamples. Short enough to feel
// "live" in the Settings panel, long enough that CPU deltas are meaningful and
// the sampling cost is negligible.
const defaultInterval = 2 * time.Second

// Snapshot is a point-in-time view of host-system load. Each metric group
// carries an *Avail bool: when false the host platform could not supply that
// metric for this sample and the numeric fields are meaningless — callers SHOULD
// render a placeholder rather than the zero value.
type Snapshot struct {
	// CPU is overall (all-core) utilization since the previous sample.
	CPUPercent float64 `json:"cpu_percent"`
	CPUAvail   bool    `json:"cpu_avail"`

	// Load is the 1/5/15-minute load average (Unix-like hosts only).
	Load1     float64 `json:"load1"`
	Load5     float64 `json:"load5"`
	Load15    float64 `json:"load15"`
	LoadAvail bool    `json:"load_avail"`

	// Memory (physical RAM).
	MemTotal     uint64  `json:"mem_total"`
	MemUsed      uint64  `json:"mem_used"`
	MemAvailable uint64  `json:"mem_available"`
	MemPercent   float64 `json:"mem_percent"`
	MemAvail     bool    `json:"mem_avail"`

	// Swap.
	SwapTotal   uint64  `json:"swap_total"`
	SwapUsed    uint64  `json:"swap_used"`
	SwapPercent float64 `json:"swap_percent"`
	SwapAvail   bool    `json:"swap_avail"`

	// Disk for the filesystem holding the Argus data dir (worktrees + SQLite).
	DiskTotal   uint64  `json:"disk_total"`
	DiskUsed    uint64  `json:"disk_used"`
	DiskFree    uint64  `json:"disk_free"`
	DiskPercent float64 `json:"disk_percent"`
	DiskPath    string  `json:"disk_path"`
	DiskAvail   bool    `json:"disk_avail"`

	// ProcRSS is the resident memory of the Argus process itself.
	ProcRSS   uint64 `json:"proc_rss"`
	ProcAvail bool   `json:"proc_avail"`

	// UptimeSec is host uptime in seconds.
	UptimeSec   uint64 `json:"uptime_sec"`
	UptimeAvail bool   `json:"uptime_avail"`

	// SampledAt is when this snapshot was collected.
	SampledAt time.Time `json:"sampled_at"`
}

// sampleFunc collects one snapshot for the given disk path. It is a field on the
// Collector so tests can inject a deterministic sampler without touching the host.
type sampleFunc func(diskPath string) Snapshot

// Collector periodically samples the host and caches the latest Snapshot.
type Collector struct {
	diskPath string
	interval time.Duration

	// sample collects a snapshot; prime warms any sampler-internal baseline
	// (gopsutil's CPU percentage compares against the previous call) before the
	// first real sample. Both are injectable for tests.
	sample sampleFunc
	prime  func()

	mu   sync.RWMutex
	last Snapshot

	cancel context.CancelFunc
	done   chan struct{}
}

// New returns a Collector that will sample the filesystem holding diskPath. Call
// Start to begin sampling and Close to stop. diskPath should be the Argus data
// directory so the disk metric reflects where worktrees and the DB actually live.
func New(diskPath string) *Collector {
	return &Collector{
		diskPath: diskPath,
		interval: defaultInterval,
		sample:   defaultSample,
		prime:    defaultPrime,
	}
}

// Start primes the CPU baseline, takes an immediate first sample (so Latest is
// populated right away), and launches the background sampling loop. Safe to call
// once; subsequent calls before Close are a no-op-ish reset and should be avoided.
func (c *Collector) Start() {
	if c.interval <= 0 {
		c.interval = defaultInterval
	}
	if c.prime != nil {
		c.prime()
	}
	c.update() // populate Latest() immediately so the first poll isn't empty

	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.done = make(chan struct{})
	go c.loop(ctx)
	uxlog.Log("[sysmetrics] collector started (interval=%s, disk=%s)", c.interval, c.diskPath)
}

func (c *Collector) loop(ctx context.Context) {
	defer close(c.done)
	t := time.NewTicker(c.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			uxlog.Log("[sysmetrics] collector stopped")
			return
		case <-t.C:
			c.update()
		}
	}
}

// update takes one sample, swaps it into the cache, and logs any metric group
// that newly became unavailable (transition true→false) so the log isn't spammed
// every tick on a platform that simply never supports a metric.
func (c *Collector) update() {
	snap := c.sample(c.diskPath)
	c.mu.Lock()
	prev := c.last
	c.last = snap
	c.mu.Unlock()
	logNewlyUnavailable(prev, snap)
}

// Latest returns the most recent snapshot. Before the first sample it returns the
// zero Snapshot (all availability flags false).
func (c *Collector) Latest() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.last
}

// SetForTest replaces the cached snapshot. It exists so tests in other packages
// (e.g. the REST handler) can seed a deterministic snapshot without starting the
// real sampler. Not for production use.
func (c *Collector) SetForTest(s Snapshot) {
	c.mu.Lock()
	c.last = s
	c.mu.Unlock()
}

// Close stops the sampling loop and waits for it to exit. Safe to call on a
// Collector that was never started.
func (c *Collector) Close() {
	if c.cancel != nil {
		c.cancel()
	}
	if c.done != nil {
		<-c.done
	}
}

// defaultPrime warms gopsutil's CPU baseline so the first real cpu.Percent(0,…)
// reflects the interval since Start rather than since process boot.
func defaultPrime() { _, _ = cpu.Percent(0, false) }

// defaultSample collects every metric, tolerating per-metric failures by leaving
// that group's availability flag false.
func defaultSample(diskPath string) Snapshot {
	s := Snapshot{DiskPath: diskPath, SampledAt: time.Now()}

	if pct, err := cpu.Percent(0, false); err == nil && len(pct) > 0 {
		s.CPUPercent = pct[0]
		s.CPUAvail = true
	}
	if avg, err := load.Avg(); err == nil && avg != nil {
		s.Load1, s.Load5, s.Load15 = avg.Load1, avg.Load5, avg.Load15
		s.LoadAvail = true
	}
	if vm, err := mem.VirtualMemory(); err == nil && vm != nil {
		s.MemTotal, s.MemUsed, s.MemAvailable, s.MemPercent = vm.Total, vm.Used, vm.Available, vm.UsedPercent
		s.MemAvail = true
	}
	if sw, err := mem.SwapMemory(); err == nil && sw != nil {
		s.SwapTotal, s.SwapUsed, s.SwapPercent = sw.Total, sw.Used, sw.UsedPercent
		s.SwapAvail = true
	}
	if du, err := disk.Usage(diskPath); err == nil && du != nil {
		s.DiskTotal, s.DiskUsed, s.DiskFree, s.DiskPercent = du.Total, du.Used, du.Free, du.UsedPercent
		s.DiskAvail = true
	}
	pid := int32(os.Getpid()) //nolint:gosec // a PID always fits in int32 on supported platforms
	if p, err := process.NewProcess(pid); err == nil {
		if mi, err := p.MemoryInfo(); err == nil && mi != nil {
			s.ProcRSS = mi.RSS
			s.ProcAvail = true
		}
	}
	if up, err := host.Uptime(); err == nil {
		s.UptimeSec = up
		s.UptimeAvail = true
	}
	return s
}

// logNewlyUnavailable logs each metric group that was available in prev but is
// not in cur, so an environment that loses a metric (or never had it after the
// first sample) is noted once rather than on every tick.
func logNewlyUnavailable(prev, cur Snapshot) {
	type g struct {
		name        string
		wasOK, isOK bool
	}
	for _, grp := range []g{
		{"cpu", prev.CPUAvail, cur.CPUAvail},
		{"load", prev.LoadAvail, cur.LoadAvail},
		{"mem", prev.MemAvail, cur.MemAvail},
		{"swap", prev.SwapAvail, cur.SwapAvail},
		{"disk", prev.DiskAvail, cur.DiskAvail},
		{"proc", prev.ProcAvail, cur.ProcAvail},
		{"uptime", prev.UptimeAvail, cur.UptimeAvail},
	} {
		if grp.wasOK && !grp.isOK {
			uxlog.Log("[sysmetrics] %s metric became unavailable", grp.name)
		}
	}
}
