package agent

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"sync"
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
	procs := parsePgrepOutput([]byte(out))
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

func TestStopDevStackFor_SignalsOnlyExactMatch(t *testing.T) {
	origGrace := devStackGracePeriod
	devStackGracePeriod = time.Millisecond
	t.Cleanup(func() { devStackGracePeriod = origGrace })

	prevPgrep := pgrepOutput
	t.Cleanup(func() { pgrepOutput = prevPgrep })
	const siblingLine = "222 process-compose -f /Users/aaron/.argus/worktrees/Sherlock/3b-more/.devbox/virtenv/redis/process-compose.yaml\n"
	const targetLine = "111 process-compose -f /Users/aaron/.argus/worktrees/Sherlock/3b/.devbox/virtenv/redis/process-compose.yaml\n"
	callCount := 0
	pgrepOutput = func() ([]byte, error) {
		callCount++
		if callCount == 1 {
			// Initial scan: both the target worktree's process and an
			// unrelated sibling worktree's process (name is a string-prefix
			// of the target's) are running.
			return []byte(targetLine + siblingLine), nil
		}
		// Post-grace-period verification scan: pid 111 died from SIGTERM;
		// the sibling (never signaled) is still running.
		return []byte(siblingLine), nil
	}

	var mu sync.Mutex
	var signaled []int
	prevSignal := signalPID
	t.Cleanup(func() { signalPID = prevSignal })
	signalPID = func(pid int, sig syscall.Signal) error {
		mu.Lock()
		signaled = append(signaled, pid)
		mu.Unlock()
		return nil
	}

	stopDevStackFor("/Users/aaron/.argus/worktrees/Sherlock/3b")

	mu.Lock()
	defer mu.Unlock()
	testutil.DeepEqual(t, signaled, []int{111})
}

func TestStopDevStackFor_NoMatchesIsNoop(t *testing.T) {
	prevPgrep := pgrepOutput
	t.Cleanup(func() { pgrepOutput = prevPgrep })
	pgrepOutput = func() ([]byte, error) { return nil, nil }

	var calls int
	prevSignal := signalPID
	t.Cleanup(func() { signalPID = prevSignal })
	signalPID = func(pid int, sig syscall.Signal) error {
		calls++
		return nil
	}

	stopDevStackFor("/Users/aaron/.argus/worktrees/nope/nope")
	testutil.Equal(t, calls, 0)
}

func TestStopDevStackFor_SigkillsSurvivors(t *testing.T) {
	origGrace := devStackGracePeriod
	devStackGracePeriod = time.Millisecond
	t.Cleanup(func() { devStackGracePeriod = origGrace })

	wt := "/Users/aaron/.argus/worktrees/proj/task"
	prevPgrep := pgrepOutput
	t.Cleanup(func() { pgrepOutput = prevPgrep })
	pgrepOutput = func() ([]byte, error) {
		return []byte("555 mysqld --datadir=" + wt + "/.devbox/virtenv/mysql80/data\n"), nil
	}

	var mu sync.Mutex
	var sigs []syscall.Signal
	prevSignal := signalPID
	t.Cleanup(func() { signalPID = prevSignal })
	signalPID = func(pid int, sig syscall.Signal) error {
		mu.Lock()
		sigs = append(sigs, sig)
		mu.Unlock()
		return nil
	}

	stopDevStackFor(wt)

	mu.Lock()
	defer mu.Unlock()
	testutil.DeepEqual(t, sigs, []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL})
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
	scriptPath := wt + "/mysqld"
	script := "#!/bin/sh\nwhile true; do sleep 1; done\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil { //nolint:gosec // test fixture, needs +x
		t.Fatalf("write fake mysqld script: %v", err)
	}

	cmd := exec.Command(scriptPath)
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
