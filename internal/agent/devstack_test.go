package agent

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

func TestExtractWorktreePath(t *testing.T) {
	tests := []struct {
		name    string
		cmdline string
		want    string
	}{
		{
			name:    "argus worktree with nested devbox file",
			cmdline: "/x/bin/process-compose -p 63050 -f /Users/aaron/.argus/worktrees/Sherlock/6b-fanout-mech/.devbox/virtenv/redis/process-compose.yaml",
			want:    "/Users/aaron/.argus/worktrees/Sherlock/6b-fanout-mech",
		},
		{
			name:    "legacy claude worktree location",
			cmdline: "mysqld --datadir=/Users/aaron/.claude/worktrees/proj/task/.devbox/virtenv/mysql80/data",
			want:    "/Users/aaron/.claude/worktrees/proj/task",
		},
		{
			name:    "no worktree path present",
			cmdline: "redis-server --port 6379",
			want:    "",
		},
		{
			// redis-server's real command line glues its socket flag
			// directly onto the path with a colon and no space at all —
			// caught via a live `argus doctor` smoke test, which without
			// this fix reported every redis-server orphan's worktree path
			// as "unixsocket:/Users/..." instead of "/Users/...".
			name:    "redis-server unixsocket colon-glued prefix is excluded",
			cmdline: "redis-server unixsocket:/Users/aaron/.argus/worktrees/proj/task/.devbox/virtenv/redis/redis.sock",
			want:    "/Users/aaron/.argus/worktrees/proj/task",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testutil.Equal(t, extractWorktreePath(tt.cmdline), tt.want)
		})
	}
}

func TestExtractWorktreePath_PrefixSiblingNotConflated(t *testing.T) {
	a := extractWorktreePath("process-compose -f /Users/aaron/.argus/worktrees/Sherlock/3b/.devbox/virtenv/redis/process-compose.yaml")
	b := extractWorktreePath("process-compose -f /Users/aaron/.argus/worktrees/Sherlock/3b-more/.devbox/virtenv/redis/process-compose.yaml")
	testutil.Equal(t, a, "/Users/aaron/.argus/worktrees/Sherlock/3b")
	testutil.Equal(t, b, "/Users/aaron/.argus/worktrees/Sherlock/3b-more")
	if a == b {
		t.Fatalf("sibling worktree paths must not extract equal, got %q == %q", a, b)
	}
}

func TestProcessNameFromCmdline(t *testing.T) {
	tests := []struct {
		cmdline string
		want    string
	}{
		{"/x/bin/process-compose -p 1 -f y.yaml", "process-compose"},
		{"/x/bin/mysqld --datadir=/y", "mysqld"},
		{"redis-server unixsocket:/y/redis.sock", "redis-server"},
		{"/x/bin/postgres -D /y -p 5432", "postgres"},
		{"caddy run --config Caddyfile", "caddy"},
		{"unrelated-process --foo", ""},
	}
	for _, tt := range tests {
		testutil.Equal(t, processNameFromCmdline(tt.cmdline), tt.want)
	}
}

func TestParsePgrepOutput(t *testing.T) {
	out := "111 /x/bin/process-compose -p 1 -f /Users/aaron/.argus/worktrees/proj/task/.devbox/virtenv/redis/process-compose.yaml\n" +
		"222 mysqld --datadir=/Users/aaron/.argus/worktrees/proj/task/.devbox/virtenv/mysql80/data\n" +
		"333 some-unrelated-process --flag\n" +
		"\n"
	procs, err := parsePgrepOutput([]byte(out))
	testutil.NoError(t, err)
	testutil.Equal(t, len(procs), 2)
	testutil.Equal(t, procs[0].PID, 111)
	testutil.Equal(t, procs[0].Name, "process-compose")
	testutil.Equal(t, procs[0].WorktreePath, "/Users/aaron/.argus/worktrees/proj/task")
	testutil.Equal(t, procs[1].PID, 222)
	testutil.Equal(t, procs[1].Name, "mysqld")
}

func TestScanDevStackProcesses_NoMatches(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh -c not available on windows")
	}
	prev := pgrepOutput
	t.Cleanup(func() { pgrepOutput = prev })
	pgrepOutput = func() ([]byte, error) {
		// The standard `false` utility always exits 1 with no output and no
		// shell involved, mirroring pgrep's own "no processes matched" exit
		// code — produces a genuine *exec.ExitError rather than a
		// hand-fabricated one.
		return exec.Command("false").Output()
	}

	procs, err := ScanDevStackProcesses()
	testutil.NoError(t, err)
	testutil.Equal(t, len(procs), 0)
}

func TestScanDevStackProcesses_ScanUnavailable(t *testing.T) {
	prev := pgrepOutput
	t.Cleanup(func() { pgrepOutput = prev })
	wantErr := errors.New(`exec: "pgrep": executable file not found in $PATH`)
	pgrepOutput = func() ([]byte, error) {
		return nil, wantErr
	}

	_, err := ScanDevStackProcesses()
	testutil.ErrorIs(t, err, wantErr)
}

func TestScanDevStackProcesses_ParsesFakeOutput(t *testing.T) {
	prev := pgrepOutput
	t.Cleanup(func() { pgrepOutput = prev })
	pgrepOutput = func() ([]byte, error) {
		return []byte("444 caddy run --config /Users/aaron/.argus/worktrees/proj/task/Caddyfile\n"), nil
	}

	procs, err := ScanDevStackProcesses()
	testutil.NoError(t, err)
	testutil.Equal(t, len(procs), 1)
	testutil.Equal(t, procs[0].Name, "caddy")
}

// TestStopDevStackFor_RealProcess exercises the real pgrep + real signal
// path end-to-end against a genuine subprocess this test owns (never an
// arbitrary/pre-existing PID), so it's safe to run against the real OS
// process table.
func TestStopDevStackFor_RealProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pgrep/POSIX signals unavailable on windows")
	}
	if _, err := exec.LookPath("pgrep"); err != nil {
		t.Skip("pgrep not on PATH")
	}

	origGrace := devStackGracePeriod
	devStackGracePeriod = 300 * time.Millisecond
	t.Cleanup(func() { devStackGracePeriod = origGrace })

	// This test owns a real subprocess, so it uses the real argv re-read. Skip
	// where ps is unusable (e.g. an agent sandbox) rather than fail.
	if _, err := realPsCommand(os.Getpid()); err != nil {
		t.Skipf("ps unavailable: %v", err)
	}
	prevPs := psCommand
	t.Cleanup(func() { psCommand = prevPs })
	psCommand = realPsCommand

	// The fake worktree root plus a script literally named "mysqld" living
	// directly inside it: BSD sleep(1) rejects extra positional args (which
	// rules out passing a marker+path as trailing sleep args), so instead
	// the executable's own path carries both substrings the real production
	// command lines have — a known dev-stack name and the worktree root —
	// with no extra argv needed. The script loops forever without `exec`ing
	// into something else, so its own invocation path (embedding both
	// substrings) stays visible in ps/pgrep for the life of the process.
	wt := t.TempDir() + "/.argus/worktrees/faketest/faketask"
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatalf("mkdir fake worktree: %v", err)
	}
	// A symlink to the sleep binary named "mysqld": argv[0] is the dev-stack
	// binary and the worktree root is in its path, matching the executable-
	// based classification (a #! script would show as /bin/sh <script>).
	scriptPath := wt + "/mysqld"
	sleepBin, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep not on PATH")
	}
	// A symlink, not a copy: a copied platform binary can be killed at exec
	// by code-signing enforcement, while argv[0] stays the symlink path.
	if err := os.Symlink(sleepBin, scriptPath); err != nil {
		t.Fatalf("symlink fake mysqld: %v", err)
	}

	cmd := exec.Command(scriptPath, "1000")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fake dev-stack process: %v", err)
	}
	pid := cmd.Process.Pid
	// Reap in the background as soon as the process exits — otherwise it
	// stays a zombie (PID slot still allocated) until something calls
	// Wait(), which would make a raw kill(pid, 0) liveness check succeed
	// even after stopDevStackFor has genuinely killed it.
	go func() { _ = cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	waitUntilVisible(t, pid, wt)

	stopDevStackFor(wt)

	if !waitUntilGone(pid, 3*time.Second) {
		t.Fatalf("pid %d still running after stopDevStackFor", pid)
	}
}

func waitUntilVisible(t *testing.T, pid int, worktreePath string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		procs, err := ScanDevStackProcesses()
		if err == nil {
			for _, p := range procs {
				if p.PID == pid && p.WorktreePath == worktreePath {
					return
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pid %d never became visible to ScanDevStackProcesses for worktree %q", pid, worktreePath)
}

func waitUntilGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func TestClassifyDevStackCmdline(t *testing.T) {
	const wt = "/Users/aaron/.argus/worktrees/Sketch/t1"
	tests := []struct {
		name       string
		cmdline    string
		wantName   string
		wantSuperv bool
	}{
		{"oct30 devbox wrapper", "devbox run -- mysqld --datadir=" + wt + "/tmp/mysql-data", "mysqld", true},
		{"devbox wrapper abs path", "/usr/local/bin/devbox run -- /nix/store/x-mysql/bin/mysqld --datadir=" + wt + "/tmp/mysql-data", "mysqld", true},
		{"oct30 launchd-parented mysqld", "/nix/store/x-mysql/bin/mysqld --datadir=" + wt + "/tmp/mysql-data", "mysqld", false},
		{"nix wrapped binary", "/nix/store/x/bin/.mysqld-wrapped --datadir=" + wt + "/d", "mysqld", false},
		{"process-compose is a supervisor", "/x/bin/process-compose -f " + wt + "/.devbox/virtenv/redis/process-compose.yaml", "process-compose", true},
		{"devbox without run --", "devbox services up " + wt, "", false},
		{"devbox run of unrelated binary", "devbox run -- go test " + wt + " mysqld", "", false},
		{"agent prompt mentions binary and path", "claude --add-dir " + wt + " fix the postgres and mysqld startup", "", false},
		{"empty", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, sup := classifyDevStackCmdline(tt.cmdline)
			testutil.Equal(t, n, tt.wantName)
			testutil.Equal(t, sup, tt.wantSuperv)
		})
	}
}

func TestParsePgrepOutput_Oct30Shapes(t *testing.T) {
	const wt = "/Users/aaron/.argus/worktrees/Sketch/t1"
	out := "10 devbox run -- mysqld --datadir=" + wt + "/tmp/mysql-data\n" +
		"11 /nix/store/x-mysql/bin/mysqld --datadir=" + wt + "/tmp/mysql-data\n" +
		"12 claude --add-dir " + wt + " restart postgres\n"
	procs, err := parsePgrepOutput([]byte(out))
	testutil.NoError(t, err)
	testutil.Equal(t, len(procs), 2)
	testutil.Equal(t, procs[0].PID, 10)
	testutil.True(t, procs[0].Supervisor)
	testutil.Equal(t, procs[1].PID, 11)
	testutil.Equal(t, procs[1].WorktreePath, wt)
}

func TestClassifyDevStackCmdline_ReviewShapes(t *testing.T) {
	const wt = "/Users/aaron/.argus/worktrees/Sketch/t1"
	tests := []struct {
		name       string
		cmdline    string
		wantName   string
		wantSuperv bool
	}{
		{"devbox run without --", "devbox run mysqld --datadir=" + wt + "/d", "mysqld", true},
		{"devbox run with env wrapper", "devbox run -- env A=1 mysqld --datadir=" + wt + "/d", "mysqld", true},
		{"devbox run with abs env and two assignments", "/usr/bin/devbox run -- /usr/bin/env A=1 B=2 mysqld --datadir=" + wt, "mysqld", true},
		{"devbox run with a flag before the command", "devbox run -q mysqld --datadir=" + wt, "mysqld", true},
		{"mysqld_safe is a supervisor", "/nix/store/x/bin/mysqld_safe --datadir=" + wt + "/d", "mysqld_safe", true},
		{"devbox run mysqld_safe", "devbox run -- mysqld_safe --datadir=" + wt + "/d", "mysqld_safe", true},
		{"sh -c wrapper is not matched (its child is)", "sh -c mysqld --datadir=" + wt, "", false},
		{"devbox shell", "devbox shell mysqld " + wt, "", false},
		{"devbox run env of unrelated", "devbox run -- env A=1 make test " + wt + " mysqld", "", false},
		{"devbox run with no command", "devbox run --", "", false},
		{"devbox bare", "devbox", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, sup := classifyDevStackCmdline(tt.cmdline)
			testutil.Equal(t, n, tt.wantName)
			testutil.Equal(t, sup, tt.wantSuperv)
		})
	}
}

func TestParsePgrepOutput_LongLinesAndErrors(t *testing.T) {
	const wt = "/Users/aaron/.argus/worktrees/p/t"
	// A matching process whose command line is well over bufio's 64KB default
	// must not hide the processes after it.
	long := "100 mysqld --datadir=" + wt + "/d --opt=" + strings.Repeat("x", 200*1024) + "\n"
	after := "200 redis-server unixsocket:" + wt + "/r.sock\n"
	procs, err := parsePgrepOutput([]byte(long + after))
	testutil.NoError(t, err)
	testutil.Equal(t, len(procs), 2)
	testutil.Equal(t, procs[1].PID, 200)

	// Beyond the hard cap it is a scan FAILURE, not a silently short list.
	huge := "300 mysqld --datadir=" + wt + "/d " + strings.Repeat("y", maxPgrepLine+10) + "\n"
	procs, err = parsePgrepOutput([]byte(huge + after))
	if err == nil {
		t.Fatalf("over-long line must be an error, got %d procs", len(procs))
	}
	testutil.Equal(t, len(procs), 0)

	prev := pgrepOutput
	t.Cleanup(func() { pgrepOutput = prev })
	pgrepOutput = func() ([]byte, error) { return []byte(huge), nil }
	if _, err := ScanDevStackProcesses(); err == nil {
		t.Fatal("ScanDevStackProcesses must surface the parse error")
	}
}

func TestParsePgrepOutput_MultilinePromptYieldsPhantom(t *testing.T) {
	// Documents WHY every PID is re-verified: a multi-line prompt quoting a
	// dev-stack command line becomes a candidate entry on its own line.
	const wt = "/Users/aaron/.argus/worktrees/p/t"
	out := "7 claude --prompt first line\n48213 /nix/store/x/bin/mysqld --datadir=" + wt + "/d\nlast line\n"
	procs, err := parsePgrepOutput([]byte(out))
	testutil.NoError(t, err)
	testutil.Equal(t, len(procs), 1)
	testutil.Equal(t, procs[0].PID, 48213)
}

// stopHarness stubs every seam of the stop path and records, in order, every
// signal and every sleep.
type stopHarness struct {
	t       *testing.T
	scans   [][]byte
	scanN   int
	events  []string
	ps      map[int]string // live argv per pid; absent = gone
	psErr   error
	sigErr  error
	sleeps  []time.Duration
	cancels int
}

func newStopHarness(t *testing.T, scans ...string) *stopHarness {
	t.Helper()
	h := &stopHarness{t: t, ps: map[int]string{}}
	for _, sc := range scans {
		h.scans = append(h.scans, []byte(sc))
		// Default: each scanned line's pid is live with exactly that argv.
		for _, line := range strings.Split(sc, "\n") {
			if f := strings.SplitN(strings.TrimSpace(line), " ", 2); len(f) == 2 {
				if pid, err := strconv.Atoi(f[0]); err == nil {
					h.ps[pid] = f[1]
				}
			}
		}
	}
	prevP, prevS, prevPs, prevSl := pgrepOutput, signalPID, psCommand, devStackSleep
	t.Cleanup(func() { pgrepOutput, signalPID, psCommand, devStackSleep = prevP, prevS, prevPs, prevSl })
	pgrepOutput = func() ([]byte, error) {
		b := h.scans[min(h.scanN, len(h.scans)-1)]
		h.scanN++
		return b, nil
	}
	psCommand = func(pid int) (string, error) {
		if h.psErr != nil {
			return "", h.psErr
		}
		return h.ps[pid], nil
	}
	signalPID = func(pid int, sig syscall.Signal) error {
		h.events = append(h.events, fmt.Sprintf("%v:%d", sig, pid))
		return h.sigErr
	}
	devStackSleep = func(d time.Duration, cancel <-chan struct{}) bool {
		h.sleeps = append(h.sleeps, d)
		h.events = append(h.events, "sleep")
		select {
		case <-cancel:
			h.cancels++
			return false
		default:
			return true
		}
	}
	return h
}

// signals returns only the recorded signal events (no sleeps).
func (h *stopHarness) signals() []string {
	var out []string
	for _, e := range h.events {
		if e != "sleep" {
			out = append(out, e)
		}
	}
	return out
}

const stopWT = "/Users/aaron/.argus/worktrees/proj/task"

func pcLine(pid int) string {
	return fmt.Sprintf("%d process-compose -f %s/.devbox/virtenv/redis/process-compose.yaml\n", pid, stopWT)
}
func myLine(pid int) string { return fmt.Sprintf("%d mysqld --datadir=%s/d\n", pid, stopWT) }

func TestStopDevStackFor_SignalsOnlyExactMatch(t *testing.T) {
	const sibling = "222 process-compose -f /Users/aaron/.argus/worktrees/Sherlock/3b-more/.devbox/virtenv/redis/process-compose.yaml\n"
	const target = "111 process-compose -f /Users/aaron/.argus/worktrees/Sherlock/3b/.devbox/virtenv/redis/process-compose.yaml\n"
	h := newStopHarness(t, target+sibling, sibling)
	stopDevStackFor("/Users/aaron/.argus/worktrees/Sherlock/3b")
	testutil.DeepEqual(t, h.events, []string{"terminated:111", "sleep"})
}

func TestStopDevStackFor_NoMatchesIsNoop(t *testing.T) {
	h := newStopHarness(t, "")
	stopDevStackFor("/Users/aaron/.argus/worktrees/nope/nope")
	testutil.Equal(t, len(h.events), 0)
}

func TestStopDevStackFor_SigkillsSurvivors(t *testing.T) {
	h := newStopHarness(t, myLine(555))
	stopDevStackFor(stopWT)
	testutil.DeepEqual(t, h.events, []string{"terminated:555", "sleep", "killed:555"})
}

func TestStopDevStackFor_SupervisorGraceSleepsBetweenSupervisorAndChildren(t *testing.T) {
	// The supervisor-grace sleep is what gives a supervisor time to shut down
	// before the direct round: deleting it must change this sequence.
	h := newStopHarness(t, myLine(200)+pcLine(100), myLine(200), "")
	stopDevStackFor(stopWT)
	testutil.DeepEqual(t, h.events, []string{"terminated:100", "sleep", "terminated:200", "sleep"})
	testutil.Equal(t, h.sleeps[0], devStackSupervisorGrace)
	testutil.Equal(t, h.sleeps[1], devStackGracePeriod)
}

func TestStopDevStackFor_SupervisorsKilledBeforeChildren(t *testing.T) {
	h := newStopHarness(t, myLine(200)+pcLine(100)) // child listed first; all survive
	stopDevStackFor(stopWT)
	testutil.DeepEqual(t, h.events, []string{
		"terminated:100", "sleep",
		"terminated:200", "terminated:100", "sleep",
		"killed:100", "killed:200",
	})
}

func TestStopDevStackFor_MysqldSafeIsASupervisor(t *testing.T) {
	safe := fmt.Sprintf("100 /nix/store/x/bin/mysqld_safe --datadir=%s/d\n", stopWT)
	h := newStopHarness(t, myLine(200)+safe, myLine(200), "")
	stopDevStackFor(stopWT)
	testutil.DeepEqual(t, h.events, []string{"terminated:100", "sleep", "terminated:200", "sleep"})
}

func TestStopDevStackFor_RemovalWrappersNotRegressed(t *testing.T) {
	for name, line := range map[string]string{
		"devbox run --":           fmt.Sprintf("10 devbox run -- mysqld --datadir=%s/d\n", stopWT),
		"devbox run":              fmt.Sprintf("10 devbox run mysqld --datadir=%s/d\n", stopWT),
		"devbox run -- env A=1":   fmt.Sprintf("10 devbox run -- env A=1 mysqld --datadir=%s/d\n", stopWT),
		"launchd-parented mysqld": myLine(10),
	} {
		t.Run(name, func(t *testing.T) {
			h := newStopHarness(t, line, "")
			stopDevStackFor(stopWT)
			testutil.DeepEqual(t, h.events[:1], []string{"terminated:10"})
		})
	}
}

func TestStopDevStackFor_MultilinePromptPhantomNeverSignaled(t *testing.T) {
	// pgrep prints 48213 on its own line from inside a multi-line prompt of
	// pid 7 (an agent). 48213's own live argv is something unrelated.
	scan := "7 claude --prompt first line\n48213 /nix/store/x/bin/mysqld --datadir=" + stopWT + "/d\nlast line\n"
	h := newStopHarness(t, scan)
	h.ps[48213] = "/usr/bin/vim notes.txt" // the phantom pid is really something else
	stopDevStackFor(stopWT)
	testutil.Equal(t, len(h.signals()), 0)
}

func TestVerifyDevStackProc(t *testing.T) {
	const wt = stopWT
	good := DevStackProc{PID: 5, Name: "mysqld", WorktreePath: wt}
	tests := []struct {
		name string
		ps   string
		err  error
		ok   bool
	}{
		{"matches", "mysqld --datadir=" + wt + "/d", nil, true},
		{"gone", "", nil, false},
		{"ps error", "", errors.New("ps blew up"), false},
		{"now another binary", "redis-server unixsocket:" + wt + "/r.sock", nil, false},
		{"not a dev stack at all", "vim " + wt + "/mysqld.txt", nil, false},
		{"reused pid in another worktree", "mysqld --datadir=/Users/aaron/.argus/worktrees/other/wt/d", nil, false},
		{"multi-line argv of an agent", "claude --prompt line1\nmysqld --datadir=" + wt + "/d", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prev := psCommand
			t.Cleanup(func() { psCommand = prev })
			psCommand = func(int) (string, error) { return tt.ps, tt.err }
			ok, why := verifyDevStackProc(good)
			testutil.Equal(t, ok, tt.ok)
			if !ok && why == "" {
				t.Fatal("a refusal must say why")
			}
		})
	}
}

func TestStopDevStack_LongKillGraceAndAbort(t *testing.T) {
	// The sweeper passes a 30s kill grace and a Proceed re-check.
	h := newStopHarness(t, myLine(555))
	proceeds := 0
	StopDevStack(stopWT, StopOptions{KillGrace: 30 * time.Second, Proceed: func() bool { proceeds++; return proceeds < 2 }})
	// Proceed true before the SIGTERM round, false before SIGKILL: no kill.
	testutil.DeepEqual(t, h.events, []string{"terminated:555", "sleep"})
	testutil.Equal(t, h.sleeps[0], 30*time.Second)
	testutil.Equal(t, proceeds, 2)

	// Abandoned before anything is signaled.
	h2 := newStopHarness(t, myLine(556))
	StopDevStack(stopWT, StopOptions{Proceed: func() bool { return false }})
	testutil.Equal(t, len(h2.events), 0)
}

func TestStopDevStack_CancelDuringGraceSendsNoKill(t *testing.T) {
	h := newStopHarness(t, myLine(555))
	cancel := make(chan struct{})
	close(cancel)
	StopDevStack(stopWT, StopOptions{KillGrace: 30 * time.Second, Cancel: cancel})
	testutil.DeepEqual(t, h.events, []string{"terminated:555", "sleep"})
	testutil.Equal(t, h.cancels, 1)

	// Cancelled in the supervisor grace: nothing beyond the supervisor SIGTERM.
	h2 := newStopHarness(t, myLine(200)+pcLine(100))
	StopDevStack(stopWT, StopOptions{Cancel: cancel})
	testutil.DeepEqual(t, h2.events, []string{"terminated:100", "sleep"})
}

func TestStopDevStack_LogsPIDSignalAndFailuresViaSlog(t *testing.T) {
	var buf bytes.Buffer
	prevLog := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prevLog) })
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))

	h := newStopHarness(t, myLine(555))
	h.sigErr = errors.New("operation not permitted")
	stopDevStackFor(stopWT)
	out := buf.String()
	testutil.Contains(t, out, "pid 555")
	testutil.Contains(t, out, "terminated")
	testutil.Contains(t, out, "operation not permitted")

	// A refused (unverifiable) signal is logged with the reason too.
	buf.Reset()
	h2 := newStopHarness(t, myLine(556))
	h2.psErr = errors.New("ps denied")
	stopDevStackFor(stopWT)
	testutil.Contains(t, buf.String(), "NOT signaling")
	testutil.Contains(t, buf.String(), "ps denied")
	testutil.Equal(t, len(h2.signals()), 0)

	// A rescan failure is logged via slog too.
	buf.Reset()
	newStopHarness(t, myLine(557))
	calls := 0
	pgrepOutput = func() ([]byte, error) {
		calls++
		if calls == 1 {
			return []byte(myLine(557)), nil
		}
		return nil, errors.New("pgrep vanished")
	}
	stopDevStackFor(stopWT)
	testutil.Contains(t, buf.String(), "scan unavailable")
	testutil.Contains(t, buf.String(), "pgrep vanished")
}

func TestStopDevStack_ExportedWrapperNoopWhenNothingRuns(t *testing.T) {
	h := newStopHarness(t, "")
	StopDevStack(stopWT, StopOptions{})
	testutil.Equal(t, len(h.events), 0)
}
