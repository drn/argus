package usagebudget

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

// Tests for fix-backend-usage-routing's usage-budget-routing delta: the
// streaming /usage probe, its dedicated probe directory + trust-dialog
// handling, and daemon-log (slog) visibility of every outcome.

// trustFixtureDir is the workspace path the captured folder-trust dialog
// (testdata/trust_dialog.raw) names. The dialog wraps it across two screen
// lines at 100 columns, so matching it requires joining the wrapped lines.
const trustFixtureDir = "/private/tmp/claude-501/-Users-user1-Development-Personal-argus/6b703f3d-51c2-4037-af34-619b9fc1d10b/scratchpad/probe-dir"

// fixtureBase is the wall-clock date the real fixtures were captured on.
func fixtureBase(t *testing.T) (time.Time, *time.Location) {
	t.Helper()
	loc, err := time.LoadLocation("America/Los_Angeles")
	testutil.NoError(t, err)
	return time.Date(2026, 10, 7, 22, 43, 0, 0, loc), loc
}

// --- Requirement: incremental read, stop on success -----------------------

// Scenario: Reading stored before process exit.
func TestProbe_StreamingStoresReadingAndTerminatesBeforeExit(t *testing.T) {
	resetState(t)
	readSlog := captureSlog(t)
	base, loc := fixtureBase(t)
	nowFunc = func() time.Time { return base }
	f := newFakeSession(chunked(readFixture(t, "usage_100x80.raw"), 256)...)
	installFakeSession(t, f, t.TempDir())

	testutil.NoError(t, Probe(context.Background()))

	got, ok := snapshot()
	testutil.Equal(t, ok, true)
	testutil.Equal(t, got.Percentage, 51.0)
	if want := time.Date(2026, 10, 13, 1, 0, 0, 0, loc); !got.ResetAt.Equal(want) {
		t.Errorf("ResetAt = %v, want %v", got.ResetAt, want)
	}
	terminated, selfExited, _ := f.state()
	testutil.Equal(t, terminated, true)
	// The real interactive /usage session never exits; the probe must stop it
	// rather than wait for EOF.
	testutil.Equal(t, selfExited, false)
	testutil.Contains(t, readSlog(), "[usagebudget] probe updated")
}

// The probe is bounded by a timeout; on timeout the cache is unchanged and the
// timeout is logged to the daemon log.
func TestProbe_StreamingTimeoutLeavesCacheAndLogs(t *testing.T) {
	resetState(t)
	readSlog := captureSlog(t)
	orig := probeTimeout
	probeTimeout = 100 * time.Millisecond
	t.Cleanup(func() { probeTimeout = orig })
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	nowFunc = func() time.Time { return now }
	previous := Reading{Percentage: 33, ResetAt: now.Add(48 * time.Hour), LastProbedAt: now.Add(-10 * time.Minute)}
	storeReading(previous)
	f := newFakeSession([]byte("\x1b[2J\x1b[HLoading usage…"))
	installFakeSession(t, f, t.TempDir())

	testutil.NoError(t, Probe(context.Background()))

	got, ok := snapshot()
	testutil.Equal(t, ok, true)
	testutil.DeepEqual(t, got, previous)
	terminated, _, _ := f.state()
	testutil.Equal(t, terminated, true)
	testutil.Contains(t, readSlog(), "[usagebudget] probe timed out")
}

// The probe needs a PTY tall enough that the weekly section is on screen: at
// 24 rows Claude Code renders "Current session" and a scroll arrow, with the
// weekly section below the fold.
func TestUsagePTYIsTallEnoughForWeeklySection(t *testing.T) {
	if ptyRows < 80 {
		t.Fatalf("ptyRows = %d, want >= 80 (weekly section is below the fold at 24 rows)", ptyRows)
	}
}

// --- Parser: percentage on the line after the header ----------------------

// Scenario: Percentage on the line after the header (hand-written shape).
func TestParseUsageOutput_PercentageOnLineAfterHeader(t *testing.T) {
	base, loc := fixtureBase(t)
	out := "Current week (all models)\n" +
		"█████████████████████▌                         41% used\n" +
		"Resets Oct 13 at 1am (America/Los_Angeles)\n"

	got, err := parseUsageOutput(out, base)
	testutil.NoError(t, err)

	testutil.Equal(t, got.Percentage, 41.0)
	if want := time.Date(2026, 10, 13, 1, 0, 0, 0, loc); !got.ResetAt.Equal(want) {
		t.Errorf("ResetAt = %v, want %v", got.ResetAt, want)
	}
}

// Scenario: Percentage on the line after the header, against the real
// captured 100x80 /usage screen. Other sections' percentages (Current session
// 62%, Current week (Fable) 0%, Usage credits 100%) must not be mistaken for
// the weekly all-models one.
func TestParseUsageOutput_RealFixture(t *testing.T) {
	base, loc := fixtureBase(t)

	got, err := parseUsageOutput(renderUsageOutput(readFixture(t, "usage_100x80.raw")), base)
	testutil.NoError(t, err)

	testutil.Equal(t, got.Percentage, 51.0)
	if want := time.Date(2026, 10, 13, 1, 0, 0, 0, loc); !got.ResetAt.Equal(want) {
		t.Errorf("ResetAt = %v, want %v", got.ResetAt, want)
	}
}

func TestParseUsageOutput_IgnoresOtherSections(t *testing.T) {
	base, _ := fixtureBase(t)

	t.Run("weekly reading among other sections", func(t *testing.T) {
		out := strings.Join([]string{
			"Current session",
			"███████████████████████████████                    62% used",
			"Resets 2:30am (America/Los_Angeles)",
			"Current week (all models)",
			"█████████████████████████▌                         51% used",
			"Resets Oct 13 at 1am (America/Los_Angeles)",
			"Current week (Fable)",
			"                                                   0% used",
			"Resets Oct 13 at 1am (America/Los_Angeles)",
			"Usage credits",
			"██████████████████████████████████████████████████ 100% used",
			"$591.23 / $0.00 spent · Resets Nov 1 (America/Los_Angeles)",
		}, "\n")

		got, err := parseUsageOutput(out, base)
		testutil.NoError(t, err)
		testutil.Equal(t, got.Percentage, 51.0)
	})

	t.Run("weekly header without its own percentage does not borrow the next section's", func(t *testing.T) {
		out := strings.Join([]string{
			"Current session",
			"62% used",
			"Current week (all models)",
			"Resets Oct 13 at 1am (America/Los_Angeles)",
			"Current week (Fable)",
			"0% used",
			"Resets Oct 13 at 1am (America/Los_Angeles)",
		}, "\n")

		if _, err := parseUsageOutput(out, base); err == nil {
			t.Fatal("expected a parse error when the weekly section has no percentage")
		}
	})
}

// --- Requirement: dedicated probe directory + trust dialog -----------------

func TestEnsureProbeDir_CreatesDirUnderArgusDataDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir, err := ensureProbeDir()
	if err != nil {
		t.Fatalf("ensureProbeDir: %v", err)
	}

	testutil.Equal(t, dir, filepath.Join(home, ".argus", "usage-probe"))
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat probe dir: %v", err)
	}
	testutil.Equal(t, info.IsDir(), true)

	// Idempotent: a second call succeeds on the existing directory.
	again, err := ensureProbeDir()
	testutil.NoError(t, err)
	testutil.Equal(t, again, dir)
}

func TestProbe_RunsSessionInProbeDir(t *testing.T) {
	resetState(t)
	base, _ := fixtureBase(t)
	nowFunc = func() time.Time { return base }
	dir := t.TempDir()
	f := newFakeSession(readFixture(t, "usage_100x80.raw"))
	gotDir := installFakeSession(t, f, dir)

	testutil.NoError(t, Probe(context.Background()))

	testutil.Equal(t, *gotDir, dir)
}

// trustingSession scripts Claude Code's folder-trust dialog: it shows the
// dialog, and once the probe selects "Yes, I trust this folder" (down-arrow
// then Enter, or the "2" shortcut) it renders the /usage screen. A bare Enter
// accepts the highlighted default "No, exit" and the session exits.
func trustingSession(t *testing.T) *fakeUsageSession {
	t.Helper()
	usage := chunked(readFixture(t, "usage_100x80.raw"), 512)
	f := newFakeSession(readFixture(t, "trust_dialog.raw"))
	pushed := false
	f.onWrite = func(f *fakeUsageSession, _ []byte) {
		_, _, written := f.state()
		down := strings.Contains(written, "\x1b[B") || strings.Contains(written, "\x1bOB")
		trusted := (down && strings.Contains(written, "\r")) || strings.Contains(written, "2")
		switch {
		case trusted && !pushed:
			pushed = true
			f.push([]byte("\x1b[2J\x1b[H"))
			f.push(usage...)
		case !trusted && strings.Contains(written, "\r"):
			f.mu.Lock()
			f.exitWhenEmpty = true
			f.mu.Unlock()
		}
	}
	return f
}

// Scenario: Trust dialog for the probe directory is accepted.
func TestProbe_TrustDialogForProbeDirIsAccepted(t *testing.T) {
	resetState(t)
	base, _ := fixtureBase(t)
	nowFunc = func() time.Time { return base }
	f := trustingSession(t)
	installFakeSession(t, f, trustFixtureDir)

	testutil.NoError(t, Probe(context.Background()))

	_, _, written := f.state()
	if written == "" {
		t.Fatal("probe wrote no keystrokes; expected it to select the trust option")
	}
	got, ok := snapshot()
	testutil.Equal(t, ok, true)
	testutil.Equal(t, got.Percentage, 51.0)
}

// Scenario: Trust dialog for another path is not accepted.
func TestProbe_TrustDialogForOtherDirAborts(t *testing.T) {
	resetState(t)
	readSlog := captureSlog(t)
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	nowFunc = func() time.Time { return now }
	previous := Reading{Percentage: 20, ResetAt: now.Add(48 * time.Hour), LastProbedAt: now.Add(-5 * time.Minute)}
	storeReading(previous)
	f := trustingSession(t)
	installFakeSession(t, f, "/Users/someone-else/.argus/usage-probe")

	testutil.NoError(t, Probe(context.Background()))

	terminated, selfExited, written := f.state()
	testutil.Equal(t, written, "")
	testutil.Equal(t, terminated, true)
	testutil.Equal(t, selfExited, false)
	got, ok := snapshot()
	testutil.Equal(t, ok, true)
	testutil.DeepEqual(t, got, previous)
	testutil.Contains(t, readSlog(), "[usagebudget] probe blocked by dialog")
}

// Scenario: Other dialog aborts the probe (the "Allow external CLAUDE.md file
// imports?" dialog seen live when probing from the argus repo).
func TestProbe_OtherDialogAbortsAndLogsFirstLine(t *testing.T) {
	resetState(t)
	readSlog := captureSlog(t)
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	nowFunc = func() time.Time { return now }
	previous := Reading{Percentage: 20, ResetAt: now.Add(48 * time.Hour), LastProbedAt: now.Add(-5 * time.Minute)}
	storeReading(previous)
	f := newFakeSession(readFixture(t, "imports_dialog.raw"))
	installFakeSession(t, f, t.TempDir())

	testutil.NoError(t, Probe(context.Background()))

	terminated, selfExited, written := f.state()
	testutil.Equal(t, written, "")
	testutil.Equal(t, terminated, true)
	testutil.Equal(t, selfExited, false)
	got, ok := snapshot()
	testutil.Equal(t, ok, true)
	testutil.DeepEqual(t, got, previous)
	log := readSlog()
	testutil.Contains(t, log, "[usagebudget] probe blocked by dialog")
	testutil.Contains(t, log, "Allow external CLAUDE.md file imports?")
}

// --- Requirement: fail open + every outcome reaches the daemon log ----------

// Scenario: Successful probe is logged (percentage and reset on the line).
func TestProbe_SuccessLineReachesDaemonLog(t *testing.T) {
	resetState(t)
	readSlog := captureSlog(t)
	base, _ := fixtureBase(t)
	nowFunc = func() time.Time { return base }
	f := newFakeSession(readFixture(t, "usage_100x80.raw"))
	installFakeSession(t, f, t.TempDir())

	testutil.NoError(t, Probe(context.Background()))

	log := readSlog()
	testutil.Contains(t, log, "[usagebudget] probe updated")
	testutil.Contains(t, log, "51")
	testutil.Contains(t, log, "2026-10-13")
}
