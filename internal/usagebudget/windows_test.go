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
)

func TestQuotaWindowsExpireIndependently(t *testing.T) {
	resetState(t)
	base := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	nowFunc = func() time.Time { return base }
	output := "Current session\n96% used\nResets 1pm (UTC)\nCurrent week (all models)\n75% used\nResets Oct 31 at 1pm (UTC)"
	reading, err := parseUsageOutput(output, base)
	testutil.NoError(t, err)
	reading.LastProbedAt = base
	storeReading(reading)
	pct, ok := CachedClaudePct()
	testutil.True(t, ok)
	testutil.Equal(t, pct, 96.0)
	nowFunc = func() time.Time { return base.Add(time.Hour) }
	pct, ok = CachedClaudePct()
	testutil.True(t, ok)
	testutil.Equal(t, pct, 75.0)
}

func TestSessionOnlyProbeStoresOnEOF(t *testing.T) {
	resetState(t)
	nowFunc = func() time.Time { return time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC) }
	f := newFakeSession([]byte("Current session\n96% used\nResets 1pm (UTC)\n"))
	f.exitWhenEmpty = true
	installFakeSession(t, f, t.TempDir())
	testutil.NoError(t, Probe(context.Background()))
	pct, ok := CachedClaudePct()
	testutil.True(t, ok)
	testutil.Equal(t, pct, 96.0)
}

func TestAccountUsageLauncherUsesFakeCLI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	bin := t.TempDir()
	testutil.NoError(t, os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nprintf 'args=%s,%s cfg=%s END' \"$1\" \"$2\" \"$CLAUDE_CONFIG_DIR\"\n"), 0o700))
	t.Setenv("PATH", bin+":/bin")
	dir := t.TempDir()
	s, err := startAccountUsageSession(context.Background(), dir, dir)
	testutil.NoError(t, err)
	testutil.Contains(t, readUntil(t, s, "END", 5*time.Second), "args=--,/usage cfg="+dir)
	testutil.NoError(t, s.Terminate())
	_, err = startAccountUsageSession(context.Background(), filepath.Join(dir, "missing"), dir)
	testutil.Error(t, err)
}

func TestWindowSelectionValidity(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name            string
		session, weekly *Window
		want            float64
		known           bool
		window          string
	}{
		{"both expired", &Window{Percentage: 96, ResetAt: now}, &Window{Percentage: 75, ResetAt: now}, 0, false, ""},
		{"weekly fuller", &Window{Percentage: 20, ResetAt: now.Add(time.Hour), LastProbedAt: now}, &Window{Percentage: 75, ResetAt: now.Add(time.Hour), LastProbedAt: now}, 75, true, "seven_day"},
		{"stale session", &Window{Percentage: 96, ResetAt: now.Add(time.Hour), LastProbedAt: now.Add(-2 * time.Hour)}, &Window{Percentage: 75, ResetAt: now.Add(time.Hour), LastProbedAt: now}, 75, true, "seven_day"},
		{"session only", &Window{Percentage: 96, ResetAt: now.Add(time.Hour)}, nil, 96, true, "five_hour"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, ok := SelectReading(Reading{FiveHour: tc.session, SevenDay: tc.weekly, LastProbedAt: now}, now)
			testutil.Equal(t, ok, tc.known)
			if ok {
				testutil.Equal(t, r.Percentage, tc.want)
				testutil.Equal(t, r.Window, tc.window)
			}
		})
	}
}

func TestSessionParseAndPartialFrame(t *testing.T) {
	resetState(t)
	base := time.Date(2026, 10, 30, 23, 0, 0, 0, time.UTC)
	nowFunc = func() time.Time { return base }
	session := "Current session\n96% used\nResets 1am (UTC)\n"
	testutil.False(t, storeIfParsed(session))
	r, err := parseUsageOutput(session, base)
	testutil.NoError(t, err)
	testutil.Equal(t, r.ResetAt, base.Add(2*time.Hour))
	testutil.Equal(t, r.Percentage, 96.0)
	testutil.True(t, storeIfParsed(session+"Current week (all models)\n99% used\nResets Oct 31 at 1pm (UTC)"))
	pct, ok := CachedClaudePct()
	testutil.True(t, ok)
	testutil.Equal(t, pct, 99.0)
	for _, text := range []string{
		"Current session\n101% used\nResets 1am (UTC)",
		"Current session\n-1% used\nResets 1am (UTC)",
		"Current session\n96% used\nResets Oct 30 at 1pm (UTC)",
		"Current session\n96% used\nResets 10pm (UTC)",
		"Current week (all models)\n96% used\nResets Oct 28 at 1pm (UTC)",
	} {
		t.Run(text, func(t *testing.T) { _, err := parseUsageOutput(text, base); testutil.Error(t, err) })
	}
}

func TestRefreshingEOFLeavesCacheUntouched(t *testing.T) {
	resetState(t)
	nowFunc = func() time.Time { return time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC) }
	finishWithoutSession([]byte("Refreshing...\nCurrent session\n96% used\nResets 1pm (UTC)\n"))
	_, ok := CachedClaudePct()
	testutil.False(t, ok)
}

func TestPartialMeasurementKeepsOtherWindowAge(t *testing.T) {
	resetState(t)
	now := time.Now()
	nowFunc = func() time.Time { return now }
	session := &Window{Percentage: 96, ResetAt: now.Add(3 * time.Hour), LastProbedAt: now.Add(-59 * time.Minute)}
	storeReading(Reading{FiveHour: session, LastProbedAt: session.LastProbedAt})
	weekly := &Window{Percentage: 75, ResetAt: now.Add(24 * time.Hour), LastProbedAt: now}
	storeReading(Reading{SevenDay: weekly, LastProbedAt: now})
	pct, ok := CachedClaudePct()
	testutil.True(t, ok)
	testutil.Equal(t, pct, 96.0)
	nowFunc = func() time.Time { return now.Add(2 * time.Minute) }
	pct, ok = CachedClaudePct()
	testutil.True(t, ok)
	testutil.Equal(t, pct, 75.0)
}

func TestNamedProbeFailureAndDeduplication(t *testing.T) {
	resetState(t)
	cfg := config.DefaultConfig()
	cfg.DefaultAccount = "personal"
	cfg.Accounts = map[string]config.Account{"personal": {ClaudeConfigDir: t.TempDir()}}
	cfg.BackendRouting.Tiers = []config.BackendTier{{Backend: "claude", Account: "personal", Probe: config.ProbeClaudeUsage}, {Backend: "claude", Account: "missing", Probe: config.ProbeClaudeUsage}}
	now := time.Now()
	storeReading(Reading{Percentage: 42, LastProbedAt: now, ResetAt: now.Add(time.Hour)}, "personal")
	orig := startAccountUsageSession
	defer func() { startAccountUsageSession = orig }()
	calls := 0
	startAccountUsageSession = func(context.Context, string, string) (usageSession, error) {
		calls++
		return nil, errors.New("not logged in")
	}
	readLog, readSlog := initTestUxlog(t), captureSlog(t)
	testutil.NoError(t, ProbeConfiguredAccounts(context.Background(), cfg))
	testutil.Equal(t, calls, 1)
	pct, ok := CachedClaudePctForAccount("personal")
	testutil.True(t, ok)
	testutil.Equal(t, pct, 42.0)
	testutil.Contains(t, readLog(), "not logged in")
	testutil.Contains(t, readSlog(), "missing")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	testutil.NoError(t, ProbeConfiguredAccounts(ctx, cfg))
	testutil.Equal(t, calls, 1)
}

func TestAccountPTYUsesIdentityAndStripsAuth(t *testing.T) {
	for _, name := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDECODE"} {
		t.Setenv(name, "test-placeholder")
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "inherited")
	dir := t.TempDir()
	s, err := startAccountPTYSession(context.Background(), dir, dir, "sh", "-c", `printf 'cfg=%s api=%s auth=%s oauth=%s child=%s END' "$CLAUDE_CONFIG_DIR" "$ANTHROPIC_API_KEY" "$ANTHROPIC_AUTH_TOKEN" "$CLAUDE_CODE_OAUTH_TOKEN" "$CLAUDECODE"`)
	testutil.NoError(t, err)
	defer s.Terminate() //nolint:errcheck
	out := readUntil(t, s, "END", 5*time.Second)
	testutil.Contains(t, out, "cfg="+dir+" api= auth= oauth= child= END")
	testutil.False(t, strings.Contains(out, "test-placeholder"))
}

func TestNamedProbeIsolatedAndBridgeSkips(t *testing.T) {
	resetState(t)
	base := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	nowFunc = func() time.Time { return base }
	cfg := config.DefaultConfig()
	cfg.Accounts = map[string]config.Account{"personal": {ClaudeConfigDir: t.TempDir()}}
	cfg.BackendRouting.Tiers = []config.BackendTier{{Backend: "claude", Account: "personal", Probe: config.ProbeClaudeUsage}}
	orig := startAccountUsageSession
	defer func() { startAccountUsageSession = orig }()
	var dirs []string
	startAccountUsageSession = func(ctx context.Context, dir, configDir string) (usageSession, error) {
		dirs = append(dirs, configDir)
		return newFakeSession(sampleUsageFrame()), nil
	}
	testutil.NoError(t, ProbeConfiguredAccounts(context.Background(), cfg))
	testutil.DeepEqual(t, dirs, []string{cfg.Accounts["personal"].ClaudeConfigDir})
	pct, ok := CachedClaudePctForAccount("personal")
	testutil.True(t, ok)
	testutil.Equal(t, pct, 94.5)
	_, ok = CachedClaudePct()
	testutil.False(t, ok)
	restore := SetBridgeReader(func(account string) (Reading, bool) { return Reading{Percentage: 1}, account == "personal" })
	defer restore()
	testutil.NoError(t, ProbeConfiguredAccounts(context.Background(), cfg))
	testutil.Equal(t, len(dirs), 1)
}
