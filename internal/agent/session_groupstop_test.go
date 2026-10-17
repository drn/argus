package agent

import (
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
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
	sess, err := StartSession(strings.ReplaceAll(t.Name(), "/", "-"), exec.Command("sh", "-c", script), 24, 80)
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
	leader := sess.PID()
	testutil.NoError(t, sess.StopScoped(StopAgentOnly))
	// Wait on the leader itself, not sess.Done(): on Linux the surviving
	// child keeps the PTY slave open, so Done (which waits for the PTY
	// drain) doesn't fire until the child exits.
	testutil.True(t, waitGone(leader, 3*time.Second))
	testutil.False(t, waitGone(child, 500*time.Millisecond))
}

func TestSession_StopScoped_RerenderEscalates(t *testing.T) {
	if testing.Short() {
		t.Skip("real PTY process")
	}
	prev := agentStopKillAfter.Swap(int64(400 * time.Millisecond))
	t.Cleanup(func() { agentStopKillAfter.Store(prev) })
	sess, child := startGroupSession(t, `trap "" TERM HUP; sleep 60 & echo CHILD=$!; wait`)
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })
	testutil.NoError(t, sess.StopScoped(StopRerender))
	time.Sleep(150 * time.Millisecond)
	testutil.NoError(t, syscall.Kill(sess.PID(), 0))
	testutil.True(t, waitGone(sess.PID(), 2*time.Second))
	select {
	case <-sess.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("descendant retained PTY through escalation")
	}
	testutil.True(t, waitGone(child, 2*time.Second))
}

func TestSession_StopScoped_RerenderCancelsBeforeDrain(t *testing.T) {
	if testing.Short() {
		t.Skip("real PTY process")
	}
	prev := agentStopKillAfter.Swap(int64(time.Second))
	t.Cleanup(func() { agentStopKillAfter.Store(prev) })
	var logs syncBuffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })
	sess, child := startGroupSession(t, `trap "" HUP; trap 'sleep 0.2; exit 0' TERM; sleep 60 & echo CHILD=$!; wait`)
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })
	testutil.NoError(t, sess.StopScoped(StopRerender))
	select {
	case <-sess.processExited:
	case <-time.After(2 * time.Second):
		t.Fatal("process exit did not cancel escalation before PTY drain")
	}
	testutil.NoError(t, syscall.Kill(child, 0))
	deadline := time.Now().Add(1500 * time.Millisecond)
	for !strings.Contains(logs.String(), `msg="[rerender] exited during grace" task=`+sess.TaskID) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	testutil.Contains(t, logs.String(), `msg="[rerender] exited during grace" task=`+sess.TaskID)
	testutil.NoError(t, syscall.Kill(child, syscall.SIGKILL))
	<-sess.Done()
	testutil.NoError(t, sess.Err())
}

func TestRerenderStop_DefaultGrace(t *testing.T) {
	testutil.Equal(t, RerenderStopGrace, 3*time.Second)
	testutil.Equal(t, time.Duration(agentStopKillAfter.Load()), RerenderStopGrace)
}

func TestSession_ProcessExitPrecedesDrain(t *testing.T) {
	if testing.Short() {
		t.Skip("external process")
	}
	cmd := exec.Command("true")
	testutil.NoError(t, cmd.Start())
	master, err := os.CreateTemp(t.TempDir(), "master")
	testutil.NoError(t, err)
	s := &Session{Cmd: cmd, ptmx: master, processExited: make(chan struct{}), readDone: make(chan struct{}), done: make(chan struct{})}
	go s.waitLoop()
	t.Cleanup(func() { close(s.readDone); <-s.done })
	select {
	case <-s.processExited:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("processExited waited for PTY drain")
	}
	select {
	case <-s.done:
		t.Fatal("Done closed before readDone")
	default:
	}
}

func TestRunner_OrdinaryKicksDoNotEscalate(t *testing.T) {
	if testing.Short() {
		t.Skip("real PTY process")
	}
	for _, revive := range []bool{false, true} {
		name := "switch"
		if revive {
			name = "revive"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			prev := agentStopKillAfter.Swap(int64(50 * time.Millisecond))
			t.Cleanup(func() { agentStopKillAfter.Store(prev) })
			cfg := runnerTestConfig()
			cfg.Backends["test"] = config.Backend{Command: `trap "" TERM HUP; echo READY; sleep 60 </dev/null >/dev/null 2>&1 & wait`}
			r := NewRunner(nil)
			task := &model.Task{ID: name, Name: name, Worktree: t.TempDir()}
			sess, err := r.Start(task, cfg, 24, 80, false)
			testutil.NoError(t, err)
			t.Cleanup(func() { r.StopAll(); _ = syscall.Kill(-sess.PID(), syscall.SIGKILL) })
			deadline := time.Now().Add(2 * time.Second)
			for !strings.Contains(string(sess.RecentOutput()), "READY") && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
			}
			testutil.Contains(t, string(sess.RecentOutput()), "READY")
			if revive {
				err = r.Revive(task, cfg, 30, 98)
			} else {
				err = r.KickRerender(task, cfg, 30, 98)
			}
			testutil.NoError(t, err)
			time.Sleep(300 * time.Millisecond)
			testutil.NoError(t, syscall.Kill(sess.PID(), 0))
		})
	}
}

func TestSession_StopScoped_AgentOnlyDoesNotEscalate(t *testing.T) {
	if testing.Short() {
		t.Skip("real PTY process")
	}
	prev := agentStopKillAfter.Swap(int64(50 * time.Millisecond))
	t.Cleanup(func() { agentStopKillAfter.Store(prev) })
	sess, child := startGroupSession(t, `trap "" TERM HUP; sleep 60 & echo CHILD=$!; wait`)
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })
	testutil.NoError(t, sess.StopScoped(StopAgentOnly))
	time.Sleep(500 * time.Millisecond)
	testutil.NoError(t, syscall.Kill(sess.PID(), 0))
}

func TestRunner_WidthKickEscalatesAndRestarts(t *testing.T) {
	if testing.Short() {
		t.Skip("real PTY process")
	}
	t.Setenv("HOME", t.TempDir())
	prev := agentStopKillAfter.Swap(int64(50 * time.Millisecond))
	t.Cleanup(func() { agentStopKillAfter.Store(prev) })
	cfg := runnerTestConfig()
	cfg.Backends["test"] = config.Backend{Command: `trap "" TERM HUP; sleep 60 </dev/null >/dev/null 2>&1 & echo CHILD=$!; wait`}
	r := NewRunner(nil)
	task := &model.Task{ID: "width-kick", Name: "width", Worktree: t.TempDir()}
	sess, err := r.Start(task, cfg, 24, 80, false)
	testutil.NoError(t, err)
	t.Cleanup(func() {
		current := r.Get(task.ID)
		r.StopAll()
		if current != nil {
			_ = syscall.Kill(-current.PID(), syscall.SIGKILL)
		}
	})
	oldPID := sess.PID()
	deadline := time.Now().Add(2 * time.Second)
	var child int
	for time.Now().Before(deadline) {
		if m := childPIDRe.FindSubmatch(sess.RecentOutput()); m != nil {
			child, _ = strconv.Atoi(string(m[1]))
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	testutil.True(t, child > 0)
	t.Cleanup(func() {
		if child > 0 {
			_ = syscall.Kill(child, syscall.SIGKILL)
		}
	})
	testutil.NoError(t, r.KickWidthRerender(task, cfg, 30, 98))
	deadline = time.Now().Add(2 * time.Second)
	var replacement SessionHandle
	for time.Now().Before(deadline) {
		replacement = r.Get(task.ID)
		if !r.HasPendingRestart(task.ID) && replacement != nil && replacement.PID() != oldPID {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	testutil.NotNil(t, replacement)
	testutil.False(t, r.HasPendingRestart(task.ID))
	testutil.True(t, replacement.PID() != oldPID)
	testutil.True(t, waitGone(child, 2*time.Second))
}

func TestStopScope_String(t *testing.T) {
	testutil.Equal(t, StopTree.String(), "tree")
	testutil.Equal(t, StopAgentOnly.String(), "agent-only")
	testutil.Equal(t, StopRerender.String(), "rerender")
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
