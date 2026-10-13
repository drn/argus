package hostsleep

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

const customOut = `Battery Power:
 sleep                1
 displaysleep         2
AC Power:
 Sleep On Power Button 1
 disksleep            10
 sleep                1 (sleep prevented by caffeinate)
 displaysleep         10
`

const assertOut = `Listed by owning process:
   pid 61135(caffeinate): [0x1] 00:00:01 PreventSystemSleep named: "caffeinate command-line tool"
	Details: caffeinate asserting on behalf of Process ID 1335
   pid 61113(caffeinate): [0x2] 00:00:03 PreventUserIdleSystemSleep named: "caffeinate command-line tool"
	Details: caffeinate asserting for 300 secs
`

func TestParseACSleep(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
		ok   bool
	}{
		{"ac section wins over battery", customOut, 1, true},
		{"never", "AC Power:\n sleep                0\n", 0, true},
		{"ignores disksleep and displaysleep", "AC Power:\n disksleep 10\n displaysleep 10\n", -1, false},
		{"no ac section", "Battery Power:\n sleep 1\n", -1, false},
		{"garbage", "", -1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseACSleep(tt.in)
			testutil.Equal(t, got, tt.want)
			testutil.Equal(t, ok, tt.ok)
		})
	}
}

func TestParseHeldBy(t *testing.T) {
	testutil.True(t, ParseHeldBy(assertOut, 1335))
	testutil.False(t, ParseHeldBy(assertOut, 1336))
	testutil.False(t, ParseHeldBy(assertOut, 0))
	testutil.False(t, ParseHeldBy("", 1335))
}

func TestDiagnose(t *testing.T) {
	tests := []struct {
		name string
		in   Inputs
		want Status
	}{
		{"api off is never a warning", Inputs{APIEnabled: false, Supported: true, ACSleepMin: 1}, NotApplicable},
		{"non-darwin", Inputs{APIEnabled: true, Supported: false, ACSleepMin: 1}, NotApplicable},
		{"gather error", Inputs{APIEnabled: true, Supported: true, GatherErr: errors.New("x")}, Unknown},
		{"unparsed sleep", Inputs{APIEnabled: true, Supported: true, ACSleepMin: -1}, Unknown},
		{"sleep disabled", Inputs{APIEnabled: true, Supported: true, ACSleepMin: 0}, Safe},
		{"held", Inputs{APIEnabled: true, Supported: true, ACSleepMin: 1, HeldByDaemon: true}, Held},
		{"at risk", Inputs{APIEnabled: true, Supported: true, ACSleepMin: 1}, AtRisk},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Diagnose(tt.in)
			testutil.Equal(t, r.Status, tt.want)
			testutil.Equal(t, r.Warn(), tt.want == AtRisk)
			if tt.want == AtRisk {
				testutil.Equal(t, r.Fix, PermanentFix)
				testutil.True(t, r.Message != "")
			}
		})
	}
}

func TestCheck(t *testing.T) {
	oldRun, oldOS := runPmset, goos
	t.Cleanup(func() { runPmset, goos = oldRun, oldOS })
	goos = "darwin"

	t.Run("api off skips pmset", func(t *testing.T) {
		runPmset = func(context.Context, ...string) (string, error) { t.Fatal("pmset ran"); return "", nil }
		testutil.Equal(t, Check(false, 1).Status, NotApplicable)
	})
	t.Run("at risk then held", func(t *testing.T) {
		runPmset = func(_ context.Context, args ...string) (string, error) {
			if args[1] == "custom" {
				return customOut, nil
			}
			return assertOut, nil
		}
		testutil.Equal(t, Check(true, 999).Status, AtRisk)
		testutil.Equal(t, Check(true, 1335).Status, Held)
	})
	t.Run("sleep zero is safe without reading assertions", func(t *testing.T) {
		runPmset = func(_ context.Context, args ...string) (string, error) {
			if args[1] == "assertions" {
				t.Fatal("assertions read")
			}
			return "AC Power:\n sleep 0\n", nil
		}
		testutil.Equal(t, Check(true, 1).Status, Safe)
	})
	t.Run("pmset failure is unknown", func(t *testing.T) {
		runPmset = func(context.Context, ...string) (string, error) { return "", errors.New("boom") }
		testutil.Equal(t, Check(true, 1).Status, Unknown)
	})
	t.Run("unparseable output is unknown", func(t *testing.T) {
		runPmset = func(context.Context, ...string) (string, error) { return "nonsense", nil }
		testutil.Equal(t, Check(true, 1).Status, Unknown)
	})
	t.Run("assertion read failure degrades to at risk", func(t *testing.T) {
		runPmset = func(_ context.Context, args ...string) (string, error) {
			if args[1] == "custom" {
				return customOut, nil
			}
			return "", errors.New("boom")
		}
		testutil.Equal(t, Check(true, 1335).Status, AtRisk)
	})
	t.Run("non-darwin", func(t *testing.T) {
		goos = "linux"
		defer func() { goos = "darwin" }()
		testutil.Equal(t, Check(true, 1).Status, NotApplicable)
	})
}

func TestDaemonPID(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.pid")
	testutil.NoError(t, os.WriteFile(good, []byte("1335\n"), 0o644))
	bad := filepath.Join(dir, "bad.pid")
	testutil.NoError(t, os.WriteFile(bad, []byte("nope"), 0o644))
	testutil.Equal(t, DaemonPID(good), 1335)
	testutil.Equal(t, DaemonPID(bad), 0)
	testutil.Equal(t, DaemonPID(filepath.Join(dir, "missing")), 0)
}

func TestMonitor(t *testing.T) {
	oldOS := goos
	t.Cleanup(func() { goos = oldOS })
	goos = "darwin"

	calls := make(chan struct{}, 8)
	m := NewMonitor()
	m.check = func(bool, int) Report { calls <- struct{}{}; return Report{Status: AtRisk} }

	testutil.Equal(t, m.Get(false, 1).Status, NotApplicable)
	testutil.Equal(t, len(calls), 0)

	// First call returns the placeholder and kicks one background check.
	testutil.Equal(t, m.Get(true, 1).Status, NotApplicable)
	select {
	case <-calls:
	case <-time.After(2 * time.Second):
		t.Fatal("background check never ran")
	}
	deadline := time.Now().Add(2 * time.Second)
	for m.Get(true, 1).Status != AtRisk && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	testutil.Equal(t, m.Get(true, 1).Status, AtRisk)

	// A different pid discards the cached verdict instead of serving it.
	testutil.Equal(t, m.Get(true, 2).Status, NotApplicable)
}

func TestMonitor_OnUpdateFiresAfterCheck(t *testing.T) {
	m := NewMonitorForTest(func(bool, int) Report { return Report{Status: AtRisk} })
	done := make(chan struct{})
	m.OnUpdate = func() { close(done) }
	m.Get(true, 1)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("OnUpdate not called")
	}
}
