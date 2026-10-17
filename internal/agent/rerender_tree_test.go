package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

func TestRerenderTreeHelper(t *testing.T) {
	if os.Getenv("ARGUS_TEST_RERENDER_TREE_HELPER") != "1" {
		return
	}
	signal.Ignore(syscall.SIGTERM, syscall.SIGHUP)
	child := exec.Command("sh", "-c", `trap "" TERM HUP; echo DETACHED_READY; exec sleep 60`)
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := child.Start(); err != nil {
		os.Exit(2)
	}
	fmt.Printf("CHILD=%d\n", child.Process.Pid)
	_ = child.Wait()
}

func TestRerenderEscalation_DetachedTree(t *testing.T) {
	if testing.Short() {
		t.Skip("real PTY processes")
	}
	t.Setenv("HOME", t.TempDir())
	var logs syncBuffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })
	prev := agentStopKillAfter.Swap(int64(50 * time.Millisecond))
	t.Cleanup(func() { agentStopKillAfter.Store(prev) })
	outside := exec.Command("sleep", "60")
	testutil.NoError(t, outside.Start())
	t.Cleanup(func() { _ = outside.Process.Kill(); _ = outside.Wait() })
	cmd := exec.Command(os.Args[0], "-test.run=^TestRerenderTreeHelper$")
	cmd.Env = append(os.Environ(), "ARGUS_TEST_RERENDER_TREE_HELPER=1")
	sess, err := StartSession("rerender-detached", cmd, 24, 80)
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = syscall.Kill(-sess.PID(), syscall.SIGKILL) })
	var child int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		out := sess.RecentOutput()
		if m := childPIDRe.FindSubmatch(out); m != nil && strings.Contains(string(out), "DETACHED_READY") {
			child, _ = strconv.Atoi(string(m[1]))
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	testutil.True(t, child > 0)
	if child > 0 {
		t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })
	}
	pgid, err := syscall.Getpgid(child)
	testutil.NoError(t, err)
	testutil.Equal(t, pgid, child)
	testutil.True(t, pgid != sess.PID())
	nodes, err := rerenderProcessSnapshot(context.Background())
	testutil.NoError(t, err)
	checkedReuse := false
	for _, node := range nodes {
		if int(node.pid) == outside.Process.Pid {
			node.started++
			sent, err := signalRerenderDescendant(node, syscall.SIGKILL)
			testutil.NoError(t, err)
			testutil.False(t, sent)
			checkedReuse = true
		}
	}
	testutil.True(t, checkedReuse)
	testutil.NoError(t, sess.StopScoped(StopRerender))
	select {
	case <-sess.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("detached helper held PTY until drain timeout")
	}
	testutil.True(t, waitGone(child, time.Second))
	for _, node := range nodes {
		if int(node.pid) == sess.PID() {
			sent, err := signalRerenderDescendant(node, syscall.SIGKILL)
			testutil.NoError(t, err)
			testutil.False(t, sent)
		}
	}
	testutil.NoError(t, syscall.Kill(outside.Process.Pid, 0))
	deadline = time.Now().Add(time.Second)
	for !strings.Contains(logs.String(), "descendants reaped") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	testutil.Contains(t, logs.String(), "task=rerender-detached count=1 matched=1")
}

func TestSelectRerenderDescendants(t *testing.T) {
	nodes := []rerenderProcess{{pid: 10, parent: 1}, {pid: 11, parent: 10}, {pid: 12, parent: 11}, {pid: 13, parent: 10}, {pid: 20, parent: 1}, {pid: 1, parent: 10}, {pid: 30, parent: 31}, {pid: 31, parent: 30}}
	got := selectRerenderDescendants(nodes, 10)
	want := []rerenderProcess{nodes[2], nodes[3], nodes[1]}
	testutil.Equal(t, len(got), len(want))
	for i, n := range got {
		testutil.Equal(t, n, want[i])
	}
	for _, root := range []int{0, 1, -1, 99} {
		t.Run(strconv.Itoa(root), func(t *testing.T) { testutil.Equal(t, len(selectRerenderDescendants(nodes, root)), 0) })
	}
}

func TestReapRerenderDescendants(t *testing.T) {
	for _, phase := range []string{"success", "term error", "kill error", "empty"} {
		t.Run(phase, func(t *testing.T) {
			nodes := []rerenderProcess{{pid: 10}, {pid: 11}}
			if phase == "empty" {
				nodes = nil
			}
			var signals []syscall.Signal
			waited := false
			stats := reapRerenderDescendants(nodes, func(n rerenderProcess, sig syscall.Signal) (bool, error) {
				signals = append(signals, sig)
				if phase == "term error" && sig == syscall.SIGTERM || phase == "kill error" && sig == syscall.SIGKILL {
					return false, errors.New("boom")
				}
				return n.pid == 10, nil // pid11 exited or its identity changed
			}, func(grace time.Duration) {
				waited = true
				testutil.Equal(t, grace, 250*time.Millisecond)
				testutil.DeepEqual(t, signals, []syscall.Signal{syscall.SIGTERM, syscall.SIGTERM})
			})
			testutil.Equal(t, stats.matched, len(nodes))
			if phase == "empty" {
				testutil.False(t, waited)
				return
			}
			testutil.True(t, waited)
			if phase == "success" {
				testutil.Equal(t, stats, rerenderReapStats{matched: 2, term: 1, killed: 1, reaped: 2})
			} else {
				testutil.Equal(t, stats.failed, 2)
			}
		})
	}
}

func TestRerenderSnapshotCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := rerenderProcessSnapshot(ctx)
	testutil.ErrorIs(t, err, context.Canceled)
}

func TestRerenderEscalation_AlreadyExited(t *testing.T) {
	if testing.Short() {
		t.Skip("external process")
	}
	cmd := exec.Command("true")
	testutil.NoError(t, cmd.Run())
	var logs syncBuffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	s := &Session{TaskID: "already-exited", Cmd: cmd, processExited: make(chan struct{})}
	s.escalateRerender(0)
	testutil.Contains(t, logs.String(), "escalation skipped")
	testutil.False(t, strings.Contains(logs.String(), "descendants reaped"))
	close(s.processExited)
	s.escalateRerender(0) // canceled before any inspection
}
