package sysmetrics

import (
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

func TestCollector_LatestZeroBeforeSample(t *testing.T) {
	c := New(t.TempDir())
	got := c.Latest()
	testutil.Equal(t, got.CPUAvail, false)
	testutil.Equal(t, got.DiskAvail, false)
	testutil.True(t, got.SampledAt.IsZero())
}

func TestCollector_UpdatePopulatesLatest(t *testing.T) {
	c := &Collector{
		diskPath: "/data",
		interval: defaultInterval,
		prime:    func() {},
		sample: func(path string) Snapshot {
			return Snapshot{CPUPercent: 42, CPUAvail: true, DiskPath: path, DiskAvail: true}
		},
	}
	c.update()
	got := c.Latest()
	testutil.Equal(t, got.CPUPercent, 42)
	testutil.True(t, got.CPUAvail)
	testutil.Equal(t, got.DiskPath, "/data")
}

func TestCollector_UnavailableMetricDoesNotError(t *testing.T) {
	c := &Collector{
		diskPath: "/data",
		interval: defaultInterval,
		prime:    func() {},
		// Sampler that can supply nothing — mirrors a platform missing every metric.
		sample: func(path string) Snapshot { return Snapshot{DiskPath: path, SampledAt: time.Unix(1, 0)} },
	}
	c.update()
	got := c.Latest()
	testutil.Equal(t, got.CPUAvail, false)
	testutil.Equal(t, got.LoadAvail, false)
	testutil.Equal(t, got.MemAvail, false)
	testutil.False(t, got.SampledAt.IsZero())
}

// Drives the transition-logging path (a metric available in one sample, gone the
// next) — exercises logNewlyUnavailable's true→false branch.
func TestCollector_HandlesAvailabilityTransition(t *testing.T) {
	var n int32
	c := &Collector{
		diskPath: "/data",
		interval: defaultInterval,
		prime:    func() {},
		sample: func(path string) Snapshot {
			if atomic.AddInt32(&n, 1) == 1 {
				return Snapshot{CPUAvail: true, MemAvail: true, DiskAvail: true, LoadAvail: true, SwapAvail: true, ProcAvail: true, UptimeAvail: true}
			}
			return Snapshot{} // everything now unavailable
		},
	}
	c.update() // first: all available
	c.update() // second: all unavailable → logs each transition
	testutil.Equal(t, c.Latest().CPUAvail, false)
}

func TestCollector_StartSamplesImmediatelyAndCloses(t *testing.T) {
	var calls int32
	c := &Collector{
		diskPath: "/data",
		interval: time.Hour, // long: the only sample before Close is the immediate one
		prime:    func() {},
		sample: func(path string) Snapshot {
			atomic.AddInt32(&calls, 1)
			return Snapshot{CPUAvail: true}
		},
	}
	c.Start()
	testutil.True(t, c.Latest().CPUAvail) // immediate sample populated Latest
	c.Close()                             // returns only once the loop goroutine exits
	testutil.Equal(t, atomic.LoadInt32(&calls), int32(1))
}

func TestCollector_TicksUntilClosed(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-based")
	}
	var calls int32
	c := &Collector{
		diskPath: "/data",
		interval: time.Millisecond,
		prime:    func() {},
		sample: func(path string) Snapshot {
			atomic.AddInt32(&calls, 1)
			return Snapshot{}
		},
	}
	c.Start()
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&calls) < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	c.Close()
	if got := atomic.LoadInt32(&calls); got < 3 {
		t.Fatalf("expected at least 3 samples from ticking, got %d", got)
	}
}

func TestCollector_CloseWithoutStartIsSafe(t *testing.T) {
	c := New(t.TempDir())
	c.Close() // no cancel/done set — must not panic or block
}

// Start is idempotent: a second call must not launch a second goroutine or
// re-sample. With a long interval the only sample is the first Start's immediate one.
func TestCollector_StartIsIdempotent(t *testing.T) {
	var calls int32
	c := &Collector{
		diskPath: "/data",
		interval: time.Hour,
		prime:    func() {},
		sample: func(string) Snapshot {
			atomic.AddInt32(&calls, 1)
			return Snapshot{}
		},
	}
	c.Start()
	c.Start() // no-op: must not re-prime, re-sample, or leak a second loop
	defer c.Close()
	testutil.Equal(t, atomic.LoadInt32(&calls), int32(1))
}

// Exercises the real gopsutil-backed sampler. Disk usage via statfs works on the
// dev platforms (darwin/linux); other metrics are best-effort, so we only assert
// the universally-available pieces to keep the test platform-stable.
func TestDefaultSample(t *testing.T) {
	s := defaultSample(t.TempDir())
	testutil.False(t, s.SampledAt.IsZero())
	testutil.True(t, s.DiskPath != "")
	testutil.True(t, s.DiskAvail)
}

func TestDefaultPrime(t *testing.T) {
	defaultPrime() // must not panic
}

func TestCollapseHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home dir on this host")
	}
	testutil.Equal(t, collapseHome(home), "~")
	testutil.Equal(t, collapseHome(home+string(os.PathSeparator)+".argus"), "~"+string(os.PathSeparator)+".argus")
	// A path outside home is returned unchanged (and never leaks a username).
	outside := string(os.PathSeparator) + "var" + string(os.PathSeparator) + "tmp"
	testutil.Equal(t, collapseHome(outside), outside)
}

func TestCollector_SetForTest(t *testing.T) {
	c := New(t.TempDir())
	c.SetForTest(Snapshot{CPUPercent: 7, CPUAvail: true})
	got := c.Latest()
	testutil.Equal(t, got.CPUPercent, 7)
	testutil.True(t, got.CPUAvail)
}

// A zero/negative interval is reset to the default by Start.
func TestCollector_StartResetsZeroInterval(t *testing.T) {
	c := &Collector{
		diskPath: "/data",
		interval: 0,
		prime:    func() {},
		sample:   func(string) Snapshot { return Snapshot{} },
	}
	c.Start()
	defer c.Close()
	testutil.Equal(t, c.interval, defaultInterval)
}
