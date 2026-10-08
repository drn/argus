package agent

import (
	"os/exec"
	"regexp"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

var childPIDRe = regexp.MustCompile(`CHILD=(\d+)`)

// startGroupSession starts script under a PTY session and returns the session
// plus the pid the script reports as its background child (same process
// group — a non-interactive sh does no job control, mirroring how an agent's
// stdio MCP servers share its group).
func startGroupSession(t *testing.T, script string) (*Session, int) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	sess, err := StartSession("group-stop", exec.Command("sh", "-c", script), 24, 80)
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = syscall.Kill(-sess.PID(), syscall.SIGKILL) })
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m := childPIDRe.FindSubmatch(sess.RecentOutput()); m != nil {
			pid, _ := strconv.Atoi(string(m[1]))
			return sess, pid
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("child pid never reported; output=%q", sess.RecentOutput())
	return nil, 0
}

func waitGone(pid int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func setStopKillAfter(t *testing.T, d time.Duration) {
	t.Helper()
	prev := stopKillAfter.Swap(int64(d))
	t.Cleanup(func() { stopKillAfter.Store(prev) })
}

func TestSession_Stop_KillsProcessGroup(t *testing.T) {
	// The child ignores SIGHUP, so the tty hangup the kernel sends when the
	// leader dies can't kill it: only a signal to the whole group can — a
	// PID-only stop leaves it reparented to launchd.
	sess, child := startGroupSession(t, `trap "" HUP; sleep 60 & echo CHILD=$!; wait`)
	testutil.NoError(t, sess.Stop())
	<-sess.Done()
	testutil.True(t, waitGone(child, 3*time.Second))
}

func TestSession_Stop_EscalatesToSIGKILL(t *testing.T) {
	setStopKillAfter(t, 200*time.Millisecond)
	// The whole group ignores SIGTERM (SIG_IGN survives exec), like an MCP
	// server that hangs on shutdown.
	sess, child := startGroupSession(t, `trap "" TERM HUP; sleep 60 & echo CHILD=$!; wait`)
	testutil.NoError(t, sess.Stop())
	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("leader survived SIGKILL escalation")
	}
	testutil.True(t, waitGone(child, 3*time.Second))
}

func TestStopSignalTarget(t *testing.T) {
	tests := []struct {
		name                string
		pid, pgid, selfPgid int
		want                int
	}{
		{"own group leader → group", 100, 100, 7, -100},
		{"pgid lookup failed → pid only", 100, 0, 7, 100},
		{"init group → pid only", 100, 1, 7, 100},
		{"shares our group → pid only", 100, 7, 7, 100},
		{"not its own group leader → pid only", 100, 90, 7, 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testutil.Equal(t, stopSignalTarget(tt.pid, tt.pgid, tt.selfPgid), tt.want)
		})
	}
}

func TestSession_StopScoped_AgentOnlyLeavesGroup(t *testing.T) {
	// A kick/recycle/resize bounce must not take down the agent's own
	// background processes (dev servers) — only the agent itself.
	sess, child := startGroupSession(t, `trap "" HUP; sleep 60 & echo CHILD=$!; wait`)
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })
	testutil.NoError(t, sess.StopScoped(StopAgentOnly))
	<-sess.Done()
	testutil.False(t, waitGone(child, 500*time.Millisecond))
}

func TestStopScope_String(t *testing.T) {
	testutil.Equal(t, StopTree.String(), "tree")
	testutil.Equal(t, StopAgentOnly.String(), "agent-only")
	testutil.Equal(t, StopScope(9).String(), "unknown")
}

// runnerGroupConfig runs a backend that backgrounds a HUP-ignoring child in
// the agent's process group and reports its pid.
func runnerGroupConfig() config.Config {
	cfg := runnerTestConfig()
	cfg.Backends["test"] = config.Backend{Command: `trap "" HUP; sleep 60 & echo CHILD=$!; wait`}
	return cfg
}

func startRunnerGroupSession(t *testing.T, r *Runner, id string) int {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	task := &model.Task{ID: id, Name: id, Worktree: t.TempDir()}
	h, err := r.Start(task, runnerGroupConfig(), 24, 80, false)
	testutil.NoError(t, err)
	// A kick/recycle restarts the session asynchronously: wait out the
	// restart, then tree-stop whatever replacement is running.
	t.Cleanup(func() {
		deadline := time.Now().Add(3 * time.Second)
		for r.HasPendingRestart(id) && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		r.StopAll()
	})
	sess := h.(*Session)
	t.Cleanup(func() { _ = syscall.Kill(-sess.PID(), syscall.SIGKILL) })
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m := childPIDRe.FindSubmatch(sess.RecentOutput()); m != nil {
			pid, _ := strconv.Atoi(string(m[1]))
			t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
			return pid
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("child pid never reported")
	return 0
}

func TestRunner_KickKeepsGroupAlive_StopKillsIt(t *testing.T) {
	t.Run("kick rerender", func(t *testing.T) {
		r := NewRunner(nil)
		child := startRunnerGroupSession(t, r, "kick")
		task := &model.Task{ID: "kick", Name: "kick", Worktree: t.TempDir()}
		testutil.NoError(t, r.KickRerender(task, runnerGroupConfig(), 24, 80))
		testutil.False(t, waitGone(child, 500*time.Millisecond))
	})
	t.Run("recycle", func(t *testing.T) {
		r := NewRunner(nil)
		child := startRunnerGroupSession(t, r, "recycle")
		task := &model.Task{ID: "recycle", Name: "recycle", Worktree: t.TempDir()}
		testutil.NoError(t, r.Recycle(task, runnerGroupConfig(), 24, 80))
		testutil.False(t, waitGone(child, 500*time.Millisecond))
	})
	t.Run("stop (finished / explicit)", func(t *testing.T) {
		r := NewRunner(nil)
		child := startRunnerGroupSession(t, r, "stop")
		testutil.NoError(t, r.Stop("stop"))
		testutil.True(t, waitGone(child, 3*time.Second))
	})
	t.Run("stop scoped agent-only", func(t *testing.T) {
		r := NewRunner(nil)
		child := startRunnerGroupSession(t, r, "scoped")
		testutil.NoError(t, r.StopScoped("scoped", StopAgentOnly))
		testutil.False(t, waitGone(child, 500*time.Millisecond))
	})
	t.Run("stop scoped unknown task", func(t *testing.T) {
		testutil.ErrorIs(t, NewRunner(nil).StopScoped("nope", StopTree), ErrSessionNotFound)
	})
}
