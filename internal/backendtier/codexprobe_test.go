package backendtier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/uxlog"
)

func resetCodexState(t *testing.T) {
	t.Helper()
	codexCache.mu.Lock()
	codexCache.reading = Reading{}
	codexCache.ok = false
	codexCache.mu.Unlock()

	codexRolloutProbeRunner = func() (Reading, bool) {
		t.Fatal("codexRolloutProbeRunner not set")
		return Reading{}, false
	}
	codexPTYProbeRunner = func(context.Context) ([]byte, error) {
		t.Fatal("codexPTYProbeRunner not set")
		return nil, nil
	}
	codexNowFunc = time.Now
	codexCacheSnapshotHook = nil
	SetCodexPTYFallbackEnabled(false)

	t.Cleanup(func() {
		codexCache.mu.Lock()
		codexCache.reading = Reading{}
		codexCache.ok = false
		codexCache.mu.Unlock()
		codexRolloutProbeRunner = rolloutReading
		codexPTYProbeRunner = runCodexPTYProbe
		codexNowFunc = time.Now
		codexCacheSnapshotHook = nil
		SetCodexPTYFallbackEnabled(false)
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

func TestProbe_FreshRolloutSkipsPTYFallback(t *testing.T) {
	resetCodexState(t)
	readLog := initTestUxlog(t)
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	codexNowFunc = func() time.Time { return now }
	want := Reading{Percentage: 42, ResetAt: now.Add(24 * time.Hour)}
	codexRolloutProbeRunner = func() (Reading, bool) { return want, true }
	codexPTYProbeRunner = func(context.Context) ([]byte, error) {
		t.Fatal("PTY fallback must not run when the rollout read is fresh")
		return nil, nil
	}

	testutil.NoError(t, Probe(context.Background()))

	pct, ok := CachedCodexPct()
	testutil.Equal(t, ok, true)
	testutil.Equal(t, pct, 42.0)
	testutil.Contains(t, readLog(), "[backendtier] codex probe updated")
}

func TestProbe_StaleOrMissingRolloutFallsBackToPTY(t *testing.T) {
	resetCodexState(t)
	SetCodexPTYFallbackEnabled(true)
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	codexNowFunc = func() time.Time { return now }
	codexRolloutProbeRunner = func() (Reading, bool) { return Reading{}, false }
	codexPTYProbeRunner = func(context.Context) ([]byte, error) {
		return []byte("Usage\n  87% used\n  resets in 3h 20m\n"), nil
	}

	testutil.NoError(t, Probe(context.Background()))

	pct, ok := CachedCodexPct()
	testutil.Equal(t, ok, true)
	testutil.Equal(t, pct, 87.0)
}

// TestProbe_PTYFallbackDisabledByDefaultLeavesCacheStale is the core
// regression test for fix-backend-routing-semantics: a dead rollout-file
// path with the (default, opt-in-off) PTY fallback must NOT spend Codex
// quota — the cache simply stays stale/unknown, exactly like any other
// fail-open probe miss.
func TestProbe_PTYFallbackDisabledByDefaultLeavesCacheStale(t *testing.T) {
	resetCodexState(t)
	readLog := initTestUxlog(t)
	var logBuf bytes.Buffer
	originalSlog := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(originalSlog) })
	codexRolloutProbeRunner = func() (Reading, bool) { return Reading{}, false }
	codexPTYProbeRunner = func(context.Context) ([]byte, error) {
		t.Fatal("the costed PTY fallback must not run when disabled (the default)")
		return nil, nil
	}

	testutil.NoError(t, Probe(context.Background()))

	_, ok := CachedCodexPct()
	testutil.Equal(t, ok, false)
	testutil.Contains(t, readLog(), "costed PTY fallback is disabled")
	// Probe runs exclusively in the daemon process, where uxlog.Log is a
	// silent no-op (uxlog.Init is never called there) — slog.Warn is what
	// actually reaches daemon.log, so this must be logged on both channels.
	testutil.Contains(t, logBuf.String(), "costed PTY fallback is disabled")
	// The normal state when Codex is unused: INFO, not WARN.
	testutil.Contains(t, logBuf.String(), "level=INFO")
	if strings.Contains(logBuf.String(), "level=WARN") {
		t.Fatalf("disabled-fallback skip logged at WARN: %s", logBuf.String())
	}
}

// A PTY /status reading's expiry is capped at probe time + 8 days, so a
// garbage reset can't keep it valid indefinitely.
func TestProbe_PTYReadingExpiryIsCappedAtEightDays(t *testing.T) {
	resetCodexState(t)
	SetCodexPTYFallbackEnabled(true)
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	codexNowFunc = func() time.Time { return now }
	codexRolloutProbeRunner = func() (Reading, bool) { return Reading{}, false }
	codexPTYProbeRunner = func(context.Context) ([]byte, error) {
		return []byte("Weekly limit: 40% used\nresets in 400d\n"), nil
	}

	testutil.NoError(t, Probe(context.Background()))

	got, ok := CachedReading()
	testutil.Equal(t, ok, true)
	testutil.Equal(t, got.ExpiresAt(), now.Add(8*24*time.Hour))
	codexNowFunc = func() time.Time { return now.Add(8*24*time.Hour + time.Minute) }
	_, ok = CachedCodexPct()
	testutil.Equal(t, ok, false)
}

// TestProbe_PTYFallbackExplicitlyEnabledStillRuns confirms the opt-in itself
// works: a caller that explicitly enables the fallback still gets the
// pre-existing costed-fallback behavior.
func TestProbe_PTYFallbackExplicitlyEnabledStillRuns(t *testing.T) {
	resetCodexState(t)
	SetCodexPTYFallbackEnabled(true)
	codexRolloutProbeRunner = func() (Reading, bool) { return Reading{}, false }
	codexPTYProbeRunner = func(context.Context) ([]byte, error) {
		return []byte("Usage\n  33% used\n"), nil
	}

	testutil.NoError(t, Probe(context.Background()))

	pct, ok := CachedCodexPct()
	testutil.Equal(t, ok, true)
	testutil.Equal(t, pct, 33.0)
}

func TestProbe_PTYFallbackFailureLeavesCacheUnchangedAndLogs(t *testing.T) {
	resetCodexState(t)
	SetCodexPTYFallbackEnabled(true)
	readLog := initTestUxlog(t)
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	codexNowFunc = func() time.Time { return now }
	previous := Reading{Percentage: 55, ResetAt: now.Add(time.Hour), LastProbedAt: now.Add(-time.Minute)}
	storeCodexReading(previous)
	codexRolloutProbeRunner = func() (Reading, bool) { return Reading{}, false }
	codexPTYProbeRunner = func(context.Context) ([]byte, error) {
		return nil, errors.New("boom")
	}

	testutil.NoError(t, Probe(context.Background()))

	got, ok := codexSnapshot()
	testutil.Equal(t, ok, true)
	testutil.DeepEqual(t, got, previous)
	testutil.Contains(t, readLog(), "[backendtier] codex PTY probe failed")
}

func TestProbe_PTYFallbackUnparseableOutputLeavesCacheUnchangedAndLogs(t *testing.T) {
	resetCodexState(t)
	SetCodexPTYFallbackEnabled(true)
	readLog := initTestUxlog(t)
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	codexNowFunc = func() time.Time { return now }
	previous := Reading{Percentage: 55, ResetAt: now.Add(time.Hour), LastProbedAt: now.Add(-time.Minute)}
	storeCodexReading(previous)
	codexRolloutProbeRunner = func() (Reading, bool) { return Reading{}, false }
	codexPTYProbeRunner = func(context.Context) ([]byte, error) {
		return []byte("not a status screen"), nil
	}

	testutil.NoError(t, Probe(context.Background()))

	got, ok := codexSnapshot()
	testutil.Equal(t, ok, true)
	testutil.DeepEqual(t, got, previous)
	testutil.Contains(t, readLog(), "[backendtier] codex PTY probe parse failed")
}

func TestProbe_ContextCancellationIsNotLoggedAsFailure(t *testing.T) {
	resetCodexState(t)
	SetCodexPTYFallbackEnabled(true)
	readLog := initTestUxlog(t)
	codexRolloutProbeRunner = func() (Reading, bool) { return Reading{}, false }
	codexPTYProbeRunner = func(ctx context.Context) ([]byte, error) {
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	testutil.NoError(t, Probe(ctx))

	if strings.Contains(readLog(), "[backendtier] codex PTY probe failed") {
		t.Fatalf("canceled context should not log probe failure: %s", readLog())
	}
}

func TestCachedCodexPct_UnknownBeforeProbe(t *testing.T) {
	resetCodexState(t)

	_, ok := CachedCodexPct()
	testutil.Equal(t, ok, false)
}

func TestCachedCodexPct_StaleReadingReturnsUnknown(t *testing.T) {
	resetCodexState(t)
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	codexNowFunc = func() time.Time { return now }
	// A reading with neither a reset time nor an explicit ValidUntil (a PTY
	// /status reading whose reset didn't parse) is held 24h from the probe
	// (fix-backend-usage-routing); past that it is stale/unknown. Depleted
	// rollout readings instead carry ValidUntil anchored to the rollout mtime.
	storeCodexReading(Reading{Percentage: 90, LastProbedAt: now.Add(-25 * time.Hour)})

	_, ok := CachedCodexPct()
	testutil.Equal(t, ok, false)
}

func TestCachedCodexPct_NeverTriggersLiveProbe(t *testing.T) {
	resetCodexState(t)
	codexCacheSnapshotHook = func() {}
	storeCodexReading(Reading{Percentage: 10, LastProbedAt: time.Now()})

	pct, ok := CachedCodexPct()
	testutil.Equal(t, ok, true)
	testutil.Equal(t, pct, 10.0)
}

func writeRolloutFile(t *testing.T, dir, name string, mod time.Time, lines []string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
	return path
}

func rateLimitsLine(primaryPct, secondaryPct float64, primaryResets, secondaryResets int64) string {
	rec := map[string]any{
		"timestamp": "2026-09-22T00:00:00Z",
		"type":      "event_msg",
		"payload": map[string]any{
			"type": "token_count",
			"rate_limits": map[string]any{
				"primary": map[string]any{
					"used_percent":   primaryPct,
					"window_minutes": 300,
					"resets_at":      primaryResets,
				},
				"secondary": map[string]any{
					"used_percent":   secondaryPct,
					"window_minutes": 10080,
					"resets_at":      secondaryResets,
				},
			},
		},
	}
	b, err := json.Marshal(rec)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestLatestRolloutFile(t *testing.T) {
	t.Run("picks most recently modified file across nested date dirs", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		codexHomeDirFunc = os.UserHomeDir

		old := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
		newer := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
		writeRolloutFile(t, filepath.Join(home, ".codex", "sessions", "2026", "09", "20"), "rollout-a.jsonl", old, []string{"{}"})
		wantPath := writeRolloutFile(t, filepath.Join(home, ".codex", "sessions", "2026", "09", "22"), "rollout-b.jsonl", newer, []string{"{}"})

		files, err := recentRolloutFiles(codexMaxRolloutFiles)
		testutil.NoError(t, err)
		testutil.Equal(t, len(files), 2)
		testutil.Equal(t, files[0].path, wantPath)
		testutil.Equal(t, files[0].modTime.UTC(), newer)
		testutil.Equal(t, files[1].modTime.UTC(), old)
	})

	t.Run("missing sessions dir errors", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		codexHomeDirFunc = os.UserHomeDir

		_, err := recentRolloutFiles(codexMaxRolloutFiles)
		if err == nil {
			t.Fatal("expected error for missing sessions dir")
		}
	})

	t.Run("ignores non-rollout files", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		codexHomeDirFunc = os.UserHomeDir

		dir := filepath.Join(home, ".codex", "sessions", "2026", "09", "22")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hi"), 0o644); err != nil {
			t.Fatal(err)
		}

		_, err := recentRolloutFiles(codexMaxRolloutFiles)
		if err == nil {
			t.Fatal("expected error when no rollout files are present")
		}
	})

	t.Run("returns at most limit files, newest first", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		codexHomeDirFunc = os.UserHomeDir

		base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
		dir := filepath.Join(home, ".codex", "sessions", "2026", "09", "01")
		for i := range 5 {
			writeRolloutFile(t, dir, fmt.Sprintf("rollout-%d.jsonl", i), base.Add(time.Duration(i)*time.Hour), []string{"{}"})
		}

		files, err := recentRolloutFiles(3)
		testutil.NoError(t, err)
		testutil.Equal(t, len(files), 3)
		for i, want := range []int{4, 3, 2} {
			testutil.Equal(t, filepath.Base(files[i].path), fmt.Sprintf("rollout-%d.jsonl", want))
		}
	})
}

func TestParseLatestRateLimits(t *testing.T) {
	t.Run("valid record picks the weekly (10080-minute) window", func(t *testing.T) {
		dir := t.TempDir()
		path := writeRolloutFile(t, dir, "rollout.jsonl", time.Now(), []string{
			rateLimitsLine(10, 60, 1000, 2000),
		})

		got, ok := parseLatestRateLimits(path)
		testutil.Equal(t, ok, true)
		testutil.Equal(t, got.Percentage, 60.0)
		testutil.Equal(t, got.ResetAt, time.Unix(2000, 0))
	})

	t.Run("last matching record wins over earlier ones", func(t *testing.T) {
		dir := t.TempDir()
		path := writeRolloutFile(t, dir, "rollout.jsonl", time.Now(), []string{
			rateLimitsLine(5, 5, 100, 100),
			rateLimitsLine(70, 20, 800, 900),
		})

		// Weekly-only routing (fix-backend-usage-routing): the 300-minute
		// window's 70% is ignored; the 10080-minute window's 20% is the reading.
		got, ok := parseLatestRateLimits(path)
		testutil.Equal(t, ok, true)
		testutil.Equal(t, got.Percentage, 20.0)
		testutil.Equal(t, got.ResetAt, time.Unix(900, 0))
	})

	t.Run("malformed lines are skipped, not fatal", func(t *testing.T) {
		dir := t.TempDir()
		path := writeRolloutFile(t, dir, "rollout.jsonl", time.Now(), []string{
			"not json at all",
			`{"payload": {`,
			rateLimitsLine(30, 40, 1, 2),
		})

		got, ok := parseLatestRateLimits(path)
		testutil.Equal(t, ok, true)
		testutil.Equal(t, got.Percentage, 40.0)
	})

	t.Run("no rate_limits record returns false", func(t *testing.T) {
		dir := t.TempDir()
		path := writeRolloutFile(t, dir, "rollout.jsonl", time.Now(), []string{
			`{"payload":{"type":"token_count"}}`,
			`{}`,
		})

		_, ok := parseLatestRateLimits(path)
		testutil.Equal(t, ok, false)
	})

	t.Run("missing file returns false", func(t *testing.T) {
		_, ok := parseLatestRateLimits(filepath.Join(t.TempDir(), "nope.jsonl"))
		testutil.Equal(t, ok, false)
	})
}

// TestWeeklyCodexWindow pins fix-backend-usage-routing's weekly-only Codex
// reading: the window is selected by its 10080-minute duration, whichever slot
// (primary/secondary) carries it; any other window is ignored. Replaces the
// superseded worse-of-both-windows behavior.
func TestWeeklyCodexWindow(t *testing.T) {
	for _, tc := range []struct {
		name       string
		rl         *codexRateLimits
		wantPct    float64
		wantResets int64
		wantOK     bool
	}{
		{name: "nil rate limits", rl: nil, wantOK: false},
		{
			name:       "weekly in primary, secondary null",
			rl:         &codexRateLimits{Primary: &codexRateWindow{UsedPercent: 79, WindowMinutes: 10080, ResetsAt: 1791761947}},
			wantPct:    79,
			wantResets: 1791761947,
			wantOK:     true,
		},
		{
			name:       "weekly in secondary",
			rl:         &codexRateLimits{Secondary: &codexRateWindow{UsedPercent: 34, WindowMinutes: 10080, ResetsAt: 222}},
			wantPct:    34,
			wantResets: 222,
			wantOK:     true,
		},
		{
			name: "5h window at 95 ignored, weekly at 40 used",
			rl: &codexRateLimits{
				Primary:   &codexRateWindow{UsedPercent: 95, WindowMinutes: 300, ResetsAt: 1},
				Secondary: &codexRateWindow{UsedPercent: 40, WindowMinutes: 10080, ResetsAt: 2},
			},
			wantPct:    40,
			wantResets: 2,
			wantOK:     true,
		},
		{
			name:   "only a non-weekly window is no reading",
			rl:     &codexRateLimits{Primary: &codexRateWindow{UsedPercent: 95, WindowMinutes: 300, ResetsAt: 1}},
			wantOK: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pct, resetAt, ok := weeklyCodexWindow(tc.rl)
			testutil.Equal(t, ok, tc.wantOK)
			if !tc.wantOK {
				return
			}
			testutil.Equal(t, pct, tc.wantPct)
			testutil.Equal(t, resetAt, time.Unix(tc.wantResets, 0))
		})
	}
}

func TestRolloutReading(t *testing.T) {
	t.Run("fresh file is used", func(t *testing.T) {
		resetCodexState(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		codexHomeDirFunc = os.UserHomeDir
		now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
		codexNowFunc = func() time.Time { return now }
		writeRolloutFile(t, filepath.Join(home, ".codex", "sessions", "2026", "09", "22"), "rollout-a.jsonl", now.Add(-5*time.Minute), []string{
			rateLimitsLine(20, 20, now.Add(5*time.Hour).Unix(), now.Add(72*time.Hour).Unix()),
		})

		got, ok := rolloutReading()
		testutil.Equal(t, ok, true)
		testutil.Equal(t, got.Percentage, 20.0)
	})

	// Supersedes the old 1-hour mtime gate (fix-backend-usage-routing): every
	// Codex use writes a new rollout, so the newest rollout stays the best
	// reading until its own weekly reset.
	t.Run("three-day-old file is used while its weekly reset is in the future", func(t *testing.T) {
		resetCodexState(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		codexHomeDirFunc = os.UserHomeDir
		now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
		codexNowFunc = func() time.Time { return now }
		writeRolloutFile(t, filepath.Join(home, ".codex", "sessions", "2026", "09", "19"), "rollout-a.jsonl", now.Add(-72*time.Hour), []string{
			rateLimitsLine(20, 30, now.Add(-70*time.Hour).Unix(), now.Add(24*time.Hour).Unix()),
		})

		got, ok := rolloutReading()
		testutil.Equal(t, ok, true)
		testutil.Equal(t, got.Percentage, 30.0)
	})

	// A newest rollout without rate_limits (a short session) or with an
	// expired reading must not hide an older rollout's still-valid reading.
	t.Run("older valid rollout is used when newer ones are unusable", func(t *testing.T) {
		resetCodexState(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		codexHomeDirFunc = os.UserHomeDir
		now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
		codexNowFunc = func() time.Time { return now }
		dir := filepath.Join(home, ".codex", "sessions", "2026", "09", "22")
		writeRolloutFile(t, dir, "rollout-valid.jsonl", now.Add(-3*time.Hour), []string{
			rateLimitsLine(10, 45, now.Add(time.Hour).Unix(), now.Add(48*time.Hour).Unix()),
		})
		writeRolloutFile(t, dir, "rollout-expired.jsonl", now.Add(-2*time.Hour), []string{
			rateLimitsLine(10, 99, now.Add(-time.Hour).Unix(), now.Add(-time.Minute).Unix()),
		})
		writeRolloutFile(t, dir, "rollout-short.jsonl", now.Add(-time.Hour), []string{
			`{"payload":{"type":"session_meta"}}`,
		})

		got, ok := rolloutReading()
		testutil.Equal(t, ok, true)
		testutil.Equal(t, got.Percentage, 45.0)
	})

	t.Run("only the newest codexMaxRolloutFiles rollouts are considered", func(t *testing.T) {
		resetCodexState(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		codexHomeDirFunc = os.UserHomeDir
		now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
		codexNowFunc = func() time.Time { return now }
		dir := filepath.Join(home, ".codex", "sessions", "2026", "09", "22")
		writeRolloutFile(t, dir, "rollout-oldest.jsonl", now.Add(-100*time.Hour), []string{
			rateLimitsLine(10, 45, now.Add(time.Hour).Unix(), now.Add(48*time.Hour).Unix()),
		})
		for i := range codexMaxRolloutFiles {
			writeRolloutFile(t, dir, fmt.Sprintf("rollout-short-%d.jsonl", i), now.Add(-time.Duration(i+1)*time.Minute), []string{"{}"})
		}

		_, ok := rolloutReading()
		testutil.Equal(t, ok, false)
	})

	// A garbage resets_at far in the future can't keep a reading valid
	// indefinitely: expiry is capped at the rollout's mtime + 8 days.
	t.Run("expiry is capped at rollout mtime plus eight days", func(t *testing.T) {
		resetCodexState(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		codexHomeDirFunc = os.UserHomeDir
		now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
		codexNowFunc = func() time.Time { return now }
		mod := now.Add(-time.Hour)
		writeRolloutFile(t, filepath.Join(home, ".codex", "sessions", "2026", "09", "22"), "rollout-a.jsonl", mod, []string{
			rateLimitsLine(10, 30, now.Add(time.Hour).Unix(), now.Add(400*24*time.Hour).Unix()),
		})

		got, ok := rolloutReading()
		testutil.Equal(t, ok, true)
		testutil.Equal(t, got.ExpiresAt().UTC(), mod.Add(8*24*time.Hour))

		codexNowFunc = func() time.Time { return mod.Add(8*24*time.Hour + time.Minute) }
		_, ok = rolloutReading()
		testutil.Equal(t, ok, false)
	})

	t.Run("missing sessions dir is rejected", func(t *testing.T) {
		resetCodexState(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		codexHomeDirFunc = os.UserHomeDir

		_, ok := rolloutReading()
		testutil.Equal(t, ok, false)
	})
}

func TestParseCodexStatusOutput(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)

	t.Run("percentage and reset parse", func(t *testing.T) {
		resetCodexState(t)
		codexNowFunc = func() time.Time { return now }

		got, err := parseCodexStatusOutput("Usage\n  5h limit: 12% used\n  Weekly limit: 87% used\n  resets in 3h 20m\n")
		testutil.NoError(t, err)
		testutil.Equal(t, got.Percentage, 87.0)
		testutil.Equal(t, got.ResetAt, now.Add(3*time.Hour+20*time.Minute))
	})

	t.Run("percentage without reset still succeeds", func(t *testing.T) {
		resetCodexState(t)
		codexNowFunc = func() time.Time { return now }

		got, err := parseCodexStatusOutput("Weekly limit: 40% used\n")
		testutil.NoError(t, err)
		testutil.Equal(t, got.Percentage, 40.0)
		testutil.Equal(t, got.ResetAt.IsZero(), true)
	})

	// Weekly-only routing: a hot 5h row never stands in for the weekly one,
	// and the reset is the weekly row's own.
	t.Run("weekly row is used and the 5h row ignored", func(t *testing.T) {
		resetCodexState(t)
		codexNowFunc = func() time.Time { return now }

		got, err := parseCodexStatusOutput("Usage\n  5h limit: 95% used\n  resets in 2h\n  Weekly limit: 40% used\n  resets in 3d 4h\n")
		testutil.NoError(t, err)
		testutil.Equal(t, got.Percentage, 40.0)
		testutil.Equal(t, got.ResetAt, now.Add(3*24*time.Hour+4*time.Hour))
	})

	t.Run("weekly row with its reset on the same line", func(t *testing.T) {
		resetCodexState(t)
		codexNowFunc = func() time.Time { return now }

		got, err := parseCodexStatusOutput("5h limit: 95% used (resets in 1h)\nWeekly limit: 22% used (resets in 5d)\n")
		testutil.NoError(t, err)
		testutil.Equal(t, got.Percentage, 22.0)
		testutil.Equal(t, got.ResetAt, now.Add(5*24*time.Hour))
	})

	t.Run("weekly row without a reset does not borrow the 5h reset", func(t *testing.T) {
		resetCodexState(t)
		codexNowFunc = func() time.Time { return now }

		got, err := parseCodexStatusOutput("5h limit: 95% used\nresets in 2h\nWeekly limit: 40% used\n")
		testutil.NoError(t, err)
		testutil.Equal(t, got.Percentage, 40.0)
		testutil.Equal(t, got.ResetAt.IsZero(), true)
	})

	t.Run("only a 5h row is no reading", func(t *testing.T) {
		resetCodexState(t)
		codexNowFunc = func() time.Time { return now }

		if _, err := parseCodexStatusOutput("5h limit: 95% used\nresets in 2h\n"); err == nil {
			t.Fatal("expected an error when only the 5h row is present")
		}
	})

	// Unlabeled rows: the weekly window is the one resetting furthest out.
	t.Run("unlabeled rows prefer the furthest reset", func(t *testing.T) {
		resetCodexState(t)
		codexNowFunc = func() time.Time { return now }

		got, err := parseCodexStatusOutput("Usage\n  95% used\n  resets in 2h\n  40% used\n  resets in 4d\n")
		testutil.NoError(t, err)
		testutil.Equal(t, got.Percentage, 40.0)
		testutil.Equal(t, got.ResetAt, now.Add(4*24*time.Hour))
	})

	t.Run("unlabeled rows with same-line resets prefer the furthest reset", func(t *testing.T) {
		resetCodexState(t)
		codexNowFunc = func() time.Time { return now }

		got, err := parseCodexStatusOutput("95% used (resets in 2h)\n40% used (resets in 4d)\n")
		testutil.NoError(t, err)
		testutil.Equal(t, got.Percentage, 40.0)
	})

	t.Run("no percentage found is an error", func(t *testing.T) {
		_, err := parseCodexStatusOutput("codex has no usage information here")
		if err == nil {
			t.Fatal("expected an error when no usage percentage is present")
		}
	})

	t.Run("unrelated percentage without used keyword is ignored", func(t *testing.T) {
		_, err := parseCodexStatusOutput("Progress: 50%\n")
		if err == nil {
			t.Fatal("expected an error since no usage percentage is present")
		}
	})
}

func TestParseCodexResetDuration(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      string
		want    time.Duration
		wantErr bool
	}{
		{name: "hours and minutes", in: "3h 20m", want: 3*time.Hour + 20*time.Minute},
		{name: "days only", in: "4d", want: 4 * 24 * time.Hour},
		{name: "days hours minutes", in: "1d 2h 3m", want: 24*time.Hour + 2*time.Hour + 3*time.Minute},
		{name: "empty is an error", in: "", wantErr: true},
		{name: "garbage is an error", in: "soon", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseCodexResetDuration(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tc.in)
				}
				return
			}
			testutil.NoError(t, err)
			testutil.Equal(t, got, tc.want)
		})
	}
}

func TestRenderCodexStatusOutput_StripsANSI(t *testing.T) {
	raw := []byte("\x1b[2J\x1b[Hplain text here")
	got := renderCodexStatusOutput(raw)
	testutil.Contains(t, got, "plain text here")
	if bytes.Contains([]byte(got), []byte("\x1b")) {
		t.Fatalf("expected ANSI-free output, got %q", got)
	}
}
