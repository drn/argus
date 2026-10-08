package backendtier

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

// Tests for fix-backend-usage-routing's backend-tier-routing delta: Codex
// weekly-window selection, the depleted=100% reading, validity-until-reset,
// and slog (daemon.log) visibility of codex probe outcomes.

// fixtureNow is 2026-10-07 12:00 UTC: before every fixture's weekly resets_at
// (1791761947 = 2026-10-11 23:39:07 UTC).
var fixtureNow = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

const fixtureWeeklyResets int64 = 1791761947

// captureSlog swaps the default slog logger for a buffer-backed one (slog is
// what reaches daemon.log) and restores it on cleanup.
func captureSlog(t *testing.T) func() string {
	t.Helper()
	var (
		mu  sync.Mutex
		buf bytes.Buffer
	)
	orig := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&lockedWriter{mu: &mu, w: &buf}, nil)))
	t.Cleanup(func() { slog.SetDefault(orig) })
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	}
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// installFixtureRollout copies testdata/<name> into a temp HOME's Codex
// sessions tree with the given mtime and points the probe at that HOME.
func installFixtureRollout(t *testing.T, name string, mod time.Time) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	testutil.NoError(t, err)
	home := t.TempDir()
	t.Setenv("HOME", home)
	origHome := codexHomeDirFunc
	codexHomeDirFunc = os.UserHomeDir
	t.Cleanup(func() { codexHomeDirFunc = origHome })
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	writeRolloutFile(t, filepath.Join(home, ".codex", "sessions", "2026", "10", "04"), "rollout-fixture.jsonl", mod, lines)
}

// Scenario: Weekly window is found by duration, not slot.
func TestParseLatestRateLimits_WeeklyInPrimaryFixture(t *testing.T) {
	got, ok := parseLatestRateLimits(filepath.Join("testdata", "rollout_weekly_primary.jsonl"))
	testutil.Equal(t, ok, true)
	testutil.Equal(t, got.Percentage, 79.0)
	testutil.Equal(t, got.ResetAt, time.Unix(fixtureWeeklyResets, 0))
}

// Scenario: Non-weekly windows are ignored (300@95 + 10080@40 → 40).
func TestParseLatestRateLimits_MixedWindowsFixtureUsesWeekly(t *testing.T) {
	got, ok := parseLatestRateLimits(filepath.Join("testdata", "rollout_mixed_windows.jsonl"))
	testutil.Equal(t, ok, true)
	testutil.Equal(t, got.Percentage, 40.0)
	testutil.Equal(t, got.ResetAt, time.Unix(fixtureWeeklyResets, 0))
}

// Scenario: Depleted account reads as full. The fixture's latest record has
// rate_limit_reached_type set and both windows null (an earlier record had a
// 97% weekly window — the latest record wins).
func TestParseLatestRateLimits_DepletedFixtureReadsFull(t *testing.T) {
	got, ok := parseLatestRateLimits(filepath.Join("testdata", "rollout_depleted.jsonl"))
	testutil.Equal(t, ok, true)
	testutil.Equal(t, got.Percentage, 100.0)
	// Reset time unknown for a depleted record.
	testutil.Equal(t, got.ResetAt.IsZero(), true)
}

// A depleted reading is held 24h from the rollout file's mtime, not from the
// probe: re-probing the same unchanged file never extends the hold.
func TestProbe_DepletedReadingHeld24hFromRolloutMtime(t *testing.T) {
	resetCodexState(t)
	mtime := fixtureNow
	now := mtime
	codexNowFunc = func() time.Time { return now }
	installFixtureRollout(t, "rollout_depleted.jsonl", mtime)
	codexRolloutProbeRunner = rolloutReading

	for _, offset := range []time.Duration{time.Hour, 23 * time.Hour} {
		now = mtime.Add(offset)
		testutil.NoError(t, Probe(context.Background()))

		pct, ok := CachedCodexPct()
		testutil.Equal(t, ok, true)
		testutil.Equal(t, pct, 100.0)
	}

	// The probe just ran against the same file, yet the hold has lapsed.
	now = mtime.Add(25 * time.Hour)
	testutil.NoError(t, Probe(context.Background()))

	_, ok := CachedCodexPct()
	testutil.Equal(t, ok, false)
}

// A depleted rollout whose mtime is already more than 24h old yields no
// reading at all, even on the very first probe.
func TestProbe_DepletedRolloutOlderThan24hIsUnknown(t *testing.T) {
	resetCodexState(t)
	codexNowFunc = func() time.Time { return fixtureNow }
	installFixtureRollout(t, "rollout_depleted.jsonl", fixtureNow.Add(-72*time.Hour))
	codexRolloutProbeRunner = rolloutReading

	testutil.NoError(t, Probe(context.Background()))

	_, ok := CachedCodexPct()
	testutil.Equal(t, ok, false)
}

// Scenario: Old rollout stays valid until its reset — recorded as fresh
// without spawning a `codex` subprocess, even with the costed fallback on.
func TestProbe_OldRolloutValidUntilResetSkipsSubprocess(t *testing.T) {
	resetCodexState(t)
	SetCodexPTYFallbackEnabled(true)
	codexNowFunc = func() time.Time { return fixtureNow }
	installFixtureRollout(t, "rollout_weekly_primary.jsonl", fixtureNow.Add(-72*time.Hour))
	codexRolloutProbeRunner = rolloutReading
	codexPTYProbeRunner = func(context.Context) ([]byte, error) {
		t.Fatal("a 3-day-old rollout whose weekly reset is still ahead must not trigger the codex subprocess")
		return nil, nil
	}

	testutil.NoError(t, Probe(context.Background()))

	pct, ok := CachedCodexPct()
	testutil.Equal(t, ok, true)
	testutil.Equal(t, pct, 79.0)
}

// The cached reading stays fresh until its weekly reset, not just for an hour
// after the probe.
func TestCachedCodexPct_FreshUntilResetsAt(t *testing.T) {
	resetCodexState(t)
	codexNowFunc = func() time.Time { return fixtureNow }
	storeCodexReading(Reading{Percentage: 79, ResetAt: fixtureNow.Add(24 * time.Hour), LastProbedAt: fixtureNow.Add(-2 * time.Hour)})

	pct, ok := CachedCodexPct()
	testutil.Equal(t, ok, true)
	testutil.Equal(t, pct, 79.0)
}

// Scenario: Reading past its reset is unknown.
func TestCachedCodexPct_PastResetIsUnknown(t *testing.T) {
	resetCodexState(t)
	codexNowFunc = func() time.Time { return fixtureNow }
	storeCodexReading(Reading{Percentage: 79, ResetAt: fixtureNow.Add(-time.Minute), LastProbedAt: fixtureNow.Add(-time.Minute)})

	_, ok := CachedCodexPct()
	testutil.Equal(t, ok, false)
}

// Scenario: Reading past its reset is unknown — end to end through Probe with
// a freshly written rollout whose weekly resets_at has already passed.
func TestProbe_RolloutPastResetLeavesUnknown(t *testing.T) {
	resetCodexState(t)
	codexNowFunc = func() time.Time { return fixtureNow }
	home := t.TempDir()
	t.Setenv("HOME", home)
	codexHomeDirFunc = os.UserHomeDir
	codexRolloutProbeRunner = rolloutReading
	writeRolloutFile(t, filepath.Join(home, ".codex", "sessions", "2026", "10", "07"), "rollout-a.jsonl", fixtureNow.Add(-time.Minute), []string{
		rateLimitsLine(10, 55, fixtureNow.Add(-2*time.Hour).Unix(), fixtureNow.Add(-time.Hour).Unix()),
	})

	testutil.NoError(t, Probe(context.Background()))

	_, ok := CachedCodexPct()
	testutil.Equal(t, ok, false)
}

// A rollout reading already past its weekly reset is "no usable reading", so
// an opted-in PTY fallback runs instead of caching the expired value.
func TestProbe_RolloutPastResetFallsBackToPTYWhenEnabled(t *testing.T) {
	resetCodexState(t)
	SetCodexPTYFallbackEnabled(true)
	codexNowFunc = func() time.Time { return fixtureNow }
	home := t.TempDir()
	t.Setenv("HOME", home)
	codexHomeDirFunc = os.UserHomeDir
	codexRolloutProbeRunner = rolloutReading
	writeRolloutFile(t, filepath.Join(home, ".codex", "sessions", "2026", "10", "07"), "rollout-a.jsonl", fixtureNow.Add(-time.Minute), []string{
		rateLimitsLine(10, 55, fixtureNow.Add(-2*time.Hour).Unix(), fixtureNow.Add(-time.Hour).Unix()),
	})
	codexPTYProbeRunner = func(context.Context) ([]byte, error) {
		return []byte("Weekly limit: 12% used\n  resets in 6d\n"), nil
	}

	testutil.NoError(t, Probe(context.Background()))

	pct, ok := CachedCodexPct()
	testutil.Equal(t, ok, true)
	testutil.Equal(t, pct, 12.0)
}

// Every codex probe outcome is logged through slog so it reaches daemon.log
// (uxlog is a silent no-op in the daemon process).
func TestProbe_CodexOutcomesReachDaemonLog(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		resetCodexState(t)
		readSlog := captureSlog(t)
		codexNowFunc = func() time.Time { return fixtureNow }
		codexRolloutProbeRunner = func() (Reading, bool) {
			return Reading{Percentage: 42, ResetAt: fixtureNow.Add(24 * time.Hour)}, true
		}

		testutil.NoError(t, Probe(context.Background()))

		log := readSlog()
		testutil.Contains(t, log, "[backendtier] codex probe updated")
		testutil.Contains(t, log, "42")
	})

	t.Run("PTY probe failure", func(t *testing.T) {
		resetCodexState(t)
		SetCodexPTYFallbackEnabled(true)
		readSlog := captureSlog(t)
		codexRolloutProbeRunner = func() (Reading, bool) { return Reading{}, false }
		codexPTYProbeRunner = func(context.Context) ([]byte, error) {
			return nil, context.DeadlineExceeded
		}

		testutil.NoError(t, Probe(context.Background()))

		testutil.Contains(t, readSlog(), "[backendtier] codex PTY probe failed")
	})

	t.Run("PTY probe parse failure", func(t *testing.T) {
		resetCodexState(t)
		SetCodexPTYFallbackEnabled(true)
		readSlog := captureSlog(t)
		codexRolloutProbeRunner = func() (Reading, bool) { return Reading{}, false }
		codexPTYProbeRunner = func(context.Context) ([]byte, error) {
			return []byte("not a status screen"), nil
		}

		testutil.NoError(t, Probe(context.Background()))

		testutil.Contains(t, readSlog(), "[backendtier] codex PTY probe parse failed")
	})
}
