package usagebudget

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/uxlog"
)

func resetState(t *testing.T) {
	t.Helper()
	usageCache.mu.Lock()
	usageCache.reading = Reading{}
	usageCache.ok = false
	usageCache.mu.Unlock()

	probeRunner = func(context.Context) ([]byte, error) {
		return nil, errors.New("probe runner not set")
	}
	nowFunc = time.Now
	cacheSnapshotHook = nil

	t.Cleanup(func() {
		usageCache.mu.Lock()
		usageCache.reading = Reading{}
		usageCache.ok = false
		usageCache.mu.Unlock()
		probeRunner = runClaudeUsageProbe
		nowFunc = time.Now
		cacheSnapshotHook = nil
	})
}

func initTestUxlog(t *testing.T) func() string {
	t.Helper()
	uxlog.Close()
	logPath := filepath.Join(t.TempDir(), "ux.log")
	testutil.NoError(t, uxlog.Init(logPath))
	t.Cleanup(uxlog.Close)
	return func() string {
		b, err := os.ReadFile(logPath)
		testutil.NoError(t, err)
		return string(b)
	}
}

func sampleUsageFrame() []byte {
	return []byte("\x1b[2J\x1b[H╭─ Claude Code v2.1.276 ─╮\n" +
		"│ Current week (all models) 94.5% used │\n" +
		"│ Resets Sep 21 at 5:00 AM (PDT)       │\n" +
		"╰──────────────────────────────────────╯\n")
}

func TestProbe_SuccessfulParseUpdatesCache(t *testing.T) {
	resetState(t)
	readLog := initTestUxlog(t)
	loc, err := time.LoadLocation("America/Los_Angeles")
	testutil.NoError(t, err)
	probedAt := time.Date(2026, 9, 18, 12, 0, 0, 0, loc)
	nowFunc = func() time.Time { return probedAt }
	probeRunner = func(context.Context) ([]byte, error) {
		return sampleUsageFrame(), nil
	}

	testutil.NoError(t, Probe(context.Background()))

	got, ok := snapshot()
	testutil.Equal(t, ok, true)
	testutil.Equal(t, got.Percentage, 94.5)
	if want := time.Date(2026, 9, 21, 5, 0, 0, 0, loc); !got.ResetAt.Equal(want) {
		t.Errorf("ResetAt = %v, want %v", got.ResetAt, want)
	}
	testutil.Equal(t, got.LastProbedAt, probedAt)
	testutil.Contains(t, readLog(), "[usagebudget] probe updated")
}

func TestProbe_SubprocessFailureLeavesCacheUnchangedAndLogs(t *testing.T) {
	resetState(t)
	readLog := initTestUxlog(t)
	loc, err := time.LoadLocation("America/Los_Angeles")
	testutil.NoError(t, err)
	previous := Reading{
		Percentage:   88,
		ResetAt:      time.Date(2026, 9, 21, 5, 0, 0, 0, loc),
		LastProbedAt: time.Date(2026, 9, 18, 11, 0, 0, 0, loc),
	}
	storeReading(previous)
	nowFunc = func() time.Time { return time.Date(2026, 9, 18, 12, 0, 0, 0, loc) }
	probeRunner = func(context.Context) ([]byte, error) {
		return nil, errors.New("boom")
	}

	testutil.NoError(t, Probe(context.Background()))

	got, ok := snapshot()
	testutil.Equal(t, ok, true)
	testutil.DeepEqual(t, got, previous)
	testutil.Contains(t, readLog(), "[usagebudget] probe failed")
}

func TestProbe_UnparseableOutputLeavesCacheUnchangedAndLogs(t *testing.T) {
	resetState(t)
	readLog := initTestUxlog(t)
	loc, err := time.LoadLocation("America/Los_Angeles")
	testutil.NoError(t, err)
	previous := Reading{
		Percentage:   88,
		ResetAt:      time.Date(2026, 9, 21, 5, 0, 0, 0, loc),
		LastProbedAt: time.Date(2026, 9, 18, 11, 0, 0, 0, loc),
	}
	storeReading(previous)
	nowFunc = func() time.Time { return time.Date(2026, 9, 18, 12, 0, 0, 0, loc) }
	probeRunner = func(context.Context) ([]byte, error) {
		return []byte("not a usage screen"), nil
	}

	testutil.NoError(t, Probe(context.Background()))

	got, ok := snapshot()
	testutil.Equal(t, ok, true)
	testutil.DeepEqual(t, got, previous)
	testutil.Contains(t, readLog(), "[usagebudget] parse failed")
}

func TestCacheReadBeforeProbeIsUnknown(t *testing.T) {
	resetState(t)

	_, ok := snapshot()
	testutil.Equal(t, ok, false)
}

func TestParseUsageOutput_DefensiveFormats(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	testutil.NoError(t, err)
	base := time.Date(2026, 12, 31, 12, 0, 0, 0, loc)
	out := "Current week (all models): 90%\nResets Fri, Jan 2nd at 5 PM (PST)"

	got, err := parseUsageOutput(out, base)
	testutil.NoError(t, err)

	testutil.Equal(t, got.Percentage, 90.0)
	if want := time.Date(2027, 1, 2, 17, 0, 0, 0, loc); !got.ResetAt.Equal(want) {
		t.Errorf("ResetAt = %v, want %v", got.ResetAt, want)
	}
}

func TestResolveWorkerBackend_ExplicitBypassesCache(t *testing.T) {
	resetState(t)
	cacheSnapshotHook = func() {
		t.Fatal("explicit backend must not read the usage cache")
	}

	got := ResolveWorkerBackend("claude-special", config.Config{})

	testutil.Equal(t, got, "claude-special")
}

func TestResolveWorkerBackend_ManualSwitch(t *testing.T) {
	resetState(t)
	cacheSnapshotHook = func() {
		t.Fatal("manual switch must not read the usage cache")
	}
	cfg := config.DefaultConfig()
	cfg.Hera.WorkerBudget.Enabled = true

	got := ResolveWorkerBackend("", cfg)

	testutil.Equal(t, got, config.DefaultWorkerBudgetFallbackBackend)
}

func TestResolveWorkerBackend_ThresholdCrossed(t *testing.T) {
	resetState(t)
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	nowFunc = func() time.Time { return now }
	storeReading(Reading{Percentage: 91, ResetAt: now.Add(24 * time.Hour), LastProbedAt: now.Add(-time.Minute)})
	cfg := config.DefaultConfig()
	cfg.Hera.WorkerBudget.ThresholdPct = 90

	got := ResolveWorkerBackend("", cfg)

	testutil.Equal(t, got, config.DefaultWorkerBudgetFallbackBackend)
}

func TestResolveWorkerBackend_ThresholdNotCrossedUnknownOrStale(t *testing.T) {
	for _, tc := range []struct {
		name    string
		reading *Reading
	}{
		{name: "below threshold", reading: &Reading{Percentage: 89}},
		{name: "unknown"},
		{name: "stale", reading: &Reading{Percentage: 95, LastProbedAt: time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetState(t)
			now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
			nowFunc = func() time.Time { return now }
			if tc.reading != nil {
				reading := *tc.reading
				if reading.LastProbedAt.IsZero() {
					reading.LastProbedAt = now.Add(-time.Minute)
				}
				reading.ResetAt = now.Add(24 * time.Hour)
				storeReading(reading)
			}
			cfg := config.DefaultConfig()
			cfg.Hera.WorkerBudget.ThresholdPct = 90

			got := ResolveWorkerBackend("", cfg)

			testutil.Equal(t, got, "")
		})
	}
}

func TestResolveWorkerBackend_InvalidFallbackFailsOpen(t *testing.T) {
	resetState(t)
	cfg := config.DefaultConfig()
	cfg.Hera.WorkerBudget.Enabled = true
	cfg.Hera.WorkerBudget.FallbackBackend = "missing"

	got := ResolveWorkerBackend("", cfg)

	testutil.Equal(t, got, "")
}

func TestResolveWorkerBackend_AbsentConfigInactive(t *testing.T) {
	resetState(t)
	cfg := config.DefaultConfig()
	if cfg.Hera.WorkerBudget.Enabled || cfg.Hera.WorkerBudget.ThresholdPct != 0 {
		t.Fatal("default worker budget config should be inactive")
	}

	got := ResolveWorkerBackend("", cfg)

	testutil.Equal(t, got, "")
}

func TestProbe_ContextCancellationIsNotLoggedAsFailure(t *testing.T) {
	resetState(t)
	readLog := initTestUxlog(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	testutil.NoError(t, Probe(ctx))

	if strings.Contains(readLog(), "[usagebudget] probe failed") {
		t.Fatalf("canceled context should not log probe failure: %s", readLog())
	}
}
