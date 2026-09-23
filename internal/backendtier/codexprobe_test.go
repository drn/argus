package backendtier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

	t.Cleanup(func() {
		codexCache.mu.Lock()
		codexCache.reading = Reading{}
		codexCache.ok = false
		codexCache.mu.Unlock()
		codexRolloutProbeRunner = rolloutReading
		codexPTYProbeRunner = runCodexPTYProbe
		codexNowFunc = time.Now
		codexCacheSnapshotHook = nil
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

func TestProbe_PTYFallbackFailureLeavesCacheUnchangedAndLogs(t *testing.T) {
	resetCodexState(t)
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
	storeCodexReading(Reading{Percentage: 90, LastProbedAt: now.Add(-2 * time.Hour)})

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

		gotPath, gotMod, err := latestRolloutFile()
		testutil.NoError(t, err)
		testutil.Equal(t, gotPath, wantPath)
		testutil.Equal(t, gotMod.UTC(), newer)
	})

	t.Run("missing sessions dir errors", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		codexHomeDirFunc = os.UserHomeDir

		_, _, err := latestRolloutFile()
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

		_, _, err := latestRolloutFile()
		if err == nil {
			t.Fatal("expected error when no rollout files are present")
		}
	})
}

func TestParseLatestRateLimits(t *testing.T) {
	t.Run("valid record picks the worse of the two windows", func(t *testing.T) {
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
			rateLimitsLine(70, 20, 900, 900),
		})

		got, ok := parseLatestRateLimits(path)
		testutil.Equal(t, ok, true)
		testutil.Equal(t, got.Percentage, 70.0)
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

func TestWorstCodexWindow(t *testing.T) {
	for _, tc := range []struct {
		name       string
		rl         *codexRateLimits
		wantPct    float64
		wantResets int64
		wantOK     bool
	}{
		{name: "nil rate limits", rl: nil, wantOK: false},
		{name: "primary only", rl: &codexRateLimits{Primary: &codexRateWindow{UsedPercent: 12, ResetsAt: 111}}, wantPct: 12, wantResets: 111, wantOK: true},
		{name: "secondary only", rl: &codexRateLimits{Secondary: &codexRateWindow{UsedPercent: 34, ResetsAt: 222}}, wantPct: 34, wantResets: 222, wantOK: true},
		{
			name:       "secondary higher wins",
			rl:         &codexRateLimits{Primary: &codexRateWindow{UsedPercent: 10, ResetsAt: 1}, Secondary: &codexRateWindow{UsedPercent: 90, ResetsAt: 2}},
			wantPct:    90,
			wantResets: 2,
			wantOK:     true,
		},
		{
			name:       "primary higher wins",
			rl:         &codexRateLimits{Primary: &codexRateWindow{UsedPercent: 95, ResetsAt: 1}, Secondary: &codexRateWindow{UsedPercent: 3, ResetsAt: 2}},
			wantPct:    95,
			wantResets: 1,
			wantOK:     true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pct, resetAt, ok := worstCodexWindow(tc.rl)
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
			rateLimitsLine(20, 20, 1, 1),
		})

		got, ok := rolloutReading()
		testutil.Equal(t, ok, true)
		testutil.Equal(t, got.Percentage, 20.0)
	})

	t.Run("stale file is rejected", func(t *testing.T) {
		resetCodexState(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		codexHomeDirFunc = os.UserHomeDir
		now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
		codexNowFunc = func() time.Time { return now }
		writeRolloutFile(t, filepath.Join(home, ".codex", "sessions", "2026", "09", "20"), "rollout-a.jsonl", now.Add(-2*time.Hour), []string{
			rateLimitsLine(20, 20, 1, 1),
		})

		_, ok := rolloutReading()
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
