package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
	"github.com/shirou/gopsutil/v4/process"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv("ARGUS_TEST_SHARED_DAEMON_HELPER"); mode != "" {
		runSharedDaemonHelper(mode)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// The same test executable runs under a fake "claude daemon run" argv. No
// installed Claude, live daemon, network socket or real home is involved.
func runSharedDaemonHelper(mode string) {
	signal.Ignore(syscall.SIGTERM, syscall.SIGHUP)
	if mode == "hosted" {
		requests := make(chan os.Signal, 1)
		signal.Notify(requests, syscall.SIGUSR1)
		_ = os.WriteFile(os.Getenv("ARGUS_TEST_SHARED_DAEMON_REPORT")+".hosted-ready", []byte("ready"), 0o600)
		for range requests {
			_ = os.WriteFile(os.Getenv("ARGUS_TEST_SHARED_DAEMON_REPORT")+".hosted-alive", []byte("responding"), 0o600)
		}
		return
	}
	child := exec.Command("sh", "-c", `trap "" TERM HUP; exec sleep 60`)
	if mode == "daemon" {
		exe, err := os.Executable()
		if err != nil {
			os.Exit(5)
		}
		child = exec.Command(exe)
		child.Env = append(os.Environ(), "ARGUS_TEST_SHARED_DAEMON_HELPER=hosted")
	}
	if err := child.Start(); err != nil {
		os.Exit(2)
	}
	if mode == "daemon" {
		requests := make(chan os.Signal, 1)
		signal.Notify(requests, syscall.SIGUSR1)
		if err := os.WriteFile(os.Getenv("ARGUS_TEST_SHARED_DAEMON_REPORT"), []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			os.Exit(3)
		}
		exited := make(chan struct{})
		go func() { _ = child.Wait(); close(exited) }()
		for {
			select {
			case <-requests:
				_ = os.WriteFile(os.Getenv("ARGUS_TEST_SHARED_DAEMON_REPORT")+".alive", []byte("responding"), 0o600)
			case <-exited:
				return
			}
		}
	}
	exe, err := os.Executable()
	if err != nil {
		os.Exit(6)
	}
	daemon := exec.Command(exe, "daemon", "run")
	daemon.Args[0] = "claude"
	daemon.Env = append(os.Environ(), "ARGUS_TEST_SHARED_DAEMON_HELPER=daemon")
	daemon.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := daemon.Start(); err != nil {
		os.Exit(4)
	}
	fmt.Printf("DAEMON=%d HELPER=%d\n", daemon.Process.Pid, child.Process.Pid)
	_ = daemon.Wait()
}

func TestRerenderEscalation_PreservesSharedClaudeDaemon(t *testing.T) {
	if testing.Short() {
		t.Skip("real process ancestry")
	}
	t.Setenv("HOME", t.TempDir())
	var logs syncBuffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	prevGrace := agentStopKillAfter.Swap(int64(50 * time.Millisecond))
	t.Cleanup(func() { agentStopKillAfter.Store(prevGrace) })
	exe, err := os.Executable()
	testutil.NoError(t, err)
	report := filepath.Join(t.TempDir(), "hosted-pid")
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), "ARGUS_TEST_SHARED_DAEMON_HELPER=root", "ARGUS_TEST_SHARED_DAEMON_REPORT="+report)
	sess, err := StartSession("shared-daemon", cmd, 24, 80)
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = syscall.Kill(-sess.PID(), syscall.SIGKILL) })
	var daemonPID, helperPID, hostedPID int
	t.Cleanup(func() {
		for _, pid := range []int{hostedPID, daemonPID, helperPID} {
			if pid > 1 {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, _ = fmt.Sscanf(strings.TrimSpace(string(sess.RecentOutput())), "DAEMON=%d HELPER=%d", &daemonPID, &helperPID)
		if data, readErr := os.ReadFile(report); readErr == nil {
			hostedPID, _ = strconv.Atoi(string(data))
		}
		_, readyErr := os.Stat(report + ".hosted-ready")
		if daemonPID > 1 && helperPID > 1 && hostedPID > 1 && readyErr == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if daemonPID <= 1 || helperPID <= 1 || hostedPID <= 1 {
		t.Fatal("fake daemon and hosted child did not become ready")
	}
	testutil.NoError(t, sess.StopScoped(StopRerender))
	select {
	case <-sess.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("ordinary helper kept the repair alive")
	}
	testutil.True(t, waitGone(helperPID, time.Second))
	testutil.NoError(t, syscall.Kill(daemonPID, 0))
	testutil.NoError(t, syscall.Kill(hostedPID, 0))
	testutil.NoError(t, syscall.Kill(daemonPID, syscall.SIGUSR1))
	testutil.NoError(t, syscall.Kill(hostedPID, syscall.SIGUSR1))
	deadline = time.Now().Add(time.Second)
	var response, hostedResponse []byte
	for time.Now().Before(deadline) {
		response, _ = os.ReadFile(report + ".alive")
		hostedResponse, _ = os.ReadFile(report + ".hosted-alive")
		if len(response) > 0 && len(hostedResponse) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	testutil.Equal(t, string(response), "responding")
	testutil.Equal(t, string(hostedResponse), "responding")
	testutil.Contains(t, logs.String(), "preserving shared Claude daemon")
}

func TestSharedClaudeDaemonArgv(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
		want bool
	}{
		{"native", []string{"/usr/local/bin/claude", "daemon", "run"}, true},
		{"bare", []string{"claude", "daemon"}, true},
		{"versioned", []string{"/home/user/.local/share/claude/versions/2.1.110", "daemon", "run"}, true},
		{"npm", []string{"/usr/bin/node", "/usr/lib/node_modules/@anthropic-ai/claude-code/cli.js", "daemon", "run"}, true},
		{"nodejs", []string{"nodejs", "/pkg/@anthropic-ai/claude-code/cli.js", "daemon"}, true},
		{"empty", nil, false},
		{"no subcommand", []string{"claude"}, false},
		{"ordinary agent", []string{"claude", "--resume", "daemon"}, false},
		{"ordinary helper", []string{"node", "/work/dev-server.js", "daemon"}, false},
		{"other binary", []string{"my-claude", "daemon"}, false},
		{"other cli", []string{"node", "/work/cli.js", "daemon"}, false},
		{"similar command", []string{"claude", "daemon-helper"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) { testutil.Equal(t, isSharedClaudeDaemon(tc.argv), tc.want) })
	}
}

func TestSelectRerenderDescendants_SharedBoundary(t *testing.T) {
	nodes := []rerenderProcess{
		{pid: 10, parent: 1},
		{pid: 11, parent: 10, shared: true},
		{pid: 12, parent: 11},
		{pid: 13, parent: 12},
		{pid: 14, parent: 10},
		{pid: 15, parent: 14},
		{pid: 16, parent: 14, shared: true},
		{pid: 17, parent: 16},
		{pid: 20, parent: 99}, // an uninspectable parent disconnects its subtree
	}
	got := selectRerenderDescendants(nodes, 10)
	testutil.Equal(t, len(got), 2)
	for i, want := range []rerenderProcess{nodes[5], nodes[4]} {
		if i < len(got) {
			testutil.Equal(t, got[i], want)
		}
	}
}

func TestRerenderSnapshot_UninspectableArgv(t *testing.T) {
	for _, tc := range []struct {
		name     string
		argv     []string
		err      error
		included bool
	}{
		{"ordinary", []string{"helper"}, nil, true},
		{"read error", nil, errors.New("denied"), false},
		{"empty argv", nil, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inspected := false
			nodes, err := rerenderProcessSnapshotWithArgv(context.Background(), func(p *process.Process, ctx context.Context) ([]string, error) {
				if int(p.Pid) == os.Getpid() {
					inspected = true
					return tc.argv, tc.err
				}
				return p.CmdlineSliceWithContext(ctx)
			})
			testutil.NoError(t, err)
			testutil.True(t, inspected)
			found := false
			for _, n := range nodes {
				if int(n.pid) == os.Getpid() {
					found = true
				}
			}
			testutil.Equal(t, found, tc.included)
		})
	}
}

func TestRerenderSnapshot_ArgvWarningsAggregated(t *testing.T) {
	for _, mode := range []string{"readable", "error", "empty", "mixed"} {
		t.Run(mode, func(t *testing.T) {
			var logs syncBuffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })
			failures := 0
			calls := 0
			nodes, err := rerenderProcessSnapshotWithArgv(context.Background(), func(*process.Process, context.Context) ([]string, error) {
				calls++
				if mode == "empty" {
					failures++
					return nil, nil
				}
				if mode == "error" || mode == "mixed" && calls%2 == 0 {
					failures++
					return nil, errors.New("per-process denial detail")
				}
				return []string{"helper"}, nil
			})
			testutil.NoError(t, err)
			testutil.Equal(t, len(nodes), calls-failures)
			if mode == "readable" {
				testutil.Equal(t, logs.String(), "")
				return
			}
			testutil.True(t, failures > 1)
			testutil.Equal(t, strings.Count(logs.String(), "argv inspection skipped"), 1)
			testutil.True(t, strings.Contains(logs.String(), fmt.Sprintf("count=%d\n", failures)))
			testutil.False(t, strings.Contains(logs.String(), "per-process denial detail"))
		})
	}
}
