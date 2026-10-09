package usagebudget

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

// readUntil reads s until out contains want or the deadline passes.
func readUntil(t *testing.T, s io.Reader, want string, d time.Duration) string {
	t.Helper()
	got := make(chan string, 1)
	go func() {
		var sb strings.Builder
		buf := make([]byte, 1024)
		for {
			n, err := s.Read(buf)
			sb.Write(buf[:n])
			if strings.Contains(sb.String(), want) || err != nil {
				got <- sb.String()
				return
			}
		}
	}()
	select {
	case out := <-got:
		return out
	case <-time.After(d):
		t.Fatalf("timed out waiting for %q", want)
		return ""
	}
}

// The real PTY session path, driven with a stand-in command (never `claude`):
// it runs in the requested directory, strips CLAUDE_CONFIG_DIR, sets TERM,
// sizes the PTY tall enough, and Terminate stops it without waiting for exit.
func TestStartPTYSession_RunsInDirAndTerminates(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a PTY subprocess")
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "/should/not/leak")
	dir := t.TempDir()

	s, err := startPTYSession(context.Background(), dir, "sh", "-c",
		`printf 'cwd=%s cfg=[%s] term=%s size=%s END\n' "$(pwd -P)" "$CLAUDE_CONFIG_DIR" "$TERM" "$(stty size)"; sleep 30`)
	testutil.NoError(t, err)

	out := readUntil(t, s, "END", 5*time.Second)
	resolved, err := filepath.EvalSymlinks(dir)
	testutil.NoError(t, err)
	testutil.Contains(t, out, "cwd="+resolved)
	testutil.Contains(t, out, "cfg=[]")
	testutil.Contains(t, out, "term=xterm-256color")
	testutil.Contains(t, out, "size=80 100")

	start := time.Now()
	testutil.NoError(t, s.Terminate())
	testutil.NoError(t, s.Terminate()) // idempotent
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("Terminate took %v; SIGTERM should stop sleep promptly", el)
	}
	select {
	case <-s.waitDone:
	case <-time.After(2 * time.Second):
		t.Fatal("process not reaped after Terminate")
	}
}

// A process that ignores SIGTERM is killed after the grace period.
func TestPTYSessionTerminate_EscalatesToKill(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a PTY subprocess")
	}
	orig := terminateGrace
	terminateGrace = 100 * time.Millisecond
	t.Cleanup(func() { terminateGrace = orig })

	s, err := startPTYSession(context.Background(), t.TempDir(), "sh", "-c", `trap '' TERM; echo READY; sleep 30`)
	testutil.NoError(t, err)
	readUntil(t, s, "READY", 5*time.Second)

	testutil.NoError(t, s.Terminate())
	select {
	case <-s.waitDone:
	case <-time.After(2 * time.Second):
		t.Fatal("SIGTERM-ignoring process was not killed")
	}
}

func TestStartClaudeUsageSession_MissingDirFails(t *testing.T) {
	s, err := startClaudeUsageSession(context.Background(), filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		_ = s.Terminate()
		t.Fatal("expected an error starting in a missing directory")
	}
	if s != nil {
		t.Fatalf("session = %#v, want untyped nil on error", s)
	}
}

func TestEnsureProbeDir_ErrorWhenDataDirIsAFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	testutil.NoError(t, os.WriteFile(filepath.Join(home, ".argus"), []byte("x"), 0o600))

	if _, err := ensureProbeDir(); err == nil {
		t.Fatal("expected an error when ~/.argus is not a directory")
	}
}

func TestProbe_ProbeDirFailureLogs(t *testing.T) {
	resetState(t)
	readSlog := captureSlog(t)
	probeDirFunc = func() (string, error) { return "", errors.New("no dir") }

	testutil.NoError(t, Probe(context.Background()))

	testutil.Contains(t, readSlog(), "[usagebudget] probe failed")
}

// The parent context being canceled mid-probe is shutdown, not a timeout.
func TestProbe_ParentCancelMidProbeIsNotLoggedAsTimeout(t *testing.T) {
	resetState(t)
	readSlog := captureSlog(t)
	f := newFakeSession([]byte("Loading usage…"))
	installFakeSession(t, f, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	testutil.NoError(t, Probe(ctx))

	terminated, _, _ := f.state()
	testutil.Equal(t, terminated, true)
	if strings.Contains(readSlog(), "timed out") {
		t.Fatalf("parent cancel logged as timeout: %s", readSlog())
	}
}

// A screen still marked "Refreshing…" carries Claude Code's cached figures;
// the probe waits for the settled repaint.
func TestProbe_IgnoresRefreshingScreen(t *testing.T) {
	resetState(t)
	base, _ := fixtureBase(t)
	nowFunc = func() time.Time { return base }
	frame := func(pct, extra string) []byte {
		return []byte("\x1b[?2026h\x1b[2J\x1b[HCurrent week (all models)\r\n" + pct + "% used\r\n" +
			"Resets Oct 13 at 1am (America/Los_Angeles)\r\n" + extra + "\x1b[?2026l")
	}
	f := newFakeSession(frame("50", "Refreshing…"), frame("51", ""))
	installFakeSession(t, f, t.TempDir())

	testutil.NoError(t, Probe(context.Background()))

	got, ok := snapshot()
	testutil.Equal(t, ok, true)
	testutil.Equal(t, got.Percentage, 51.0)
}

func TestSyncUpdateOpen(t *testing.T) {
	tests := []struct {
		name   string
		prev   bool
		chunks []string
		want   bool
	}{
		{"no markers keeps prev false", false, []string{"abc"}, false},
		{"no markers keeps prev true", true, []string{"abc"}, true},
		{"begin opens", false, []string{"x\x1b[?2026hy"}, true},
		{"begin then end closes", false, []string{"\x1b[?2026hy\x1b[?2026l"}, false},
		{"end then begin opens", false, []string{"\x1b[?2026l..\x1b[?2026h"}, true},
		{"marker split across chunks", false, []string{"abc\x1b[?20", "26hdef"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var raw []byte
			state := tt.prev
			for _, c := range tt.chunks {
				raw = append(raw, c...)
				state = syncUpdateOpen(raw, len(c), state)
			}
			testutil.Equal(t, state, tt.want)
		})
	}
}

func TestDetectDialog(t *testing.T) {
	t.Run("input prompt alone is not a dialog", func(t *testing.T) {
		if d := detectDialog("❯ /usage\n\n  ⏵⏵ auto mode on\nEsc to cancel"); d != nil {
			t.Fatalf("got %#v, want nil", d)
		}
	})
	t.Run("cursor and footer without other options is not a dialog", func(t *testing.T) {
		if d := detectDialog("❯ only\nEnter to confirm · Esc to cancel"); d != nil {
			t.Fatalf("got %#v, want nil", d)
		}
	})
	t.Run("trust dialog joins wrapped path", func(t *testing.T) {
		d := detectDialog(renderUsageOutput(readFixture(t, "trust_dialog.raw")))
		if d == nil {
			t.Fatal("trust dialog not detected")
		}
		testutil.Equal(t, d.trust, true)
		testutil.Equal(t, d.workspace, trustFixtureDir)
		testutil.Equal(t, d.title, "Accessing workspace:")
	})
	t.Run("imports dialog is a non-trust dialog", func(t *testing.T) {
		d := detectDialog(renderUsageOutput(readFixture(t, "imports_dialog.raw")))
		if d == nil {
			t.Fatal("imports dialog not detected")
		}
		testutil.Equal(t, d.trust, false)
		testutil.Equal(t, d.title, "Allow external CLAUDE.md file imports?")
	})
}

func TestTrustPathMatches(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	testutil.NoError(t, os.Symlink(real, link))
	resolved, err := filepath.EvalSymlinks(real)
	testutil.NoError(t, err)

	testutil.Equal(t, trustPathMatches(resolved, link), true)
	testutil.Equal(t, trustPathMatches(resolved+"/", real), true)
	testutil.Equal(t, trustPathMatches("/elsewhere", real), false)
	testutil.Equal(t, trustPathMatches("", real), false)
	testutil.Equal(t, trustPathMatches(real, ""), false)
}

func TestParseResetTimestamp_Zones(t *testing.T) {
	base := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		zone string
		want string
	}{
		{"UTC", "UTC"},
		{"GMT", "UTC"},
		{"PDT", "America/Los_Angeles"},
		{"MST", "America/Denver"},
		{"CDT", "America/Chicago"},
		{"EST", "America/New_York"},
		{"Europe/London", "Europe/London"},
	}
	for _, tt := range tests {
		t.Run(tt.zone, func(t *testing.T) {
			got, err := parseResetTimestamp("Oct 13", "1am", tt.zone, base)
			testutil.NoError(t, err)
			testutil.Equal(t, got.Location().String(), tt.want)
			testutil.Equal(t, got.Hour(), 1)
		})
	}
	t.Run("unknown zone falls back to local", func(t *testing.T) {
		testutil.Equal(t, locationForZone("Nowhere/Fake"), time.Local)
		testutil.Equal(t, locationForZone("XYZ"), time.Local)
	})
	t.Run("unparseable date errors", func(t *testing.T) {
		if _, err := parseResetTimestamp("Smarch 99", "1am", "UTC", base); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestParseUsageOutput_Errors(t *testing.T) {
	base := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	for name, out := range map[string]string{
		"no weekly header": "Current session\n62% used",
		"no reset line":    "Current week (all models)\n51% used\nUsage credits\nResets Oct 13 at 1am (UTC)",
		"bad reset date":   "Current week (all models)\n51% used\nResets Smarch 99 at 1am (UTC)",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseUsageOutput(out, base); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestCachedClaudePct(t *testing.T) {
	resetState(t)
	_, ok := CachedClaudePct()
	testutil.Equal(t, ok, false)

	now := time.Now()
	storeReading(Reading{Percentage: 12, LastProbedAt: now})
	pct, ok := CachedClaudePct()
	testutil.Equal(t, ok, true)
	testutil.Equal(t, pct, 12.0)
}

// A trust dialog that goes away on its own before settling is never answered.
func TestProbe_TrustDialogThatDisappearsIsNotAnswered(t *testing.T) {
	resetState(t)
	readSlog := captureSlog(t)
	orig := probeTimeout
	probeTimeout = 300 * time.Millisecond
	t.Cleanup(func() { probeTimeout = orig })
	trustSettle = 100 * time.Millisecond
	f := newFakeSession(readFixture(t, "trust_dialog.raw"), []byte("\x1b[2J\x1b[HLoading usage…"))
	installFakeSession(t, f, trustFixtureDir)

	testutil.NoError(t, Probe(context.Background()))

	_, _, written := f.state()
	testutil.Equal(t, written, "")
	testutil.Contains(t, readSlog(), "[usagebudget] probe timed out")
}

// startOrphaningSession starts a stand-in session whose leader backgrounds a
// child that ignores SIGTERM and SIGHUP (like a stubborn MCP server or hook),
// writes the child's pid to a file, and then runs leaderTail. It returns the
// session and the child's pid.
func startOrphaningSession(t *testing.T, leaderTail string) (*ptyUsageSession, int) {
	t.Helper()
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	script := `(trap '' TERM HUP; exec sleep 30) & echo $! > ` + pidFile + `; echo READY; ` + leaderTail
	s, err := startPTYSession(context.Background(), dir, "sh", "-c", script)
	testutil.NoError(t, err)
	readUntil(t, s, "READY", 5*time.Second)
	b, err := os.ReadFile(pidFile)
	testutil.NoError(t, err)
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	return s, pid
}

// processGone polls until pid no longer exists (or is a zombie awaiting its
// reaper), up to d.
func processGone(pid int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return true
		}
		out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
		if err != nil || strings.HasPrefix(strings.TrimSpace(string(out)), "Z") {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// Terminate kills the whole process group after the leader exits, so a
// child ignoring SIGTERM (an MCP server, a hook) never outlives the probe.
func TestPTYSessionTerminate_KillsGroupChildIgnoringTerm(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a PTY subprocess")
	}
	s, child := startOrphaningSession(t, "wait")

	testutil.NoError(t, s.Terminate())

	if !processGone(child, 3*time.Second) {
		t.Fatalf("child %d ignoring SIGTERM survived Terminate", child)
	}
}

// A session that already exited on its own is not group-signalled: its pgid
// may since have been reused, so Terminate only closes the PTY.
func TestPTYSessionTerminate_SkipsGroupSignalsAfterSelfExit(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a PTY subprocess")
	}
	s, child := startOrphaningSession(t, "exit 0")
	select {
	case <-s.waitDone:
	case <-time.After(5 * time.Second):
		t.Fatal("leader did not exit on its own")
	}

	testutil.NoError(t, s.Terminate())

	if err := syscall.Kill(child, 0); err != nil {
		t.Fatalf("child was signalled after the session had already exited: %v", err)
	}
}
